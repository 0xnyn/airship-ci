package cacheproxy

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"
)

func newTestServer(t *testing.T) (*httptest.Server, *MemoryStore) {
	t.Helper()
	store := NewMemoryStore()
	var srv *Server
	ts := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { srv.Handler().ServeHTTP(w, r) }))
	ts.Start()
	t.Cleanup(ts.Close)
	srv = NewServer(store, "acme/api", ts.URL, t.TempDir(), slog.New(slog.DiscardHandler))

	return ts, store
}

func twirp(t *testing.T, base, method string, in any, out any) int {
	t.Helper()
	body, _ := json.Marshal(in)
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, base+TwirpPrefix+method, bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", "Bearer runtime-token")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if out != nil {
		if err := json.Unmarshal(raw, out); err != nil {
			t.Fatalf("%s: %s: %v", method, raw, err)
		}
	}

	return resp.StatusCode
}

func do(t *testing.T, req *http.Request) *http.Response {
	t.Helper()
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { resp.Body.Close() })

	return resp
}

func randomBytes(n int) []byte {
	out := make([]byte, n)
	_, _ = rand.Read(out)

	return out
}

// The flow actions/cache@v4 follows: reserve, upload as one Put Blob (small
// archives) or blocks plus a block list (large ones), finalize, then
// restore with a HEAD and ranged GETs.
func TestTwirpSaveAndRestore(t *testing.T) {
	t.Parallel()
	ts, store := newTestServer(t)
	archive := randomBytes(3*1024*1024 + 17)

	var created createEntryResponse
	if status := twirp(t, ts.URL, "CreateCacheEntry", createEntryRequest{Key: "linux-go-abc", Version: "v1"}, &created); status != http.StatusOK || !created.OK || created.SignedUploadURL == "" {
		t.Fatalf("create = %d %+v", status, created)
	}

	// Large archive: three blocks staged out of order, then committed.
	blocks := [][]byte{archive[:1<<20], archive[1<<20 : 2<<20], archive[2<<20:]}
	ids := []string{}
	for i := range blocks {
		ids = append(ids, base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "block-%05d", i)))
	}
	for _, i := range []int{2, 0, 1} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, created.SignedUploadURL+"?comp=block&blockid="+base64.StdEncoding.EncodeToString(fmt.Appendf(nil, "block-%05d", i)), bytes.NewReader(blocks[i]))
		req.Header.Set("x-ms-blob-type", "BlockBlob")
		if resp := do(t, req); resp.StatusCode != http.StatusCreated || resp.Header.Get("x-ms-version") == "" {
			t.Fatalf("put block %d = %d", i, resp.StatusCode)
		}
	}
	var listXML strings.Builder
	listXML.WriteString("<?xml version=\"1.0\" encoding=\"utf-8\"?><BlockList>")
	for _, id := range ids {
		listXML.WriteString("<Latest>" + id + "</Latest>")
	}
	listXML.WriteString("</BlockList>")
	list := listXML.String()
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, created.SignedUploadURL+"?comp=blocklist", strings.NewReader(list))
	if resp := do(t, req); resp.StatusCode != http.StatusCreated {
		t.Fatalf("put block list = %d", resp.StatusCode)
	}
	var finalized finalizeResponse
	raw := fmt.Sprintf(`{"key":"linux-go-abc","version":"v1","size_bytes":"%d"}`, len(archive))
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+TwirpPrefix+"FinalizeCacheEntryUpload", strings.NewReader(raw))
	req.Header.Set("Content-Type", "application/json")
	resp := do(t, req)
	_ = json.NewDecoder(resp.Body).Decode(&finalized)
	if resp.StatusCode != http.StatusOK || !finalized.OK {
		t.Fatalf("finalize = %d %+v", resp.StatusCode, finalized)
	}
	if keys := store.Keys(); len(keys) != 1 || keys[0] != "acme/api/v1/linux-go-abc" {
		t.Fatalf("stored keys = %v", keys)
	}

	// Restore: exact key.
	var found downloadURLResponse
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "linux-go-abc", Version: "v1"}, &found)
	if !found.OK || found.MatchedKey != "linux-go-abc" || found.SignedDownloadURL == "" {
		t.Fatalf("download url = %+v", found)
	}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodHead, found.SignedDownloadURL, nil)
	head := do(t, req)
	if head.StatusCode != http.StatusOK || head.Header.Get("Content-Length") != strconv.Itoa(len(archive)) {
		t.Fatalf("head = %d %s", head.StatusCode, head.Header.Get("Content-Length"))
	}
	// Ranged GETs as downloadCacheHttpClientConcurrent makes them.
	got := make([]byte, len(archive))
	const segment = 4 * 1024 * 1024
	for offset := 0; offset < len(archive); offset += segment {
		end := min(offset+segment, len(archive)) - 1
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, found.SignedDownloadURL, nil)
		req.Header.Set("Range", fmt.Sprintf("bytes=%d-%d", offset, end))
		resp := do(t, req)
		if resp.StatusCode != http.StatusPartialContent || resp.Header.Get("Content-Range") != fmt.Sprintf("bytes %d-%d/%d", offset, end, len(archive)) {
			t.Fatalf("range %d-%d = %d %s", offset, end, resp.StatusCode, resp.Header.Get("Content-Range"))
		}
		n, _ := io.ReadFull(resp.Body, got[offset:end+1])
		if n != end-offset+1 {
			t.Fatalf("range %d-%d read %d bytes", offset, end, n)
		}
	}
	if !bytes.Equal(got, archive) {
		t.Fatal("restored archive differs from the saved one")
	}
	// buildx reads from an offset to the end.
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, found.SignedDownloadURL, nil)
	req.Header.Set("Range", "bytes=3145728-")
	tail := do(t, req)
	rest, _ := io.ReadAll(tail.Body)
	if tail.StatusCode != http.StatusPartialContent || !bytes.Equal(rest, archive[3145728:]) {
		t.Fatalf("open range = %d, %d bytes", tail.StatusCode, len(rest))
	}
	// A plain GET streams everything.
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, found.SignedDownloadURL, nil)
	whole := do(t, req)
	all, _ := io.ReadAll(whole.Body)
	if whole.StatusCode != http.StatusOK || !bytes.Equal(all, archive) {
		t.Fatalf("full get = %d, %d bytes", whole.StatusCode, len(all))
	}

	// Miss: no entry, no restore key.
	var miss downloadURLResponse
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "linux-go-zzz", Version: "v1"}, &miss)
	if miss.OK {
		t.Fatalf("miss = %+v", miss)
	}
	// Another version never matches.
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "linux-go-abc", Version: "v2"}, &miss)
	if miss.OK {
		t.Fatalf("other version matched: %+v", miss)
	}
}

