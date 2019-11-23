package app

import (
	"context"
	"flag"
	"fmt"

	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

var (
	admin = flag.String("admin", "", "Identity of an admin by email address. Will be contacted through the chat system, not email")
)

const (
	Version = "0.0.1"
)

type MessageHandle interface{}

type ChatSystem interface {
	SupportsMessageEditing() bool
	SupportsMessageDeletion() bool

	SendToUserByEmail(ctx context.Context, email, message string) (MessageHandle, error)
	SendToUserByID(ctx context.Context, id, message string) (MessageHandle, error)
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

func (a *App) CommentAdded(ctx context.Context, author *events.GerritUser, change *events.GerritChange) {
	usersToTell := make([]string, 0, len(change.AllReviewers) + 1)
	if author.Username != change.Owner.Username {
		usersToTell = append(usersToTell, a.lookupGerritUser(ctx, change.Owner))
	}
	usersToTell = append(usersToTell, change.Owner)
	userToTell := change.Owner
}

func (a *App) ReviewRequested(author *events.GerritUser, change *events.GerritChange) {
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

var hardcoded = map[string]string{
	"zeebo": "@jeff",
	"jtolds": "@jt",
	"thepaul": "@thepaul",
}

func (a *App) lookupGerritUser(ctx context.Context, user events.GerritUser) string {
	name, found := hardcoded[user.Username]
	if !found {
		// try looking up username directly; maybe they are the same
		// TODO this probably needs to come out before this can be deployed as a public app
		name = user.Username
	}
	var id string
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
