package cacheproxy

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"math/big"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"
)

// DefaultInterceptHosts are the hosts whose CacheService calls are served
// locally. The runner's results host carries other services too
// (artifacts, job results); those are forwarded.
var DefaultInterceptHosts = []string{"results-receiver.actions.githubusercontent.com"}

// CA signs the certificates the proxy presents for intercepted hosts. It
// is generated on the machine and trusted only there.
type CA struct {
	cert *x509.Certificate
	key  *ecdsa.PrivateKey

	mu     sync.Mutex
	leaves map[string]*tls.Certificate
}

// NewCA generates a CA valid for a day: longer than any job, shorter
// than anyone would want a MITM root to live.
func NewCA() (*CA, error) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate ca key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Airship cache proxy (this machine only)"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(certValidity),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
		MaxPathLenZero:        true,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		return nil, fmt.Errorf("create ca: %w", err)
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		return nil, fmt.Errorf("parse ca: %w", err)
	}

	return &CA{cert: cert, key: key, leaves: map[string]*tls.Certificate{}}, nil
}

// PEM is the CA certificate for trust stores.
func (c *CA) PEM() []byte {
	return pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: c.cert.Raw})
}

// Pool is a cert pool holding only this CA, for test clients.
func (c *CA) Pool() *x509.CertPool {
	pool := x509.NewCertPool()
	pool.AddCert(c.cert)

	return pool
}

// leaf returns a certificate for host, signing one the first time.
func (c *CA) leaf(host string) (*tls.Certificate, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if leaf, ok := c.leaves[host]; ok {
		return leaf, nil
	}
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return nil, fmt.Errorf("generate key: %w", err)
	}
	serial, err := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 128))
	if err != nil {
		return nil, fmt.Errorf("serial: %w", err)
	}
	template := &x509.Certificate{
		SerialNumber: serial,
		Subject:      pkix.Name{CommonName: host},
		DNSNames:     []string{host},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     c.cert.NotAfter,
		KeyUsage:     x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, c.cert, &key.PublicKey, c.key)
	if err != nil {
		return nil, fmt.Errorf("sign %s: %w", host, err)
	}
	leaf := &tls.Certificate{Certificate: [][]byte{der, c.cert.Raw}, PrivateKey: key}
	c.leaves[host] = leaf

	return leaf, nil
}

// Proxy is the HTTPS proxy the runner's HTTPS_PROXY points at.
type Proxy struct {
	ca *CA
	// cache answers CacheService calls.
	cache http.Handler
	// intercept lists hosts (lowercase, no port) to terminate TLS for.
	intercept map[string]bool
	// upstream dials the real hosts; it must not use the proxy itself.
	upstream *http.Transport
	log      *slog.Logger
	// dial is how tunnels reach their target; tests override it.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
}

// NewProxy returns a Proxy serving cache through cache for the hosts.
func NewProxy(ca *CA, cache http.Handler, hosts []string, logger *slog.Logger) *Proxy {
	p := &Proxy{
		ca: ca, cache: cache, intercept: map[string]bool{}, log: logger,
		upstream: &http.Transport{
			Proxy:               nil, // never through ourselves
			DialContext:         (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
			TLSHandshakeTimeout: 30 * time.Second,
			ForceAttemptHTTP2:   false,
			MaxIdleConnsPerHost: 16,
		},
		dial: (&net.Dialer{Timeout: 30 * time.Second}).DialContext,
	}
	for _, host := range hosts {
		p.intercept[strings.ToLower(host)] = true
	}

	return p
}

// ServeHTTP handles CONNECT tunnels and plain proxied requests.
func (p *Proxy) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method == http.MethodConnect {
		p.connect(w, r)

		return
	}
	// A plain-HTTP request through the proxy: forward it.
	if r.URL.Host == "" {
		http.Error(w, "this is a proxy", http.StatusBadRequest)

		return
	}
	p.forward(w, r, r.URL.Scheme, r.URL.Host)
}

