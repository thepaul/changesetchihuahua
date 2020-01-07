package main

import (
	"context"
	"flag"
	"log"
	"net"
	"net/url"

	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"
)

const (
	Version = "0.0.1"
)

var (
	httpListenAddr     = flag.String("http-listen", ":80", "Address to listen on for HTTP requests to web UI and incoming Gerrit events. If empty, don't listen for HTTP.")
	httpsListenAddr    = flag.String("https-listen", ":443", "Address to listen on for HTTPS requests to web UI and incoming Gerrit events. If empty, don't listen for HTTPS.")
	persistentDBSource = flag.String("persistent-db", "sqlite:./persistent.db", "Data source for persistent DB (supported types: sqlite, postgres)")
	teamFile           = flag.String("team-file", "teams.dat", "Where to store information about registered teams")
	externalURL        = flag.String("external-url", "https://localhost.localdomain/", "The URL by which external hosts (including Slack servers) can contact this server")
)

func main() {
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer func() { panic(logger.Sync()) }()
	errg, ctx := errgroup.WithContext(context.Background())

	governor, err := NewGovernor(ctx, logger, *teamFile)
	if err != nil {
		logger.Fatal("could not set up governor", zap.Error(err))
	}

	parsedURL, err := url.Parse(*externalURL)
	if err != nil {
		logger.Fatal("parsing external-url", zap.String("external-url", *externalURL), zap.Error(err))
	}
	webState := newUIWebState(logger.Named("web-state"), governor, parsedURL)
	webHandler := newUIWebHandler(logger.Named("web-handler"), webState)

	if *httpListenAddr != "" {
		httpServer := newUIWebServer(webState, webHandler)
		httpListener, err := net.Listen("tcp", *httpListenAddr)
		if err != nil {
			logger.Fatal("listening for http on %q: %w", zap.String("listen-addr", *httpListenAddr), zap.Error(err))
		}
		errg.Go(func() error {
			return httpServer.Serve(ctx, httpListener)
		})
	}

	if *httpsListenAddr != "" {
		manager := NewTLSAutoCertManager(func(ctx context.Context, hostName string) error {
			if hostName != parsedURL.Host {
				return errs.New("invalid hostname %q", hostName)
			}
			return nil
		})
		httpsServer := newUIWebServer(webState, webHandler)
		httpsListener := manager.Listener()
		errg.Go(func() error {
			return httpsServer.Serve(ctx, httpsListener)
		})
	}

	if err := errg.Wait(); err != nil {
		logger.Fatal("exiting", zap.Error(err))
	}
}
