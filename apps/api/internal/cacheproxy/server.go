package cacheproxy

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

// TwirpPrefix is the path every CacheService call starts with.
const TwirpPrefix = "/twirp/github.actions.results.api.v1.CacheService/"

const (
	blobPath     = "/_blob/"
	downloadPath = "/_download/"
	v1Prefix     = "/_apis/artifactcache/"
	// maxJSONBody bounds twirp and v1 request bodies.
	maxJSONBody = 1 << 20
	// azureVersion is what the Azure SDK clients expect to see back.
	azureVersion = "2023-11-03"
	// uploadTTL is how long an unfinished upload is kept.
	uploadTTL = 6 * time.Hour
)

// Server serves the cache protocol over one Store, under one key prefix
// (the repository), so one bucket can hold many repositories' caches.
type Server struct {
	store  Store
	prefix string
	// baseURL is where clients reach this server, used in the URLs it
	// hands out.
	baseURL string
	tmp     string
	log     *slog.Logger

	mu      sync.Mutex
	uploads map[string]*upload
	nextID  int64
}

// upload is an entry being written: blocks staged on disk until the
// client commits them.
type upload struct {
	key, version string
	dir          string
	single       string            // Put Blob or v1 chunks: one file
	blocks       map[string]string // Azure block ID -> file
	order        []string          // committed block list
	started      time.Time
	v1Size       int64
}

// NewServer returns a Server. tmp is where uploads are staged.
func NewServer(store Store, prefix, baseURL, tmp string, logger *slog.Logger) *Server {
	return &Server{store: store, prefix: prefix, baseURL: strings.TrimRight(baseURL, "/"), tmp: tmp, log: logger, uploads: map[string]*upload{}}
}

// Handler routes the cache protocol.
func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("POST "+TwirpPrefix+"CreateCacheEntry", s.createEntry)
	mux.HandleFunc("POST "+TwirpPrefix+"FinalizeCacheEntryUpload", s.finalizeEntry)
	mux.HandleFunc("POST "+TwirpPrefix+"GetCacheEntryDownloadURL", s.downloadURL)
	mux.HandleFunc("PUT "+blobPath+"{id}", s.putBlob)
	mux.HandleFunc("HEAD "+downloadPath+"{token}", s.serveDownload)
	mux.HandleFunc("GET "+downloadPath+"{token}", s.serveDownload)
	mux.HandleFunc("GET "+v1Prefix+"cache", s.v1Lookup)
	mux.HandleFunc("POST "+v1Prefix+"caches", s.v1Reserve)
	mux.HandleFunc("PATCH "+v1Prefix+"caches/{id}", s.v1Upload)
	mux.HandleFunc("POST "+v1Prefix+"caches/{id}", s.v1Commit)
	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })

	return mux
}

// twirp v2

type createEntryRequest struct {
	Key     string `json:"key"`
	Version string `json:"version"`
}

type createEntryResponse struct {
	OK              bool   `json:"ok"`
	SignedUploadURL string `json:"signed_upload_url,omitempty"`
}

func (s *Server) createEntry(w http.ResponseWriter, r *http.Request) {
	var req createEntryRequest
	if !decodeJSON(w, r, &req) || !checkKey(w, req.Key, req.Version) {
		return
	}
	up, id, err := s.newUpload(req.Key, req.Version)
	if err != nil {
		twirpError(w, http.StatusInternalServerError, "internal", err.Error())

		return
	}
	s.log.Info("cache entry reserved", slog.String("key", req.Key), slog.String("upload", id))
	_ = up
	writeJSON(w, http.StatusOK, createEntryResponse{OK: true, SignedUploadURL: s.baseURL + blobPath + id})
}

type finalizeRequest struct {
	Key       string `json:"key"`
	Version   string `json:"version"`
	SizeBytes int64  `json:"size_bytes,string"`
}