// Small archives arrive as one Put Blob. Restore keys match by prefix,
// newest first, within the version.
func TestPutBlobAndRestoreKeys(t *testing.T) {
	t.Parallel()
	ts, store := newTestServer(t)
	save := func(key string, data []byte) {
		t.Helper()
		var created createEntryResponse
		twirp(t, ts.URL, "CreateCacheEntry", createEntryRequest{Key: key, Version: "v1"}, &created)
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, created.SignedUploadURL, bytes.NewReader(data))
		req.Header.Set("x-ms-blob-type", "BlockBlob")
		if resp := do(t, req); resp.StatusCode != http.StatusCreated {
			t.Fatalf("put blob = %d", resp.StatusCode)
		}
		var finalized finalizeResponse
		if status := twirp(t, ts.URL, "FinalizeCacheEntryUpload", map[string]any{"key": key, "version": "v1", "size_bytes": len(data)}, &finalized); status != http.StatusOK || !finalized.OK {
			t.Fatalf("finalize %s = %d %+v", key, status, finalized)
		}
	}
	save("linux-go-old", []byte("old"))
	store.Touch("acme/api/v1/linux-go-old", time.Now().Add(-time.Hour))
	save("linux-go-new", []byte("new"))
	save("linux-node-1", []byte("node"))

	var found downloadURLResponse
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "linux-go-missing", RestoreKeys: []string{"linux-go-", "linux-"}, Version: "v1"}, &found)
	if !found.OK || found.MatchedKey != "linux-go-new" {
		t.Fatalf("restore key match = %+v, want the newest linux-go- entry", found)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, found.SignedDownloadURL, nil)
	resp := do(t, req)
	if body, _ := io.ReadAll(resp.Body); string(body) != "new" {
		t.Fatalf("body = %q", body)
	}
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "x", RestoreKeys: []string{"linux-node"}, Version: "v1"}, &found)
	if !found.OK || found.MatchedKey != "linux-node-1" {
		t.Fatalf("second restore key = %+v", found)
	}

	// Keys with awkward characters round-trip.
	save("Linux-x64/go:1.23 ${hash}", []byte("odd"))
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "Linux-x64/go:1.23 ${hash}", Version: "v1"}, &found)
	if !found.OK || found.MatchedKey != "Linux-x64/go:1.23 ${hash}" {
		t.Fatalf("odd key = %+v", found)
	}
	twirp(t, ts.URL, "GetCacheEntryDownloadURL", downloadURLRequest{Key: "nope", RestoreKeys: []string{"Linux-x64/go:"}, Version: "v1"}, &found)
	if !found.OK || found.MatchedKey != "Linux-x64/go:1.23 ${hash}" {
		t.Fatalf("odd restore key = %+v", found)
	}
}

