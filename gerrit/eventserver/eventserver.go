package eventserver

import (
	"io/ioutil"
	"net/http"
	"strings"

	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

type EventHandler struct {
	logger   *zap.Logger
	receiver EventReceiver
}

type EventReceiver interface {
	GerritEventReceived(teamID string, ev events.GerritEvent)
}

func NewGerritEventSink(logger *zap.Logger, receiver EventReceiver) *EventHandler {
	return &EventHandler{
		logger:   logger,
		receiver: receiver,
	}
}

func (srv *EventHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	defer func() {
		rec := recover()
		if rec != nil {
			srv.logger.Error("panic in handler", zap.Any("panic-message", rec))
		}
	}()

	pathParts := strings.SplitN(r.URL.Path, "/", 2)
	teamID := pathParts[0]

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
	srv.receiver.GerritEventReceived(teamID, event)
	w.WriteHeader(http.StatusOK)
}