// UnmarshalJSON accepts size_bytes as a number or a string: protobuf JSON
// writes int64 as strings, and some clients send numbers.
func (f *finalizeRequest) UnmarshalJSON(data []byte) error {
	var raw struct {
		Key       string          `json:"key"`
		Version   string          `json:"version"`
		SizeBytes json.RawMessage `json:"size_bytes"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return err //nolint:wrapcheck // reported as a bad request
	}
	f.Key, f.Version = raw.Key, raw.Version
	size := strings.Trim(string(raw.SizeBytes), `"`)
	if size != "" && size != "null" {
		n, err := strconv.ParseInt(size, 10, 64)
		if err != nil {
			return fmt.Errorf("size_bytes: %w", err)
		}
		f.SizeBytes = n
	}

	return nil
}

type finalizeResponse struct {
	OK      bool   `json:"ok"`
	EntryID string `json:"entry_id,omitempty"`
}

func (s *Server) finalizeEntry(w http.ResponseWriter, r *http.Request) {
	var req finalizeRequest
	if !decodeJSON(w, r, &req) || !checkKey(w, req.Key, req.Version) {
		return
	}
	up := s.takeUpload(req.Key, req.Version)
	if up == nil {
		twirpError(w, http.StatusNotFound, "not_found", "no upload in progress for this key")

		return
	}
	defer func() { _ = os.RemoveAll(up.dir) }()
	size, err := s.commit(r.Context(), up)
	if err != nil {
		s.log.Warn("cache commit failed", slog.String("key", req.Key), slog.Any("error", err))
		twirpError(w, http.StatusInternalServerError, "internal", err.Error())

		return
	}
	if req.SizeBytes > 0 && size != req.SizeBytes {
		s.log.Warn("cache size mismatch", slog.String("key", req.Key), slog.Int64("uploaded", size), slog.Int64("declared", req.SizeBytes))
	}
	s.log.Info("cache entry saved", slog.String("key", req.Key), slog.Int64("bytes", size))
	writeJSON(w, http.StatusOK, finalizeResponse{OK: true, EntryID: strconv.FormatInt(size, 10)})
}

type downloadURLRequest struct {
	Key         string   `json:"key"`
	RestoreKeys []string `json:"restore_keys"`
	Version     string   `json:"version"`
}

type downloadURLResponse struct {
	OK                bool   `json:"ok"`
	SignedDownloadURL string `json:"signed_download_url,omitempty"`
	MatchedKey        string `json:"matched_key,omitempty"`
}

func (s *Server) downloadURL(w http.ResponseWriter, r *http.Request) {
	var req downloadURLRequest
	if !decodeJSON(w, r, &req) || !checkKey(w, req.Key, req.Version) {
		return
	}
	matched, object, err := s.lookup(r.Context(), req.Key, req.RestoreKeys, req.Version)
	if err != nil {
		twirpError(w, http.StatusInternalServerError, "internal", err.Error())

		return
	}
	if object == "" {
		writeJSON(w, http.StatusOK, downloadURLResponse{OK: false})

		return
	}
	writeJSON(w, http.StatusOK, downloadURLResponse{OK: true, SignedDownloadURL: s.downloadURLFor(object), MatchedKey: matched})
}

// lookup finds the entry for a key, then for each restore key the newest
// entry whose key starts with it, all within one version.
func (s *Server) lookup(ctx context.Context, key string, restoreKeys []string, version string) (matched string, object string, err error) {
	exact := objectKey(s.prefix, version, key)
	if _, err := s.store.Head(ctx, exact); err == nil {
		return key, exact, nil
	} else if !errors.Is(err, ErrNotFound) {
		return "", "", err
	}
	for _, restoreKey := range restoreKeys {
		if restoreKey == "" {
			continue
		}
		objects, err := s.store.List(ctx, objectKey(s.prefix, version, restoreKey))
		if err != nil {
			return "", "", err
		}
		if len(objects) == 0 {
			continue
		}
		sort.Slice(objects, func(i, j int) bool { return objects[i].LastModified.After(objects[j].LastModified) })

		return keyFromObject(s.prefix, version, objects[0].Key), objects[0].Key, nil
	}

	return "", "", nil
}

func (s *Server) downloadURLFor(object string) string {
	return s.baseURL + downloadPath + base64.RawURLEncoding.EncodeToString([]byte(object))
}

// Azure block blob emulation: what the clients do with signed upload URLs.

func (s *Server) putBlob(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	s.mu.Lock()
	up, ok := s.uploads[id]
	s.mu.Unlock()
	if !ok {
		http.Error(w, "unknown upload", http.StatusNotFound)

		return
	}
	switch r.URL.Query().Get("comp") {
	case "block":
		blockID := r.URL.Query().Get("blockid")
		if blockID == "" {
			http.Error(w, "blockid is required", http.StatusBadRequest)

			return
		}
		path := filepath.Join(up.dir, "block-"+hex.EncodeToString([]byte(blockID)))
		if err := writeFile(path, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
		s.mu.Lock()
		up.blocks[blockID] = path
		s.mu.Unlock()
		azureHeaders(w)
		w.WriteHeader(http.StatusCreated)
	case "blocklist":
		var list struct {
			Blocks []struct {
				XMLName xml.Name
				ID      string `xml:",chardata"`
			} `xml:",any"`
		}
		if err := xml.NewDecoder(io.LimitReader(r.Body, maxJSONBody)).Decode(&list); err != nil {
			http.Error(w, "invalid block list: "+err.Error(), http.StatusBadRequest)

			return
		}
		s.mu.Lock()
		up.order = up.order[:0]
		for _, block := range list.Blocks {
			id := strings.TrimSpace(block.ID)
			if _, staged := up.blocks[id]; !staged {
				s.mu.Unlock()
				http.Error(w, "block list names an unknown block", http.StatusBadRequest)

				return
			}
			up.order = append(up.order, id)
		}
		s.mu.Unlock()
		azureHeaders(w)
		w.WriteHeader(http.StatusCreated)
	case "":
		// Put Blob: the whole archive in one request.
		path := filepath.Join(up.dir, "single")
		if err := writeFile(path, r.Body); err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)

			return
		}
		s.mu.Lock()
		up.single = path
		s.mu.Unlock()
		azureHeaders(w)
		w.WriteHeader(http.StatusCreated)
	default:
		http.Error(w, "unsupported operation", http.StatusBadRequest)
	}
}