func TestTwirpValidation(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)
	if status := twirp(t, ts.URL, "CreateCacheEntry", createEntryRequest{Key: "", Version: "v1"}, nil); status != http.StatusBadRequest {
		t.Fatalf("empty key = %d", status)
	}
	if status := twirp(t, ts.URL, "CreateCacheEntry", createEntryRequest{Key: "a,b", Version: "v1"}, nil); status != http.StatusBadRequest {
		t.Fatalf("comma key = %d", status)
	}
	if status := twirp(t, ts.URL, "FinalizeCacheEntryUpload", finalizeRequest{Key: "never-reserved", Version: "v1"}, nil); status != http.StatusNotFound {
		t.Fatalf("finalize without reserve = %d", status)
	}
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPut, ts.URL+blobPath+"nope", strings.NewReader("x"))
	if resp := do(t, req); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("put to unknown upload = %d", resp.StatusCode)
	}
	// Download tokens for other prefixes are refused.
	other := base64.RawURLEncoding.EncodeToString([]byte("acme/other/v1/secret"))
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+downloadPath+other, nil)
	if resp := do(t, req); resp.StatusCode != http.StatusNotFound {
		t.Fatalf("other prefix download = %d", resp.StatusCode)
	}
}

// The legacy v1 API: reserve, chunked PATCH uploads, commit, lookup.
func TestV1(t *testing.T) {
	t.Parallel()
	ts, store := newTestServer(t)
	archive := randomBytes(10_000)

	var reserved struct {
		CacheID int64 `json:"cacheId"`
	}
	body, _ := json.Marshal(map[string]any{"key": "v1-key", "version": "hash", "cacheSize": len(archive)})
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+v1Prefix+"caches", bytes.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	resp := do(t, req)
	_ = json.NewDecoder(resp.Body).Decode(&reserved)
	if resp.StatusCode != http.StatusCreated || reserved.CacheID == 0 {
		t.Fatalf("reserve = %d %+v", resp.StatusCode, reserved)
	}
	id := strconv.FormatInt(reserved.CacheID, 10)
	for _, chunk := range [][2]int{{4000, 10000}, {0, 4000}} {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPatch, ts.URL+v1Prefix+"caches/"+id, bytes.NewReader(archive[chunk[0]:chunk[1]]))
		req.Header.Set("Content-Range", fmt.Sprintf("bytes %d-%d/*", chunk[0], chunk[1]-1))
		if resp := do(t, req); resp.StatusCode != http.StatusNoContent {
			t.Fatalf("patch = %d", resp.StatusCode)
		}
	}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+v1Prefix+"caches/"+id, strings.NewReader(`{"size":10000}`))
	req.Header.Set("Content-Type", "application/json")
	if resp := do(t, req); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("commit = %d", resp.StatusCode)
	}
	if keys := store.Keys(); len(keys) != 1 || keys[0] != "acme/api/hash/v1-key" {
		t.Fatalf("stored = %v", keys)
	}

	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+v1Prefix+"cache?keys=other,v1-&version=hash", nil)
	resp = do(t, req)
	var entry v1Entry
	_ = json.NewDecoder(resp.Body).Decode(&entry)
	if resp.StatusCode != http.StatusOK || entry.CacheKey != "v1-key" || entry.ArchiveLocation == "" {
		t.Fatalf("lookup = %d %+v", resp.StatusCode, entry)
	}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, entry.ArchiveLocation, nil)
	resp = do(t, req)
	if got, _ := io.ReadAll(resp.Body); !bytes.Equal(got, archive) {
		t.Fatal("v1 archive differs")
	}
	req, _ = http.NewRequestWithContext(context.Background(), http.MethodGet, ts.URL+v1Prefix+"cache?keys=nothing&version=hash", nil)
	if resp := do(t, req); resp.StatusCode != http.StatusNoContent {
		t.Fatalf("miss = %d", resp.StatusCode)
	}
}

func TestParseRange(t *testing.T) {
	t.Parallel()
	cases := map[string][4]any{
		"":                 {int64(0), int64(-1), false, true},
		"bytes=0-3":        {int64(0), int64(4), true, true},
		"bytes=100-":       {int64(100), int64(-1), true, true},
		"bytes=5-3":        {int64(0), int64(0), true, false},
		"bytes=-500":       {int64(0), int64(0), true, false},
		"bytes=0-1,5-6":    {int64(0), int64(0), true, false},
		"items=0-1":        {int64(0), int64(0), true, false},
		"bytes=4194304-99": {int64(0), int64(0), true, false},
	}
	for header, want := range cases {
		offset, length, ranged, ok := parseRange(header)
		if offset != want[0] || length != want[1] || ranged != want[2] || ok != want[3] {
			t.Errorf("%q = %d %d %v %v, want %v", header, offset, length, ranged, ok, want)
		}
	}
}