// connect either tunnels bytes to the target or, for intercepted hosts,
// terminates TLS and serves the connection as HTTP.
func (p *Proxy) connect(w http.ResponseWriter, r *http.Request) {
	host, port, err := net.SplitHostPort(r.Host)
	if err != nil {
		host, port = r.Host, "443"
	}
	hijacker, ok := w.(http.Hijacker)
	if !ok {
		http.Error(w, "cannot hijack", http.StatusInternalServerError)

		return
	}
	if !p.intercept[strings.ToLower(host)] || port != "443" {
		p.tunnel(hijacker, w, r, net.JoinHostPort(host, port))

		return
	}
	leaf, err := p.ca.leaf(host)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)

		return
	}
	conn, _, err := hijacker.Hijack()
	if err != nil {
		return
	}
	if _, err := io.WriteString(conn, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = conn.Close()

		return
	}
	tlsConn := tls.Server(conn, &tls.Config{
		Certificates: []tls.Certificate{*leaf},
		NextProtos:   []string{"http/1.1"},
		MinVersion:   tls.VersionTLS12,
	})
	if err := tlsConn.HandshakeContext(r.Context()); err != nil {
		p.log.Debug("tls handshake with client failed", slog.String("host", host), slog.Any("error", err))
		_ = tlsConn.Close()

		return
	}
	go p.serveIntercepted(tlsConn, host)
}

// certValidity is how long the machine's certificate authority and the
// leaves it signs last. It only needs to outlive the longest runner.
const certValidity = 30 * 24 * time.Hour

// serveIntercepted reads HTTP/1.1 requests off a terminated connection.
// CacheService calls go to the local cache; the rest to the real host.
func (p *Proxy) serveIntercepted(conn net.Conn, host string) {
	defer conn.Close()
	reader := bufio.NewReader(conn)
	for {
		req, err := http.ReadRequest(reader)
		if err != nil {
			return
		}
		req.URL.Scheme, req.URL.Host = "https", host
		req.RequestURI = ""
		resp := &interceptedResponse{conn: conn, reader: reader, req: req, header: http.Header{}}
		if strings.HasPrefix(req.URL.Path, TwirpPrefix) {
			p.cache.ServeHTTP(resp, req)
		} else {
			p.forward(resp, req, "https", host)
		}
		if resp.hijacked {
			// A protocol upgrade (the runner streams live logs over a
			// websocket on this host): the forwarder owns the connection now.
			return
		}
		resp.finish()
		if req.Close || resp.header.Get("Connection") == "close" {
			return
		}
		// A handler that did not read the whole body leaves it on the
		// connection, where it would be parsed as the next request. Drain a
		// bounded amount, as net/http does, and close past that.
		if n, _ := io.CopyN(io.Discard, req.Body, maxBodyDrain); n == maxBodyDrain {
			return
		}
	}
}

// maxBodyDrain is how much of an unread request body is skipped before the
// connection is closed instead.
const maxBodyDrain = 256 << 10

// forward sends a request to its real destination and copies the answer.
func (p *Proxy) forward(w http.ResponseWriter, r *http.Request, scheme, host string) {
	target := &url.URL{Scheme: scheme, Host: host}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)
			pr.Out.Host = host
			pr.Out.Header.Del("Proxy-Connection")
			pr.Out.Header.Del("Proxy-Authorization")
		},
		Transport:     p.upstream,
		FlushInterval: -1,
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			p.log.Warn("forward failed", slog.String("host", host), slog.String("path", r.URL.Path), slog.Any("error", err))
			http.Error(w, "upstream unreachable: "+err.Error(), http.StatusBadGateway)
		},
	}
	rp.ServeHTTP(w, r)
}

