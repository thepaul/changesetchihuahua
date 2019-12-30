package slack

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strings"
	"time"

	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackutilsx"
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

	incomingMessageCallback func(userID, chanID string, isDM bool, text string)
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

func (s *slackInterface) SetIncomingMessageCallback(cb func(userID, chanID string, isDM bool, text string)) {
	s.incomingMessageCallback = cb
}

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
	s.logger.Debug("received slack event", zap.String("event-type", event.Type))

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
	case "latency_report":
		return s.handleLatencyReport(ctx, event.Data.(*slack.LatencyReport))
	case "disconnected":
		return s.handleDisconnected(ctx, event.Data.(*slack.DisconnectedEvent))
	// this type doesn't come from Slack; just from our slack lib when it has a problem
	// understanding the JSON. we have to do this in order to get a useful error message.
	case "unmarshalling_error":
		return s.handleUnmarshallingError(ctx, event.Data.(*slack.UnmarshallingErrorEvent))

	case "user_typing":
	case "desktop_notification":

	default:
		s.logger.Debug("event type not recognized", zap.String("event-type", event.Type))
	}

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
	if eventData.Msg.SubType == "bot_message" {
		// ignore messages from bots, including echoes of messages from this bot itself
		return nil
	}
	s.logger.Debug("received message", zap.Any("message", *eventData))

	if s.incomingMessageCallback != nil {
		s.incomingMessageCallback(eventData.User, eventData.Channel, strings.HasPrefix(eventData.Channel, "D"), eventData.Text)
	}
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

func (s *slackInterface) handleUnmarshallingError(ctx context.Context, eventData *slack.UnmarshallingErrorEvent) error {
	s.logger.Error("failed to unmarshal event from slack", zap.Error(eventData))
	return nil
}

func (s *slackInterface) SendToUserByID(ctx context.Context, id, message string) (app.MessageHandle, error) {
	// TODO: can the IM channel be cached? is it expected to remain valid as long as the userid?
	_, _, chanID, err := s.rtm.OpenIMChannelContext(ctx, id)
	if err != nil {
		return nil, err
	}
	ch, tm, err := s.api.PostMessageContext(ctx, chanID, slack.MsgOptionText(message, false))
	if err != nil {
		return nil, err
	}
	return &MessageHandle{Channel: ch, Timestamp: tm}, nil
}

func (s *slackInterface) LookupUserByEmail(ctx context.Context, email string) (string, error) {
	user, err := s.rtm.GetUserByEmailContext(ctx, email)
	if err != nil {
		return "", err
	}
	return user.ID, nil
}

func (s *slackInterface) GetInfoByID(ctx context.Context, chatID string) (app.ChatUser, error) {
	user, err := s.rtm.GetUserInfoContext(ctx, chatID)
	if err != nil {
		return nil, err
	}
	// TODO: can this use rtm.GetUserPresenceContext instead? Docs suggest that that one is
	// rate-limited, and there isn't any mention of rate limiting on this web-API version,
	// so maybe this is the easiest way to avoid any headaches.
	presence, err := s.api.GetUserPresenceContext(ctx, chatID)
	if err != nil {
		return nil, err
	}
	return &slackUser{info: user, presence: presence}, nil
}

func (s *slackInterface) FormatChangeLink(project string, number int, url, subject string) string {
	return fmt.Sprintf("[%s#%d] %s", escapeText(project), number, s.FormatLink(url, subject))
}

func (s *slackInterface) FormatUserLink(chatID string) string {
	return fmt.Sprintf("<@%s>", chatID)
}

func (s *slackInterface) FormatLink(url, text string) string {
	return fmt.Sprintf("<%s|%s>", url, escapeText(text))
}

func (s *slackInterface) UnwrapUserLink(userLink string) string {
	if len(userLink) > 3 && userLink[0] == '<' && userLink[1] == '@' && userLink[len(userLink)-1] == '>'	{
		return userLink[2:len(userLink)-1]
	}
	return ""
}

func escapeText(t string) string {
	return slackutilsx.EscapeMessage(t)
}

// MessageHandle provides a handle to a Slack message, which can be used to change
// or delete that message later.
type MessageHandle struct {
	Channel   string
	Timestamp string
}

type slackUser struct {
	info *slack.User
	presence *slack.UserPresence
}

func (u *slackUser) RealName() string {
	return u.info.Profile.RealName
}

func (u *slackUser) IsOnline() bool {
	return u.presence.Online
}

func (u *slackUser) TimeLocation() *time.Location {
	return time.FixedZone(fmt.Sprintf("offset%d", u.info.TZOffset), u.info.TZOffset)
}
