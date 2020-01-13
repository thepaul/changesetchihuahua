package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"

	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/gerrit/events"
	"github.com/jtolds/changesetchihuahua/slack"
)

type uiWebState struct {
	logger      *zap.Logger
	governor    *Governor
	externalURL *url.URL
}

type uiWebServer struct {
	state   *uiWebState
	handler http.Handler
}

func newUIWebState(logger *zap.Logger, governor *Governor, externalURL *url.URL) *uiWebState {
	return &uiWebState{
		logger:              logger,
		governor:            governor,
		externalURL:         externalURL,
	}
}

func (ws *uiWebState) slackRedirectURL() string {
	redirectURL := *ws.externalURL
	redirectURL.Path = path.Join(redirectURL.Path, "slack", "oauth")
	return redirectURL.String()
}

func (ws *uiWebState) Setup(w http.ResponseWriter, r *http.Request) {
	html := fmt.Sprintf(slack.AddToSlackButton, slack.AssembleSlackAuthURL(ws.slackRedirectURL()))
	header := w.Header()
	header.Add("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

func (ws *uiWebState) maybeOAuthRedirect(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	code := values.Get("code")
	if code == "" {
		w.WriteHeader(http.StatusNotFound)
	}
	w.WriteHeader(http.StatusOK)
	resp, err := slack.GetOAuthToken(r.Context(), *slack.ClientID, *slack.ClientSecret, code, ws.slackRedirectURL())
	if err != nil {
		ws.logger.Error("Failed to acquire OAuth token from Slack", zap.Error(err))
		_, _ = w.Write([]byte(fmt.Sprintf("Failed to acquire OAuth token from Slack: %v", err)))
		return
	}
	ws.logger.Info("OAuth flow success", zap.Any("response", resp))
	jsonBlob, err := json.Marshal(resp)
	if err != nil {
		ws.logger.Error("failed to marshal OAuth token data?!", zap.Error(err))
		_, _ = w.Write([]byte("Failed to record OAuth token data."))
		return
	}
	if err := ws.governor.NewTeam(resp.TeamID, string(jsonBlob)); err != nil {
		ws.logger.Error("failed to create new team record", zap.Error(err))
		_, _ = w.Write([]byte("Failed to record new team."))
		return
	}

	_, _ = w.Write([]byte(fmt.Sprintf("Success! Changeset Chihuahua is installed to your %s workspace.\n", resp.TeamName)))
}

func (ws *uiWebState) gerritEvent(w http.ResponseWriter, r *http.Request) {
	urlPath := strings.TrimPrefix(r.URL.Path, "/gerrit/")
	pathParts := strings.SplitN(urlPath, "/", 2)
	teamID := pathParts[0]

	limited := io.LimitedReader{R: r.Body, N: events.MaxEventPayloadSize}
	body, err := ioutil.ReadAll(&limited)
	if err != nil {
		ws.logger.Error("reading gerrit payload", zap.Error(err))
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	event, err := events.DecodeGerritEvent(body)
	if err != nil {
		ws.logger.Error("decoding payload", zap.Error(err), zap.ByteString("data", body))
		w.WriteHeader(http.StatusUnprocessableEntity)
		return
	}
	ws.logger.Debug("received gerrit event", zap.String("origin", r.RemoteAddr), zap.String("event-type", event.GetType()))
	ws.governor.GerritEventReceived(teamID, event)
	w.WriteHeader(http.StatusOK)
}

func newUIWebHandler(logger *zap.Logger, state *uiWebState, isSecure bool) http.Handler {
	mux := NewLoggingMux(logger)
	mux.HandleFunc("/gerrit/", state.gerritEvent)
	if isSecure {
		mux.HandleFunc("/slack/", state.maybeOAuthRedirect)
		mux.HandleFunc("/slack/setup", state.Setup)
	}
	return mux
}

func newUIWebServer(state *uiWebState, handler http.Handler) *uiWebServer {
	return &uiWebServer{
		state:   state,
		handler: handler,
	}
}

func (server *uiWebServer) Serve(ctx context.Context, listener net.Listener) error {
	server.state.logger.Info("Listening for connections", zap.String("bind-address", listener.Addr().String()))
	go func() {
		<-ctx.Done()
		_ = listener.Close()
	}()
	return http.Serve(listener, server.handler)
}
