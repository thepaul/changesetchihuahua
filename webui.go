package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"strconv"
	"strings"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/slack"
)

var (
	externalURL = flag.String("external-url", "https://localhost.localdomain/", "The URL by which external hosts (including Slack servers) can contact this server")
)

type UIWebHandler struct {
	*http.Server
	logger   *zap.Logger
	governor *Governor

	slackRedirectURL string
}

func NewUIWebHandler(addr string, logger *zap.Logger, governor *Governor, gerritEventSink http.Handler) (*UIWebHandler, error) {
	mux := NewLoggingMux(logger)
	stdLogger, err := zap.NewStdLogAt(logger, zap.ErrorLevel)
	if err != nil {
		return nil, errs.Wrap(err)
	}
	httpServer := &http.Server{
		Addr:     addr,
		Handler:  mux,
		ErrorLog: stdLogger,
	}
	uiServer := &UIWebHandler{
		Server:           httpServer,
		logger:           logger,
		governor:         governor,
		slackRedirectURL: strings.TrimRight(*externalURL, "/") + "/slack/oauth",
	}
	mux.Handle("/gerrit/", gerritEventSink)
	mux.HandleFunc("/slack/", uiServer.maybeOAuthRedirect)
	mux.HandleFunc("/slack/setup", uiServer.Setup)
	return uiServer, nil
}

func (wh *UIWebHandler) Serve(ctx context.Context) error {
	wh.logger.Info("Listening for web connections", zap.String("bind-address", wh.Server.Addr))
	go func() {
		<-ctx.Done()
		_ = wh.Server.Close()
	}()
	return wh.Server.ListenAndServe()
}

func (wh *UIWebHandler) Setup(w http.ResponseWriter, r *http.Request) {
	html := fmt.Sprintf(slack.AddToSlackButton, slack.AssembleSlackAuthURL(wh.slackRedirectURL))
	header := w.Header()
	header.Add("Content-Length", strconv.Itoa(len(html)))
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(html))
}

func (wh *UIWebHandler) maybeOAuthRedirect(w http.ResponseWriter, r *http.Request) {
	values := r.URL.Query()
	code := values.Get("code")
	if code == "" {
		w.WriteHeader(http.StatusNotFound)
	}
	w.WriteHeader(http.StatusOK)
	resp, err := slack.GetOAuthToken(r.Context(), *slack.ClientID, *slack.ClientSecret, code, wh.slackRedirectURL)
	if err != nil {
		wh.logger.Error("Failed to acquire OAuth token from Slack", zap.Error(err))
		_, _ = w.Write([]byte(fmt.Sprintf("Failed to acquire OAuth token from Slack: %v", err)))
		return
	}
	wh.logger.Info("OAuth flow success", zap.Any("response", resp))
	jsonBlob, err := json.Marshal(resp)
	if err != nil {
		wh.logger.Error("failed to marshal OAuth token data?!", zap.Error(err))
		_, _ = w.Write([]byte("Failed to record OAuth token data."))
		return
	}
	if err := wh.governor.NewTeam(resp.TeamID, string(jsonBlob)); err != nil {
		wh.logger.Error("failed to create new team record", zap.Error(err))
		_, _ = w.Write([]byte("Failed to record new team."))
		return
	}

	_, _ = w.Write([]byte(fmt.Sprintf("Success! Changeset Chihuahua is installed to your %s workspace.\n", resp.TeamName)))
}

type LoggingMux struct {
	*http.ServeMux
	logger *zap.Logger
}

func NewLoggingMux(logger *zap.Logger) *LoggingMux {
	mux := http.NewServeMux()
	return &LoggingMux{ServeMux: mux, logger: logger}
}

func (lm *LoggingMux) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	logger := lm.logger.With(zap.String("remote-addr", r.RemoteAddr), zap.String("request-uri", r.RequestURI), zap.String("method", r.Method))
	logger.Info("incoming request")
	loggingWriter := loggingResponseWriter{ResponseWriter: w}
	lm.ServeMux.ServeHTTP(&loggingWriter, r)
	logger.Info("request completed", zap.Int("bytes-written", loggingWriter.bytesWritten), zap.Int("status-code", loggingWriter.statusCode))
}

type loggingResponseWriter struct {
	http.ResponseWriter
	bytesWritten int
	statusCode   int
}

func (w *loggingResponseWriter) Write(data []byte) (int, error) {
	n, err := w.ResponseWriter.Write(data)
	w.bytesWritten += n
	return n, err
}

func (w *loggingResponseWriter) WriteHeader(statusCode int) {
	w.statusCode = statusCode
	w.ResponseWriter.WriteHeader(statusCode)
}