func azureHeaders(w http.ResponseWriter) {
	h := w.Header()
	h.Set("x-ms-version", azureVersion)
	h.Set("x-ms-request-id", newID())
	h.Set("ETag", `"`+newID()+`"`)
	h.Set("Last-Modified", time.Now().UTC().Format(http.TimeFormat))
	h.Set("Content-Length", "0")
}

// serveDownload answers HEAD and ranged GET for an entry. Clients learn the
// size from HEAD, then fetch 4 MB ranges in parallel (actions/cache) or
// read from an offset (buildx).
func (s *Server) serveDownload(w http.ResponseWriter, r *http.Request) {
	raw, err := base64.RawURLEncoding.DecodeString(r.PathValue("token"))
	if err != nil {
		http.Error(w, "bad token", http.StatusBadRequest)

		return
	}
	object := string(raw)
	if !strings.HasPrefix(object, s.prefix+"/") {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}
	h := w.Header()
	h.Set("x-ms-version", azureVersion)
	h.Set("x-ms-blob-type", "BlockBlob")
	h.Set("Accept-Ranges", "bytes")
	h.Set("Content-Type", "application/octet-stream")
	if r.Method == http.MethodHead {
		size, err := s.store.Head(r.Context(), object)
		if errors.Is(err, ErrNotFound) {
			http.Error(w, "not found", http.StatusNotFound)

			return
		}
		if err != nil {
			http.Error(w, err.Error(), http.StatusBadGateway)

			return
		}
		h.Set("Content-Length", strconv.FormatInt(size, 10))
		w.WriteHeader(http.StatusOK)

		return
	}

	offset, length, ranged, ok := parseRange(r.Header.Get("Range"))
	if !ok {
		http.Error(w, "unsupported range", http.StatusRequestedRangeNotSatisfiable)

		return
	}
	body, total, err := s.store.Get(r.Context(), object, offset, length)
	if errors.Is(err, ErrNotFound) {
		http.Error(w, "not found", http.StatusNotFound)

		return
	}
	if err != nil {
		http.Error(w, err.Error(), http.StatusBadGateway)

		return
	}
	defer body.Close()
	if !ranged {
		h.Set("Content-Length", strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusOK)
		_, _ = io.Copy(w, body)

		return
	}
	if offset >= total {
		h.Set("Content-Range", "bytes */"+strconv.FormatInt(total, 10))
		w.WriteHeader(http.StatusRequestedRangeNotSatisfiable)

		return
	}
	end := total - 1
	if length >= 0 && offset+length-1 < end {
		end = offset + length - 1
	}
	h.Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", offset, end, total))
	h.Set("Content-Length", strconv.FormatInt(end-offset+1, 10))
	w.WriteHeader(http.StatusPartialContent)
	_, _ = io.Copy(w, body)
}

