package app

import (
	"context"
	"flag"
	"fmt"
	"time"

	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

const (
	Version = "0.0.1"
)

var (
	admin = flag.String("admin", "", "Identity of an admin by email address. Will be contacted through the chat system, not email")

	notificationTimeout = flag.Duration("notify-timeout", time.Minute*30, "Maximum amount of time to spend trying to deliver a notification")
)

type MessageHandle interface{}

type ChatSystem interface {
	SupportsMessageEditing() bool
	SupportsMessageDeletion() bool

	SendToUserByEmail(ctx context.Context, email, message string) (MessageHandle, error)
	SendToUserByID(ctx context.Context, id, message string) (MessageHandle, error)

	LookupUserByEmail(ctx context.Context, username string) (string, error)
}

type App struct {
	logger    *zap.Logger
	chat      ChatSystem
	directory *UserDirectory
}

func New(logger *zap.Logger, chat ChatSystem, directory *UserDirectory) *App {
	app := &App{
		logger:    logger,
		chat:      chat,
		directory: directory,
	}
	startMsg := fmt.Sprintf("changeset-chihuahua version %s starting up", Version)
	go app.SendToAdmin(context.Background(), startMsg)
	return app
}

func (a *App) CommentAdded(author events.GerritUser, change events.GerritChange, comment string) {
	ctx, cancel := context.WithTimeout(context.Background(), *notificationTimeout)
	defer cancel()

	if change.Owner.Username != author.Username {
		owner := a.lookupGerritUser(ctx, change.Owner)
		if owner != "" {
			_, err := a.chat.SendToUserByID(ctx, owner, fmt.Sprintf("%s commented on your changeset #%d (%s): %s", author.Name, change.Number, change.Topic, comment))
			if err != nil {
				a.logger.Warn("failed to send notification", zap.String("slack-id", owner), zap.String("gerrit-username", change.Owner.Username))
			}
		}
	}
	// else, determine somehow if this is a reply to another user, and notify them?
}

func (a *App) ReviewRequested(author events.GerritUser, change events.GerritChange) {
}

func (a *App) SendToAdmin(ctx context.Context, message string) {
	if admin == nil {
		a.logger.Error("not sending message to admin; no admin configured", zap.String("message", message))
		return
	}
	if _, err := a.chat.SendToUserByEmail(ctx, *admin, message); err != nil {
		a.logger.Error("not sending message to admin; could not look up admin", zap.String("admin-email", *admin), zap.String("message", message), zap.Error(err))
		return
	}
}

func (a *App) lookupGerritUser(ctx context.Context, user events.GerritUser) string {
	chatID, err := a.directory.LookupByGerritUsername(ctx, user.Username)
	if err != nil {
		a.logger.Warn("user not found in directory", zap.String("username", user.Username))
		return ""
	}
	return chatID
}

/*
func (a *App) notify(ctx context.Context, user, message string) {
	channel, found := map[string]string{
		"zeebo":  "@jeff",
		"jtolds": "@jt",
	}[user]
	if !found {
		a.logger.Error("no slack username found", zap.String("gerrit-username", user))
		a.chat.SendToAdmin(ctx, fmt.Sprintf("slack username missing for %q", user))
		return
	}
	a.logger.Debug("sending message", zap.String("channel", channel), zap.String("message", message))
	err := a.chat.SendMessage(ctx, channel, message)
	if err != nil {
		a.logger.Error("sending chat message", zap.Error(err))
	}
}
*/