// tunnel copies bytes both ways without looking at them.
func (p *Proxy) tunnel(hijacker http.Hijacker, w http.ResponseWriter, r *http.Request, addr string) {
	target, err := p.dial(r.Context(), "tcp", addr)
	if err != nil {
		http.Error(w, "cannot reach "+addr+": "+err.Error(), http.StatusBadGateway)

		return
	}
	client, buffered, err := hijacker.Hijack()
	if err != nil {
		_ = target.Close()

		return
	}
	if _, err := io.WriteString(client, "HTTP/1.1 200 Connection Established\r\n\r\n"); err != nil {
		_ = client.Close()
		_ = target.Close()

		return
	}
	go func() {
		defer func() { _ = client.Close() }()
		defer func() { _ = target.Close() }()
		var wg sync.WaitGroup
		wg.Go(func() {
			if buffered.Reader.Buffered() > 0 {
				_, _ = io.CopyN(target, buffered, int64(buffered.Reader.Buffered()))
			}
			_, _ = io.Copy(target, client)
			if tc, ok := target.(*net.TCPConn); ok {
				_ = tc.CloseWrite()
			}
		})
		wg.Go(func() {
			_, _ = io.Copy(client, target)
			if cc, ok := client.(*net.TCPConn); ok {
				_ = cc.CloseWrite()
			}
		})
		wg.Wait()
	}()
}

// interceptedResponse writes an HTTP/1.1 response onto a raw connection.
type interceptedResponse struct {
	conn     net.Conn
	reader   *bufio.Reader
	req      *http.Request
	header   http.Header
	status   int
	wrote    bool
	hijacked bool
	body     io.Writer
	closer   func()
}

func (r *interceptedResponse) Header() http.Header { return r.header }

func (r *interceptedResponse) WriteHeader(status int) {
	if r.wrote {
		return
	}
	r.wrote = true
	r.status = status
	resp := &http.Response{
		StatusCode: status, ProtoMajor: 1, ProtoMinor: 1, Header: r.header, Request: r.req, ContentLength: -1,
	}
	if cl := r.header.Get("Content-Length"); cl != "" {
		_, _ = fmt.Sscanf(cl, "%d", &resp.ContentLength)
	}
	// Without a length the body is chunked; the writer below handles it.
	if resp.ContentLength < 0 && status != http.StatusNoContent && status != http.StatusNotModified && r.req.Method != http.MethodHead {
		resp.TransferEncoding = []string{"chunked"}
	}
	var buf strings.Builder
	fmt.Fprintf(&buf, "HTTP/1.1 %d %s\r\n", status, http.StatusText(status))
	for key, values := range r.header {
		for _, value := range values {
			buf.WriteString(key + ": " + value + "\r\n")
		}
	}
	if len(resp.TransferEncoding) > 0 {
		buf.WriteString("Transfer-Encoding: chunked\r\n")
		buf.WriteString("\r\n")
		_, _ = io.WriteString(r.conn, buf.String())
		cw := httputil.NewChunkedWriter(r.conn)
		r.body = cw
		r.closer = func() {
			_ = cw.Close()
			_, _ = io.WriteString(r.conn, "\r\n")
		}

		return
	}
	buf.WriteString("\r\n")
	_, _ = io.WriteString(r.conn, buf.String())
	r.body = r.conn
}

func (r *interceptedResponse) Write(b []byte) (int, error) {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	if r.req.Method == http.MethodHead {
		return len(b), nil
	}

	return r.body.Write(b)
}

func (r *interceptedResponse) Flush() {}

func (r *interceptedResponse) finish() {
	if !r.wrote {
		r.WriteHeader(http.StatusOK)
	}
	if r.closer != nil {
		r.closer()
	}
}

// errAlreadyWritten is returned when an upgrade is attempted after the
// response started.
var errAlreadyWritten = errors.New("response already written")

// Hijack hands the terminated connection to the forwarder for a protocol
// upgrade. The runner streams live job logs to the results host over a
// websocket, which ReverseProxy relays by hijacking and copying bytes.
func (r *interceptedResponse) Hijack() (net.Conn, *bufio.ReadWriter, error) {
	if r.wrote {
		return nil, nil, errAlreadyWritten
	}
	r.hijacked = true

	return r.conn, bufio.NewReadWriter(r.reader, bufio.NewWriter(r.conn)), nil
}
