package cacheproxy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

// Concurrent v1 reserves each get their own cache id; sharing one would
// route one upload's chunks into another's entry.
func TestV1ConcurrentReservesAreDistinct(t *testing.T) {
	t.Parallel()
	ts, _ := newTestServer(t)
	const reserves = 64
	var mu sync.Mutex
	seen := map[int64]int{}
	var wg sync.WaitGroup
	for i := range reserves {
		wg.Go(func() {
			body, _ := json.Marshal(map[string]any{"key": "k" + string(rune('a'+i%26)) + string(rune('a'+i/26)), "version": "v", "cacheSize": 1})
			req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, ts.URL+v1Prefix+"caches", bytes.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Error(err)

				return
			}
			defer resp.Body.Close()
			var reserved struct {
				CacheID int64 `json:"cacheId"`
			}
			_ = json.NewDecoder(resp.Body).Decode(&reserved)
			mu.Lock()
			seen[reserved.CacheID]++
			mu.Unlock()
		})
	}
	wg.Wait()
	for id, n := range seen {
		if n != 1 || id == 0 {
			t.Fatalf("cache id %d handed out %d times", id, n)
		}
	}
	if len(seen) != reserves {
		t.Fatalf("%d distinct ids for %d reserves", len(seen), reserves)
	}
}

// Staged block files read as one object at any offset, across the file
// boundaries, which is what lets the store send parts straight from disk.
func TestStagedFilesReadAt(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	var whole []byte
	var paths []string
	for i, size := range []int{10, 0, 7, 100} {
		chunk := randomBytes(size)
		path := filepath.Join(dir, "block"+string(rune('0'+i)))
		if err := os.WriteFile(path, chunk, 0o600); err != nil {
			t.Fatal(err)
		}
		whole = append(whole, chunk...)
		paths = append(paths, path)
	}
	staged, err := openStaged(paths)
	if err != nil {
		t.Fatal(err)
	}
	defer staged.Close()
	if staged.size != int64(len(whole)) {
		t.Fatalf("size = %d, want %d", staged.size, len(whole))
	}
	for _, span := range [][2]int64{{0, 117}, {5, 10}, {9, 3}, {10, 7}, {16, 2}, {100, 17}, {117, 0}} {
		got, err := io.ReadAll(io.NewSectionReader(staged, span[0], span[1]))
		if err != nil || !bytes.Equal(got, whole[span[0]:span[0]+span[1]]) {
			t.Fatalf("read %v = %d bytes, %v", span, len(got), err)
		}
	}
	buf := make([]byte, 50)
	if n, err := staged.ReadAt(buf, 100); n != 17 || !errors.Is(err, io.EOF) {
		t.Fatalf("read past the end = %d, %v", n, err)
	}
}

// A handler that does not read its whole request body must not leave the
// rest on the connection, where it would be parsed as the next request.
func TestInterceptedConnectionSurvivesUnreadBody(t *testing.T) {
	t.Parallel()
	cache := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Read only a little, as a handler that rejects a malformed body does.
		_, _ = io.ReadFull(r.Body, make([]byte, 10))
		w.WriteHeader(http.StatusBadRequest)
	})
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	const host = "results-receiver.actions.githubusercontent.com"
	proxy := NewProxy(ca, cache, []string{host}, slog.New(slog.DiscardHandler))
	ps := httptest.NewServer(proxy)
	t.Cleanup(ps.Close)
	client := proxiedClient(t, ps.URL, ca)

	for i := range 3 {
		req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://"+host+TwirpPrefix+"CreateCacheEntry", bytes.NewReader(randomBytes(64<<10)))
		req.Header.Set("Content-Type", "application/json")
		resp, err := client.Do(req)
		if err != nil {
			t.Fatalf("request %d: %v", i, err)
		}
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
		if resp.StatusCode != http.StatusBadRequest {
			t.Fatalf("request %d = %d", i, resp.StatusCode)
		}
	}
}
