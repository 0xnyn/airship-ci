// Package cacheproxy is the cache accelerator that runs on each runner
// machine when a definition sets extra_config.cache: true. It speaks
// GitHub's Actions cache protocol on 127.0.0.1 and stores entries in an S3
// bucket in the customer's account, so actions/cache, setup-* actions and
// docker/build-push-action keep working unchanged with no size limits.
//
// Two listeners: the cache service itself (twirp v2, the Azure block blob
// upload calls the clients make against the "signed" URLs it hands out,
// and the legacy v1 REST API), and an HTTPS proxy that the runner's
// HTTPS_PROXY points at. The proxy terminates TLS for GitHub's results
// host with a locally trusted CA, answers CacheService calls itself and
// forwards everything else untouched.
package cacheproxy

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"sort"
	"strings"
	"sync"
	"time"

	awssdk "github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"golang.org/x/sync/errgroup"
)

// Object is a stored cache entry.
type Object struct {
	Key          string
	Size         int64
	LastModified time.Time
}

// ErrNotFound is returned for a missing object.
var ErrNotFound = errors.New("object not found")

// Store is the object storage behind the cache.
type Store interface {
	// Head returns an object's size.
	Head(ctx context.Context, key string) (int64, error)
	// Get returns length bytes of an object from offset (length < 0: to the
	// end), and the object's total size.
	Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error)
	// Put stores an object of size bytes read from body.
	Put(ctx context.Context, key string, size int64, body io.Reader) error
	// List returns the objects under prefix.
	List(ctx context.Context, prefix string) ([]Object, error)
}

// S3Store keeps entries in one bucket. When the bucket's owner is known,
// every call names it, so a bucket of the same name in another account is
// refused rather than read from or written to.
type S3Store struct {
	client  *s3.Client
	bucket  string
	account string
}

// WithOwner sets the account the bucket must belong to.
func (s *S3Store) WithOwner(account string) *S3Store {
	s.account = account

	return s
}

func (s *S3Store) owner() *string {
	if s.account == "" {
		return nil
	}

	return &s.account
}

const (
	uploadPartSize    = 64 << 20
	uploadConcurrency = 8
)

// NewS3Store returns a Store over a bucket.
func NewS3Store(client *s3.Client, bucket string) *S3Store {
	return &S3Store{client: client, bucket: bucket}
}

// Head implements Store.
func (s *S3Store) Head(ctx context.Context, key string) (int64, error) {
	out, err := s.client.HeadObject(ctx, &s3.HeadObjectInput{Bucket: &s.bucket, Key: &key, ExpectedBucketOwner: s.owner()})
	if isNotFound(err) {
		return 0, ErrNotFound
	}
	if err != nil {
		return 0, fmt.Errorf("head %s: %w", key, err)
	}

	return awssdk.ToInt64(out.ContentLength), nil
}

// Get implements Store.
func (s *S3Store) Get(ctx context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	in := &s3.GetObjectInput{Bucket: &s.bucket, Key: &key, ExpectedBucketOwner: s.owner()}
	if offset > 0 || length >= 0 {
		if length < 0 {
			in.Range = awssdk.String(fmt.Sprintf("bytes=%d-", offset))
		} else {
			in.Range = awssdk.String(fmt.Sprintf("bytes=%d-%d", offset, offset+length-1))
		}
	}
	out, err := s.client.GetObject(ctx, in)
	if isNotFound(err) {
		return nil, 0, ErrNotFound
	}
	if err != nil {
		return nil, 0, fmt.Errorf("get %s: %w", key, err)
	}
	total := awssdk.ToInt64(out.ContentLength)
	if cr := awssdk.ToString(out.ContentRange); cr != "" {
		if _, after, ok := strings.Cut(cr, "/"); ok {
			_, _ = fmt.Sscanf(after, "%d", &total)
		}
	}

	return out.Body, total, nil
}

