package main

import (
	"context"
	"flag"
	"log"
	"time"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/jtolds/changesetchihuahua/app"
	"github.com/jtolds/changesetchihuahua/gerrit"
	"github.com/jtolds/changesetchihuahua/gerrit/eventserver"
	"github.com/jtolds/changesetchihuahua/slack"
)

var (
	gerritListenAddr   = flag.String("gerrit-listen", ":29746", "Address to listen on for incoming Gerrit events")
	gerritServerAddr   = flag.String("gerrit-server", "https://gerrit-review.googlesource.com/", "Address of the Gerrit server to query about changes")
	persistentDBSource = flag.String("persistent-db", "sqlite:./persistent.db", "Data source for persistent DB")
)

func main() {
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer func() { panic(logger.Sync()) }()
	ctx := context.Background()

	chatInterface, err := slack.NewSlackInterface(logger.Named("chat"))
	if err != nil {
		logger.Fatal("initializing Slack connection", zap.Error(err))
	}
	persistentDB, err := app.NewPersistentDB(logger.Named("db"), *persistentDBSource)
	if err != nil {
		logger.Fatal("initializing persistent DB", zap.Error(err))
	}
	gerritClient, err := gerrit.OpenClient(ctx, *gerritServerAddr)
	if err != nil {
		logger.Fatal("initializing gerrit client", zap.Error(err))
	}
	chihuahua := app.New(logger, chatInterface, persistentDB, gerritClient)

	group, ctx := errgroup.WithContext(ctx)

	group.Go(func() error {
		return chatInterface.HandleEvents(ctx, chihuahua)
	})
	group.Go(func() error {
		srv, err := eventserver.NewGerritEventSink(logger.Named("gerrit"), *gerritListenAddr, chihuahua)
		if err != nil {
			return err
		}
		go func() {
			<-ctx.Done()
			_ = srv.Close()
		}()
		return srv.ListenAndServe()
	})
	group.Go(func() error {
		return chihuahua.PeriodicReportWaitingChangeSets(ctx, time.Now)
	})

	if err := group.Wait(); err != nil {
		logger.Fatal("exiting", zap.Error(err))
	}
}
