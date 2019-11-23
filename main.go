package main

import (
	"context"
	"flag"
	"log"

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/jtolds/changesetchihuahua/app"
	"github.com/jtolds/changesetchihuahua/gerrit"
	"github.com/jtolds/changesetchihuahua/slack"
)

var (
	gerritListenAddr = flag.String("gerrit-listen-addr", ":29746", "Address to listen on for incoming Gerrit events")
	directoryDB = flag.String("directory-db", "sqlite:./userdirectory.db", "Data source for user directory")
)

func main() {
	flag.Parse()

	logger, err := zap.NewDevelopment()
	if err != nil {
		log.Fatalf("Can't initialize zap logger: %v", err)
	}
	defer func() { panic(logger.Sync()) }()

	chatInterface, err := slack.NewSlackInterface(logger.Named("chat"))
	if err != nil {
		logger.Fatal("initializing Slack connection", zap.Error(err))
	}
	directory, err := app.NewUserDirectory(logger.Named("directory"), *directoryDB)
	if err != nil {
		logger.Fatal("initializing user directory DB", zap.Error(err))
	}
	chihuahua := app.New(logger, chatInterface, directory)

	group, ctx := errgroup.WithContext(context.Background())

	group.Go(func() error {
		return chatInterface.HandleEvents(ctx, chihuahua)
	})
	group.Go(func() error {
		srv, err := gerrit.NewGerritEventSink(logger.Named("gerrit"), *gerritListenAddr, chihuahua)
		if err != nil {
			return err
		}
		go func() {
			<-ctx.Done()
			_ = srv.Close()
		}()
		return srv.ListenAndServe()
	})

	if err := group.Wait(); err != nil {
		logger.Fatal("exiting", zap.Error(err))
	}
}