// Put implements Store. Small objects go in one request; larger ones as
// a multipart upload with parts sent in parallel, which is what saturates
// the instance's network. A body that can be read at any offset (the
// staged files) is sent from disk, so a 2 GB entry costs no memory and a
// part can be retried; any other body is read through a bounded set of
// buffers. An upload that fails is aborted so no parts are left behind
// (the bucket's lifecycle rule also sweeps them).
func (s *S3Store) Put(ctx context.Context, key string, size int64, body io.Reader) error {
	if size >= 0 && size <= uploadPartSize {
		if _, err := s.client.PutObject(ctx, &s3.PutObjectInput{Bucket: &s.bucket, Key: &key, Body: body, ContentLength: &size, ExpectedBucketOwner: s.owner()}); err != nil {
			return fmt.Errorf("put %s: %w", key, err)
		}

		return nil
	}
	created, err := s.client.CreateMultipartUpload(ctx, &s3.CreateMultipartUploadInput{Bucket: &s.bucket, Key: &key, ExpectedBucketOwner: s.owner()})
	if err != nil {
		return fmt.Errorf("start upload %s: %w", key, err)
	}
	uploadID := created.UploadId
	abort := func() {
		_, _ = s.client.AbortMultipartUpload(context.WithoutCancel(ctx), &s3.AbortMultipartUploadInput{Bucket: &s.bucket, Key: &key, UploadId: uploadID, ExpectedBucketOwner: s.owner()})
	}

	var (
		mu    sync.Mutex
		parts []s3types.CompletedPart
	)
	group, groupCtx := errgroup.WithContext(ctx)
	group.SetLimit(uploadConcurrency)
	send := func(number int32, part io.Reader, length int64) {
		group.Go(func() error {
			out, err := s.client.UploadPart(groupCtx, &s3.UploadPartInput{
				Bucket: &s.bucket, Key: &key, UploadId: uploadID, PartNumber: awssdk.Int32(number),
				Body: part, ContentLength: awssdk.Int64(length), ExpectedBucketOwner: s.owner(),
			})
			if err != nil {
				return fmt.Errorf("upload part %d: %w", number, err)
			}
			mu.Lock()
			parts = append(parts, s3types.CompletedPart{ETag: out.ETag, PartNumber: awssdk.Int32(number)})
			mu.Unlock()

			return nil
		})
	}
	if at, seekable := body.(io.ReaderAt); seekable && size >= 0 {
		for number, offset := int32(1), int64(0); offset < size; number, offset = number+1, offset+uploadPartSize {
			length := min(int64(uploadPartSize), size-offset)
			send(number, io.NewSectionReader(at, offset, length), length)
		}
	} else {
		// One buffer per part in flight, handed back when the part is sent.
		buffers := make(chan []byte, uploadConcurrency)
		for range uploadConcurrency {
			buffers <- make([]byte, uploadPartSize)
		}
		for number := int32(1); ; number++ {
			buf := <-buffers
			n, readErr := io.ReadFull(body, buf)
			if readErr != nil && !errors.Is(readErr, io.ErrUnexpectedEOF) && !errors.Is(readErr, io.EOF) {
				abort()

				return fmt.Errorf("read upload %s: %w", key, readErr)
			}
			if n == 0 && number > 1 {
				break
			}
			chunk := buf[:n]
			group.Go(func() error {
				defer func() { buffers <- buf }()
				out, err := s.client.UploadPart(groupCtx, &s3.UploadPartInput{
					Bucket: &s.bucket, Key: &key, UploadId: uploadID, PartNumber: awssdk.Int32(number),
					Body: bytes.NewReader(chunk), ContentLength: awssdk.Int64(int64(len(chunk))), ExpectedBucketOwner: s.owner(),
				})
				if err != nil {
					return fmt.Errorf("upload part %d: %w", number, err)
				}
				mu.Lock()
				parts = append(parts, s3types.CompletedPart{ETag: out.ETag, PartNumber: awssdk.Int32(number)})
				mu.Unlock()

				return nil
			})
			if n < uploadPartSize {
				break
			}
		}
	}
	if err := group.Wait(); err != nil {
		abort()

		return fmt.Errorf("put %s: %w", key, err)
	}
	sort.Slice(parts, func(i, j int) bool { return awssdk.ToInt32(parts[i].PartNumber) < awssdk.ToInt32(parts[j].PartNumber) })
	_, err = s.client.CompleteMultipartUpload(ctx, &s3.CompleteMultipartUploadInput{
		Bucket: &s.bucket, Key: &key, UploadId: uploadID, ExpectedBucketOwner: s.owner(),
		MultipartUpload: &s3types.CompletedMultipartUpload{Parts: parts},
	})
	if err != nil {
		abort()

		return fmt.Errorf("complete upload %s: %w", key, err)
	}

	return nil
}

