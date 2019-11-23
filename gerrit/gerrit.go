package gerrit

import (
	"io/ioutil"
	"net/http"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/app"
	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

type EventHandler struct {
	logger    *zap.Logger
	chihuahua *app.App
}

func NewGerritEventSink(logger *zap.Logger, addr string, chihuahua *app.App) (*http.Server, error) {
	eventHandler := &EventHandler{
		logger:    logger,
		chihuahua: chihuahua,
	}
	stdLogger, err := zap.NewStdLogAt(logger, zap.ErrorLevel)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	httpServer := &http.Server{
		Addr:     addr,
		Handler:  eventHandler,
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
	srv.logger.Debug("gerrit event received", zap.ByteString("data", body))

	event, err := events.DecodeGerritEvent(body)
	if err != nil {
		srv.logger.Error("decoding payload", zap.Error(err))
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}

	switch ev := event.(type) {
	case *events.CommentAddedEvent:
		srv.chihuahua.CommentAdded(&ev.Author, &ev.Change)
	case *events.ReviewerAddedEvent:
		srv.chihuahua.ReviewRequested(&ev.Reviewer, &ev.Change)
	case *events.ReviewerDeletedEvent:
	case *events.RefUpdatedEvent:
	default:
		srv.logger.Error("unknown event type", zap.String("evtype", event.GetType()))
	}
	w.WriteHeader(http.StatusOK)
}