// parseRange reads "bytes=start-end" or "bytes=start-". ranged is false
// for no header at all.
func parseRange(header string) (offset, length int64, ranged, ok bool) {
	if header == "" {
		return 0, -1, false, true
	}
	spec, found := strings.CutPrefix(header, "bytes=")
	if !found || strings.Contains(spec, ",") {
		return 0, 0, true, false
	}
	start, end, _ := strings.Cut(spec, "-")
	if start == "" {
		return 0, 0, true, false // suffix ranges are not used by the clients
	}
	offset, err := strconv.ParseInt(start, 10, 64)
	if err != nil || offset < 0 {
		return 0, 0, true, false
	}
	if end == "" {
		return offset, -1, true, true
	}
	last, err := strconv.ParseInt(end, 10, 64)
	if err != nil || last < offset {
		return 0, 0, true, false
	}

	return offset, last - offset + 1, true, true
}

// v1 REST API, for older actions and buildx without url_v2.

type v1Entry struct {
	CacheKey        string `json:"cacheKey"`
	Scope           string `json:"scope"`
	CacheVersion    string `json:"cacheVersion"`
	ArchiveLocation string `json:"archiveLocation"`
}

func (s *Server) v1Lookup(w http.ResponseWriter, r *http.Request) {
	keys := strings.Split(r.URL.Query().Get("keys"), ",")
	version := r.URL.Query().Get("version")
	if len(keys) == 0 || keys[0] == "" || version == "" {
		http.Error(w, "keys and version are required", http.StatusBadRequest)

		return
	}
	matched, object, err := s.lookup(r.Context(), keys[0], keys[1:], version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	if object == "" {
		w.WriteHeader(http.StatusNoContent)

		return
	}
	writeJSON(w, http.StatusOK, v1Entry{CacheKey: matched, Scope: "refs/heads/main", CacheVersion: version, ArchiveLocation: s.downloadURLFor(object)})
}

func (s *Server) v1Reserve(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Key       string `json:"key"`
		Version   string `json:"version"`
		CacheSize int64  `json:"cacheSize"`
	}
	if !decodeJSON(w, r, &req) || !checkKey(w, req.Key, req.Version) {
		return
	}
	up, id, err := s.newUpload(req.Key, req.Version)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	up.v1Size = req.CacheSize
	s.mu.Lock()
	n := s.nextID
	s.mu.Unlock()
	writeJSON(w, http.StatusCreated, map[string]any{"cacheId": n, "uploadId": id})
}

// v1Upload writes one chunk at the offset its Content-Range names.
func (s *Server) v1Upload(w http.ResponseWriter, r *http.Request) {
	up := s.uploadByNumber(r.PathValue("id"))
	if up == nil {
		http.Error(w, "unknown cache id", http.StatusNotFound)

		return
	}
	var start, end int64
	if _, err := fmt.Sscanf(r.Header.Get("Content-Range"), "bytes %d-%d/", &start, &end); err != nil {
		http.Error(w, "Content-Range is required", http.StatusBadRequest)

		return
	}
	path := filepath.Join(up.dir, "single")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY, 0o600) //nolint:gosec // under the staging directory this server created
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(start, io.SeekStart); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	if _, err := io.Copy(f, r.Body); err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	s.mu.Lock()
	up.single = path
	s.mu.Unlock()
	w.WriteHeader(http.StatusNoContent)
}

