// Command cacheproxy runs on a runner machine and accelerates
// actions/cache: it serves GitHub's cache protocol on localhost, backed by
// an S3 bucket in the customer's account, and intercepts the runner's
// cache calls through an HTTPS proxy. See internal/cacheproxy.
//
// The boot script starts it before the runner with:
//
//	cacheproxy -control-plane https://api.example -token asr_... -ca-out /etc/airship/cacheproxy-ca.pem
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/wizenheimer/airship/apps/api/internal/cacheproxy"
)

func main() {
	if err := run(); err != nil {
		slog.Error("cacheproxy stopped", slog.Any("error", err))
		os.Exit(1)
	}
}

func run() error {
	var (
		controlPlane = flag.String("control-plane", os.Getenv("AIRSHIP_URL"), "control plane URL")
		token        = flag.String("token", os.Getenv("AIRSHIP_RUNNER_TOKEN"), "runner token")
		cacheAddr    = flag.String("cache-listen", "127.0.0.1:6420", "cache service address")
		proxyAddr    = flag.String("proxy-listen", "127.0.0.1:6421", "HTTPS proxy address")
		caOut        = flag.String("ca-out", "", "write the proxy CA certificate here (PEM)")
		tmp          = flag.String("tmp", os.TempDir(), "where uploads are staged")
		intercept    = flag.String("intercept", strings.Join(cacheproxy.DefaultInterceptHosts, ","), "hosts whose CacheService calls are served locally")
		s3Endpoint   = flag.String("s3-endpoint", os.Getenv("AIRSHIP_S3_ENDPOINT"), "S3 endpoint override (emulators)")
		devMemory    = flag.Bool("dev-memory", false, "keep entries in memory instead of S3; no control plane needed (testing only)")
	)
	flag.Parse()
	logger := slog.New(slog.NewJSONHandler(os.Stdout, nil))

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var store cacheproxy.Store
	var prefix string
	if *devMemory {
		store = cacheproxy.NewMemoryStore()
	} else {
		if *controlPlane == "" || *token == "" {
			return errors.New("-control-plane and -token are required")
		}
		grants := &cacheproxy.GrantClient{URL: strings.TrimRight(*controlPlane, "/"), Token: *token}
		grant, err := grants.Fetch(ctx)
		if err != nil {
			return err
		}
		store = cacheproxy.NewS3Store(cacheproxy.NewS3Client(grants, grant.Region, *s3Endpoint), grant.Bucket).WithOwner(grant.AccountID)
		prefix = grant.Prefix
		logger.Info("cache grant", slog.String("bucket", grant.Bucket), slog.String("region", grant.Region), slog.String("prefix", grant.Prefix))
	}
	cache := cacheproxy.NewServer(store, prefix, "http://"+*cacheAddr, *tmp, logger)

	ca, err := cacheproxy.NewCA()
	if err != nil {
		return err
	}
	if *caOut != "" {
		if err := os.WriteFile(*caOut, ca.PEM(), 0o644); err != nil { //nolint:gosec // a public certificate
			return fmt.Errorf("write ca: %w", err)
		}
	}
	proxy := cacheproxy.NewProxy(ca, cache.Handler(), strings.Split(*intercept, ","), logger)

	servers := []*http.Server{
		{Addr: *cacheAddr, Handler: cache.Handler(), ReadHeaderTimeout: 30 * time.Second},
		{Addr: *proxyAddr, Handler: proxy, ReadHeaderTimeout: 30 * time.Second},
	}
	errs := make(chan error, len(servers))
	for _, srv := range servers {
		listener, err := (&net.ListenConfig{}).Listen(ctx, "tcp", srv.Addr)
		if err != nil {
			return fmt.Errorf("listen %s: %w", srv.Addr, err)
		}
		go func() { errs <- srv.Serve(listener) }()
	}
	logger.Info("cacheproxy ready", slog.String("cache", *cacheAddr), slog.String("proxy", *proxyAddr))

	select {
	case err := <-errs:
		return err
	case <-ctx.Done():
	}
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for _, srv := range servers {
		_ = srv.Shutdown(shutdownCtx)
	}

	return nil
}
