package cacheproxy

import (
	"bufio"
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

// The runner streams live logs to the results host over a websocket; an
// intercepted connection must relay the upgrade and the frames both ways.
func TestProxyRelaysUpgradesOnInterceptedHost(t *testing.T) {
	t.Parallel()
	upstream := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Upgrade") != "websocket" {
			http.Error(w, "expected upgrade", http.StatusBadRequest)

			return
		}
		conn, rw, err := w.(http.Hijacker).Hijack()
		if err != nil {
			return
		}
		defer conn.Close()
		_, _ = rw.WriteString("HTTP/1.1 101 Switching Protocols\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
		_ = rw.Flush()
		line, _ := rw.ReadString('\n')
		_, _ = rw.WriteString("echo:" + line)
		_ = rw.Flush()
	}))
	t.Cleanup(upstream.Close)
	ca, _ := NewCA()
	const host = "results-receiver.actions.githubusercontent.com"
	proxy := NewProxy(ca, http.NotFoundHandler(), []string{host}, slog.New(slog.DiscardHandler))
	dial := func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, upstream.Listener.Addr().String())
	}
	proxy.dial = dial
	proxy.upstream.DialContext = dial
	proxy.upstream.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // self-signed test upstream
	ps := httptest.NewServer(proxy)
	t.Cleanup(ps.Close)

	// Speak the proxy protocol by hand: CONNECT, then TLS, then the upgrade.
	raw, err := (&net.Dialer{}).DialContext(context.Background(), "tcp", strings.TrimPrefix(ps.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	defer raw.Close()
	_, _ = io.WriteString(raw, "CONNECT "+host+":443 HTTP/1.1\r\nHost: "+host+":443\r\n\r\n")
	br := bufio.NewReader(raw)
	if resp, err := http.ReadResponse(br, nil); err != nil || resp.StatusCode != http.StatusOK {
		t.Fatalf("connect = %v %v", resp, err)
	}
	tlsConn := tls.Client(raw, &tls.Config{ServerName: host, RootCAs: ca.Pool(), MinVersion: tls.VersionTLS12})
	if err := tlsConn.HandshakeContext(context.Background()); err != nil {
		t.Fatal(err)
	}
	_, _ = io.WriteString(tlsConn, "GET /_ws/ingest.sock HTTP/1.1\r\nHost: "+host+"\r\nUpgrade: websocket\r\nConnection: Upgrade\r\n\r\n")
	tr := bufio.NewReader(tlsConn)
	resp, err := http.ReadResponse(tr, nil)
	if err != nil || resp.StatusCode != http.StatusSwitchingProtocols {
		t.Fatalf("upgrade = %v %v", resp, err)
	}
	_, _ = io.WriteString(tlsConn, "hello\n")
	line, err := tr.ReadString('\n')
	if err != nil || line != "echo:hello\n" {
		t.Fatalf("frame relay = %q, %v", line, err)
	}
}