// List implements Store.
func (s *S3Store) List(ctx context.Context, prefix string) ([]Object, error) {
	var out []Object
	paginator := s3.NewListObjectsV2Paginator(s.client, &s3.ListObjectsV2Input{Bucket: &s.bucket, Prefix: &prefix, ExpectedBucketOwner: s.owner()})
	for paginator.HasMorePages() {
		page, err := paginator.NextPage(ctx)
		if err != nil {
			return nil, fmt.Errorf("list %s: %w", prefix, err)
		}
		for _, obj := range page.Contents {
			out = append(out, Object{Key: awssdk.ToString(obj.Key), Size: awssdk.ToInt64(obj.Size), LastModified: awssdk.ToTime(obj.LastModified)})
		}
	}

	return out, nil
}

func isNotFound(err error) bool {
	var noKey *s3types.NoSuchKey
	var notFound *s3types.NotFound

	return errors.As(err, &noKey) || errors.As(err, &notFound)
}

// MemoryStore is an in-memory Store for tests.
type MemoryStore struct {
	mu      sync.Mutex
	objects map[string]memoryObject
}

type memoryObject struct {
	data     []byte
	modified time.Time
}

// NewMemoryStore returns an empty MemoryStore.
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{objects: map[string]memoryObject{}}
}

// Head implements Store.
func (m *MemoryStore) Head(_ context.Context, key string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return 0, ErrNotFound
	}

	return int64(len(obj.data)), nil
}

// Get implements Store.
func (m *MemoryStore) Get(_ context.Context, key string, offset, length int64) (io.ReadCloser, int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	obj, ok := m.objects[key]
	if !ok {
		return nil, 0, ErrNotFound
	}
	total := int64(len(obj.data))
	if offset > total {
		offset = total
	}
	end := total
	if length >= 0 && offset+length < total {
		end = offset + length
	}

	return io.NopCloser(bytes.NewReader(obj.data[offset:end])), total, nil
}

// Put implements Store.
func (m *MemoryStore) Put(_ context.Context, key string, _ int64, body io.Reader) error {
	data, err := io.ReadAll(body)
	if err != nil {
		return err //nolint:wrapcheck // test double
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.objects[key] = memoryObject{data: data, modified: time.Now()}

	return nil
}

// List implements Store.
func (m *MemoryStore) List(_ context.Context, prefix string) ([]Object, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var out []Object
	for key, obj := range m.objects {
		if strings.HasPrefix(key, prefix) {
			out = append(out, Object{Key: key, Size: int64(len(obj.data)), LastModified: obj.modified})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })

	return out, nil
}

// Keys returns every stored key, for tests.
func (m *MemoryStore) Keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	keys := make([]string, 0, len(m.objects))
	for key := range m.objects {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	return keys
}

// Touch backdates an object, for restore-key ordering tests.
func (m *MemoryStore) Touch(key string, at time.Time) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if obj, ok := m.objects[key]; ok {
		obj.modified = at
		m.objects[key] = obj
	}
}

// Object keys are <prefix>/<version>/<escaped key>. Escaping is per
// character, so a restore key's prefix stays a prefix once escaped.

func objectKey(prefix, version, key string) string {
	return objectDir(prefix, version) + url.PathEscape(key)
}

// objectDir is the "directory" of a version's entries; the prefix may be
// empty, in which case the bucket holds one namespace.
func objectDir(prefix, version string) string {
	if prefix == "" {
		return url.PathEscape(version) + "/"
	}

	return prefix + "/" + url.PathEscape(version) + "/"
}

func keyFromObject(prefix, version, object string) string {
	escaped := strings.TrimPrefix(object, objectDir(prefix, version))
	key, err := url.PathUnescape(escaped)
	if err != nil {
		return escaped
	}

	return key
}