func (s *Server) v1Commit(w http.ResponseWriter, r *http.Request) {
	up := s.uploadByNumber(r.PathValue("id"))
	if up == nil {
		http.Error(w, "unknown cache id", http.StatusNotFound)

		return
	}
	s.removeUpload(up)
	defer func() { _ = os.RemoveAll(up.dir) }()
	size, err := s.commit(r.Context(), up)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	s.log.Info("cache entry saved (v1)", slog.String("key", up.key), slog.Int64("bytes", size))
	w.WriteHeader(http.StatusNoContent)
}

// uploads

func (s *Server) newUpload(key, version string) (*upload, string, error) {
	dir, err := os.MkdirTemp(s.tmp, "cache-")
	if err != nil {
		return nil, "", fmt.Errorf("stage upload: %w", err)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.nextID++
	id := strconv.FormatInt(s.nextID, 10) + "-" + newID()
	// A client that retries from scratch replaces its earlier attempt.
	for oldID, old := range s.uploads {
		if (old.key == key && old.version == version) || time.Since(old.started) > uploadTTL {
			_ = os.RemoveAll(old.dir)
			delete(s.uploads, oldID)
		}
	}
	up := &upload{key: key, version: version, dir: dir, blocks: map[string]string{}, started: time.Now()}
	s.uploads[id] = up

	return up, id, nil
}

func (s *Server) takeUpload(key, version string) *upload {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, up := range s.uploads {
		if up.key == key && up.version == version {
			delete(s.uploads, id)

			return up
		}
	}

	return nil
}

func (s *Server) uploadByNumber(number string) *upload {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, up := range s.uploads {
		if strings.HasPrefix(id, number+"-") {
			return up
		}
	}

	return nil
}

func (s *Server) removeUpload(target *upload) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, up := range s.uploads {
		if up == target {
			delete(s.uploads, id)
		}
	}
}

// commit assembles an upload's blocks in order and stores the entry.
func (s *Server) commit(ctx context.Context, up *upload) (int64, error) {
	var files []string
	switch {
	case up.single != "":
		files = []string{up.single}
	case len(up.order) > 0:
		for _, id := range up.order {
			files = append(files, up.blocks[id])
		}
	default:
		return 0, errors.New("nothing was uploaded")
	}
	var size int64
	readers := make([]io.Reader, 0, len(files))
	var handles []*os.File
	defer func() {
		for _, f := range handles {
			_ = f.Close()
		}
	}()
	for _, path := range files {
		f, err := os.Open(path) //nolint:gosec // staged by this server
		if err != nil {
			return 0, fmt.Errorf("read staged block: %w", err)
		}
		info, err := f.Stat()
		if err != nil {
			return 0, fmt.Errorf("stat staged block: %w", err)
		}
		size += info.Size()
		handles = append(handles, f)
		readers = append(readers, f)
	}
	if err := s.store.Put(ctx, objectKey(s.prefix, up.version, up.key), size, io.MultiReader(readers...)); err != nil {
		return 0, err
	}

	return size, nil
}

// helpers

func writeFile(path string, body io.Reader) error {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600) //nolint:gosec // under the staging directory this server created
	if err != nil {
		return fmt.Errorf("stage block: %w", err)
	}
	defer func() { _ = f.Close() }()
	if _, err := io.Copy(f, body); err != nil {
		return fmt.Errorf("stage block: %w", err)
	}

	return nil
}

func decodeJSON(w http.ResponseWriter, r *http.Request, out any) bool {
	if err := json.NewDecoder(io.LimitReader(r.Body, maxJSONBody)).Decode(out); err != nil {
		twirpError(w, http.StatusBadRequest, "malformed", "invalid JSON: "+err.Error())

		return false
	}

	return true
}

func checkKey(w http.ResponseWriter, key, version string) bool {
	if key == "" || version == "" || len(key) > 512 || len(version) > 256 || strings.ContainsAny(key, ",\x00") {
		twirpError(w, http.StatusBadRequest, "invalid_argument", "key and version are required; keys are at most 512 characters without commas")

		return false
	}

	return true
}

func writeJSON(w http.ResponseWriter, status int, body any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(body)
}

// twirpError answers in twirp's error format.
func twirpError(w http.ResponseWriter, status int, code, message string) {
	writeJSON(w, status, map[string]string{"code": code, "msg": message})
}

func newID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])

	return hex.EncodeToString(b[:])
}
