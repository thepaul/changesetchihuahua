package eventserver

import (
	"context"
	"flag"
	"io/ioutil"
	"net/http"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/app"
	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

var notificationTimeout = flag.Duration("notify-timeout", time.Minute*30, "Maximum amount of time to spend trying to deliver a notification")

type EventHandler struct {
	logger    *zap.Logger
	chihuahua *app.App
}

func NewGerritEventSink(logger *zap.Logger, addr string, chihuahua *app.App) (*http.Server, error) {
	eventHandler := &EventHandler{
		logger:    logger,
		chihuahua: chihuahua,
	}
	mux := http.NewServeMux()
	mux.Handle("/gerrit/", eventHandler)
	stdLogger, err := zap.NewStdLogAt(logger, zap.ErrorLevel)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	httpServer := &http.Server{
		Addr:     addr,
		Handler:  mux,
		ErrorLog: stdLogger,
	}
	return httpServer, nil
}

func (srv *EventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		rec := recover()
		if rec != nil {
			srv.logger.Error("panic in handler", zap.Any("panic-message", rec))
		}
	}()

	body, err := ioutil.ReadAll(r.Body)
	if err != nil {
		srv.logger.Error("reading payload", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event, err := events.DecodeGerritEvent(body)
	if err != nil {
		srv.logger.Error("decoding payload", zap.Error(err), zap.ByteString("data", body))
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	srv.logger.Debug("received gerrit event", zap.String("origin", r.RemoteAddr), zap.String("event-type", event.GetType()))

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), *notificationTimeout)
		defer cancel()

		switch ev := event.(type) {
		case *events.CommentAddedEvent:
			srv.chihuahua.CommentAdded(ctx, ev.Author, ev.Change, ev.PatchSet, ev.Comment, ev.EventCreatedAt())
		case *events.ReviewerAddedEvent:
			srv.chihuahua.ReviewerAdded(ctx, ev.Reviewer, ev.Change, ev.EventCreatedAt())
		case *events.PatchSetCreatedEvent:
			srv.chihuahua.PatchSetCreated(ctx, ev.Uploader, ev.Change, ev.PatchSet)
		case *events.ChangeAbandonedEvent:
			srv.chihuahua.ChangeAbandoned(ctx, ev.Abandoner, ev.Change, ev.Reason)
		case *events.ChangeMergedEvent:
			srv.chihuahua.ChangeMerged(ctx, ev.Submitter, ev.Change, ev.PatchSet)
		case *events.AssigneeChangedEvent:
			srv.chihuahua.AssigneeChanged(ctx, ev.Changer, ev.Change, ev.OldAssignee)
		case *events.DroppedOutputEvent:
			srv.logger.Warn("gerrit reports dropped events")
		case *events.ReviewerDeletedEvent:
		default:
			srv.logger.Error("unknown event type", zap.String("event-type", event.GetType()))
		}
	}()
	w.WriteHeader(http.StatusOK)
}
