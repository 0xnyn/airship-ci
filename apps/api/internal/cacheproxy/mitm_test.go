package cacheproxy

import (
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

// A client with HTTPS_PROXY pointing at the proxy and the proxy's CA
// trusted, as the runner and actions have.
func proxiedClient(t *testing.T, proxyURL string, ca *CA) *http.Client {
	t.Helper()
	parsed, _ := url.Parse(proxyURL)

	return &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(parsed),
		TLSClientConfig: &tls.Config{RootCAs: ca.Pool(), MinVersion: tls.VersionTLS12},
	}}
}

func TestProxyInterceptsCacheServiceAndForwardsTheRest(t *testing.T) {
	t.Parallel()
	// The "real" results host: a TLS server whose certificate the client
	// would not trust through the proxy anyway. The proxy reaches it with
	// a dialer that resolves the intercepted name to this server.
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Upstream", "yes")
		_, _ = io.WriteString(w, "upstream "+r.Method+" "+r.URL.Path+" host="+r.Host+" auth="+r.Header.Get("Authorization"))
	}))
	t.Cleanup(upstream.Close)
	upstreamAddr := upstream.Listener.Addr().String()

	cacheHits := 0
	cache := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		cacheHits++
		body, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"ok":true,"echo":"`+string(body)+`","host":"`+r.Host+`"}`)
	})
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	const host = "results-receiver.actions.githubusercontent.com"
	proxy := NewProxy(ca, cache, []string{host}, slog.New(slog.DiscardHandler))
	// Route the intercepted host (and the tunnel test's host) to the local upstream.
	dial := func(ctx context.Context, network, addr string) (net.Conn, error) {
		if strings.HasPrefix(addr, host) || strings.HasPrefix(addr, "other.example.com") {
			addr = upstreamAddr
		}

		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	proxy.dial = dial
	proxy.upstream.DialContext = dial
	proxy.upstream.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // the test upstream is self-signed
	ps := httptest.NewServer(proxy)
	t.Cleanup(ps.Close)
	client := proxiedClient(t, ps.URL, ca)

	// A CacheService call is answered locally, over a connection the client
	// believes is GitHub's.
	req0, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://"+host+TwirpPrefix+"GetCacheEntryDownloadURL", strings.NewReader(`{"key":"k"}`))
	req0.Header.Set("Content-Type", "application/json")
	resp, err := client.Do(req0)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK || !strings.Contains(string(body), `"echo":"{"key":"k"}"`) || cacheHits != 1 {
		t.Fatalf("intercepted call = %d %s (hits %d)", resp.StatusCode, body, cacheHits)
	}
	if resp.TLS == nil || resp.TLS.PeerCertificates[0].Subject.CommonName != host {
		t.Fatal("client did not get a certificate for the intercepted host")
	}

	// Anything else on that host goes to the real server, headers intact.
	req, _ := http.NewRequestWithContext(context.Background(), http.MethodPost, "https://"+host+"/twirp/github.actions.results.api.v1.ArtifactService/CreateArtifact", strings.NewReader("{}"))
	req.Header.Set("Authorization", "Bearer secret")
	resp, err = client.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if resp.Header.Get("X-Upstream") != "yes" || !strings.Contains(string(body), "ArtifactService/CreateArtifact host="+host+" auth=Bearer secret") {
		t.Fatalf("forwarded call = %d %s", resp.StatusCode, body)
	}
	if cacheHits != 1 {
		t.Fatal("forwarded call hit the cache")
	}

	// A second request on the same connection still works (keep-alive).
	req2, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://"+host+"/something/else", nil)
	resp, err = client.Do(req2)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "upstream GET /something/else") {
		t.Fatalf("second forwarded call = %s", body)
	}

	// Other hosts are tunneled untouched: the client sees the upstream's own
	// (untrusted) certificate, not one from our CA.
	req3, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://other.example.com/", nil)
	_, err = client.Do(req3) //nolint:bodyclose // fails before a body exists
	if err == nil || !strings.Contains(err.Error(), "certificate") {
		t.Fatalf("tunnel to another host = %v, want the real certificate to be rejected", err)
	}
	insecure := &http.Client{Transport: &http.Transport{
		Proxy:           http.ProxyURL(mustURL(ps.URL)),
		TLSClientConfig: &tls.Config{InsecureSkipVerify: true}, //nolint:gosec // proving the tunnel is transparent
	}}
	req4, _ := http.NewRequestWithContext(context.Background(), http.MethodGet, "https://other.example.com/tunnel", nil)
	resp, err = insecure.Do(req4)
	if err != nil {
		t.Fatal(err)
	}
	body, _ = io.ReadAll(resp.Body)
	resp.Body.Close()
	if !strings.Contains(string(body), "upstream GET /tunnel") {
		t.Fatalf("tunneled call = %s", body)
	}
}

func mustURL(s string) *url.URL {
	u, _ := url.Parse(s)

	return u
}

func TestCALeafIsCachedAndChained(t *testing.T) {
	t.Parallel()
	ca, err := NewCA()
	if err != nil {
		t.Fatal(err)
	}
	a, _ := ca.leaf("example.com")
	b, _ := ca.leaf("example.com")
	if a != b {
		t.Fatal("leaf not cached")
	}
	if len(a.Certificate) != 2 {
		t.Fatal("leaf does not carry the CA in its chain")
	}
	if !strings.Contains(string(ca.PEM()), "BEGIN CERTIFICATE") {
		t.Fatal("PEM export broken")
	}
}
