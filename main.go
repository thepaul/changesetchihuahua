package main

import (
	"context"
	"flag"
	"log"

	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/gerrit/eventserver"
)

const (
	Version = "0.0.1"
)

var (
	webListenAddr   = flag.String("http-listen", ":8080", "Address to listen on for web UI and incoming Gerrit events")
	persistentDBSource = flag.String("persistent-db", "sqlite:./persistent.db", "Data source for persistent DB (supported types: sqlite, postgres)")
	teamFile = flag.String("team-file", "teams.dat", "Where to store information about registered teams")
)

func main() {
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer func() { panic(logger.Sync()) }()
	ctx := context.Background()

	governor, err := NewGovernor(ctx, logger, *teamFile)
	if err != nil {
		logger.Fatal("could not set up governor", zap.Error(err))
	}
	gerritEventSink := eventserver.NewGerritEventSink(logger.Named("gerrit-events"), governor)
	webServer, err := NewUIWebHandler(*webListenAddr, logger.Named("web-server"), governor, gerritEventSink)
	if err != nil {
		logger.Fatal("initializing web server", zap.Error(err))
	}

	if err := webServer.Serve(ctx); err != nil {
		logger.Fatal("exiting", zap.Error(err))
	}
}
