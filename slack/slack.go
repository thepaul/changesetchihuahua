package slack

import (
	"context"
	"errors"
	"flag"

	"github.com/nlopes/slack"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/app"
)

var (
	botOAuthToken = flag.String("bot-oauth", "", "OAuth token for bot")
	debugSlackLib = flag.Bool("debug-slack-lib", false, "Log debug information from Slack client library")
)

type slackInterface struct {
	api *slack.Client
	rtm *slack.RTM

	rootLogger *zap.Logger
	logger     *zap.Logger

	bot  slack.UserDetails
	team slack.Team
}

type logWrapper struct {
	*zap.Logger
}

// log all github.com/nlopes/slack messages at Debug level
func (lw logWrapper) Output(callDepth int, s string) error {
	lw.WithOptions(zap.AddCallerSkip(callDepth)).Debug(s)
	return nil
}

func NewSlackInterface(logger *zap.Logger) (*slackInterface, error) {
	if botOAuthToken == nil {
		return nil, errors.New("no bot oauth token set")
	}
	slackLogger := logWrapper{logger}
	slackOptions := []slack.Option{slack.OptionLog(slackLogger)}
	if *debugSlackLib {
		slackOptions = append(slackOptions, slack.OptionDebug(true))
	}
	slackAPI := slack.New(*botOAuthToken, slackOptions...)
	slackRTM := slackAPI.NewRTM()

	go slackRTM.ManageConnection()
	return &slackInterface{
		api:        slackAPI,
		rtm:        slackRTM,
		rootLogger: logger,
		logger:     logger,
	}, nil
}

func (s *slackInterface) SupportsMessageEditing() bool  { return true }
func (s *slackInterface) SupportsMessageDeletion() bool { return true }

func (s *slackInterface) HandleEvents(ctx context.Context, chihuahua *app.App) error {
	for {
		select {
		case slackEvent := <-s.rtm.IncomingEvents:
			err := s.handleEvent(ctx, slackEvent)
			if err != nil {
				s.logger.Error("failure while handling event", zap.String("event-type", slackEvent.Type), zap.Error(err))
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

func (s *slackInterface) handleEvent(ctx context.Context, event slack.RTMEvent) (err error) {
	s.logger.Debug("received event", zap.String("event-type", event.Type))

	switch event.Type {
	case "hello":
		return s.handleHello(ctx, event.Data.(*slack.HelloEvent))
	case "connecting":
		return s.handleConnecting(ctx, event.Data.(*slack.ConnectingEvent))
	case "connected":
		return s.handleConnected(ctx, event.Data.(*slack.ConnectedEvent))
	case "message":
		return s.handleMessage(ctx, event.Data.(*slack.MessageEvent))
	case "incoming_error":
		return s.handleIncomingError(ctx, event.Data.(*slack.IncomingEventError))
	case "user_typing":
	case "latency_report":
		return s.handleLatencyReport(ctx, event.Data.(*slack.LatencyReport))
	case "disconnected":
		return s.handleDisconnected(ctx, event.Data.(*slack.DisconnectedEvent))
	}

	s.logger.Debug("event type not recognized", zap.String("event-type", event.Type))
	return nil
}

func (s *slackInterface) handleHello(ctx context.Context, eventData *slack.HelloEvent) error {
	s.logger.Info("got hello from slack")
	return nil
}

func (s *slackInterface) handleConnecting(ctx context.Context, eventData *slack.ConnectingEvent) error {
	s.logger.Info("connecting", zap.Int("attempt", eventData.Attempt), zap.Int("connection-count", eventData.ConnectionCount))
	return nil
}

func (s *slackInterface) handleConnected(ctx context.Context, eventData *slack.ConnectedEvent) error {
	s.team = *eventData.Info.Team
	s.bot = *eventData.Info.User
	s.logger = s.rootLogger.With(zap.String("team", s.team.ID))

	s.logger.Info("connected to slack",
		zap.Int("connection-count", eventData.ConnectionCount),
		zap.String("team-name", s.team.Name),
		zap.String("team-domain", s.team.Domain),
		zap.String("bot-name", s.bot.Name),
		zap.String("bot-id", s.bot.ID))
	return nil
}

func (s *slackInterface) handleMessage(ctx context.Context, eventData *slack.MessageEvent) error {
	s.logger.Info("received message",
		zap.Any("message", *eventData),
		zap.Any("attachments", eventData.Attachments),
		zap.Any("blocks", eventData.Blocks),
		zap.Any("comment", eventData.Comment))
	return nil
}

func (s *slackInterface) handleIncomingError(ctx context.Context, eventData *slack.IncomingEventError) error {
	s.logger.Info("incoming error reported by slack", zap.String("error", eventData.Error()))
	return nil
}

func (s *slackInterface) handleLatencyReport(ctx context.Context, eventData *slack.LatencyReport) error {
	// TODO: monkit stat eventData.Value ?
	return nil
}

func (s *slackInterface) handleDisconnected(ctx context.Context, eventData *slack.DisconnectedEvent) error {
	s.logger.Info("disconnected from slack", zap.Bool("intentional", eventData.Intentional), zap.String("cause", eventData.Cause.Error()))
	return nil
}

func (s *slackInterface) SendToUserByEmail(ctx context.Context, email, message string) (app.MessageHandle, error) {
	user, err := s.rtm.GetUserByEmailContext(ctx, email)
	if err != nil {
		return nil, errs.New("failed to look up user %s: %v", email, err)
	}
	ch, tm, _, err := s.rtm.SendMessageContext(ctx, user.ID, slack.MsgOptionText(message, false))
	if err != nil {
		return nil, err
	}
	return &SlackMessageHandle{Channel: ch, Timestamp: tm}, nil
}

func (s *slackInterface) SendToUserByID(ctx context.Context, id, message string) (app.MessageHandle, error) {
	ch, tm, _, err := s.rtm.SendMessageContext(ctx, id, slack.MsgOptionText(message, false))
	if err != nil {
		return nil, err
	}
	return &SlackMessageHandle{Channel: ch, Timestamp: tm}, nil
}

func (s *slackInterface) LookupUserByEmail(ctx context.Context, email string) (string, error) {
	user, err := s.rtm.GetUserByEmailContext(ctx, email)
	if err != nil {
		return "", err
	}
	return user.ID, nil
}

// SlackMessageHandle provides a handle to a Slack message, which can be used to change
// or delete that message later.
type SlackMessageHandle struct {
	Channel   string
	Timestamp string
}
