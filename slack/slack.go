package slack

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackutilsx"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

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

	incomingMessageCallback func(userID, chanID string, isDM bool, text string) string
}

type logWrapper struct {
	*zap.Logger
}

// log all github.com/nlopes/slack messages at Debug level
func (lw logWrapper) Output(callDepth int, s string) error {
	lw.WithOptions(zap.AddCallerSkip(callDepth)).Debug(s)
	return nil
}

type EventedChatSystem interface {
	app.ChatSystem

	HandleEvents(ctx context.Context, chihuahua *app.App) error
}

func NewSlackInterface(logger *zap.Logger) (EventedChatSystem, error) {
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

func (s *slackInterface) SetIncomingMessageCallback(cb func(userID, chanID string, isDM bool, text string) string) {
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
		reply := s.incomingMessageCallback(eventData.User, eventData.Channel, strings.HasPrefix(eventData.Channel, "D"), eventData.Text)
		if reply != "" {
			_, err := s.PostMessage(ctx, eventData.Channel, reply)
			if err != nil {
				s.logger.Debug("failed to send response to message", zap.Error(err), zap.String("response", reply), zap.Any("message", *eventData))
			}
		}
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

func (s *slackInterface) SendNotification(ctx context.Context, id, message string) (app.MessageHandle, error) {
	// TODO: can the IM channel be cached? is it expected to remain valid as long as the userid?
	_, _, chanID, err := s.rtm.OpenIMChannelContext(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.PostMessage(ctx, chanID, message)
}

func (s *slackInterface) SendPersonalReport(ctx context.Context, chatID string, items []string) (app.MessageHandle, error) {
	return s.SendNotification(ctx, chatID, strings.Join(items, "\n\n"))
}

func (s *slackInterface) SendChannelReport(ctx context.Context, chatID string, items []string) (app.MessageHandle, error) {
	return s.PostMessage(ctx, chatID, strings.Join(items, "\n\n"))
}

func (s *slackInterface) PostMessage(ctx context.Context, chanID, message string) (app.MessageHandle, error) {
	ch, tm, err := s.api.PostMessageContext(ctx, chanID, slack.MsgOptionText(message, false))
	if err != nil {
		return nil, err
	}
	return &MessageHandle{Channel: ch, Timestamp: tm}, nil
}

func (s *slackInterface) LookupChannelByName(ctx context.Context, channelName string) (string, error) {
	channelName = strings.TrimLeft(channelName, "#")
	cursor := ""
	for {
		conversationsPage, more, err := s.rtm.GetConversationsForUserContext(ctx, &slack.GetConversationsForUserParameters{
			Cursor:          cursor,
			ExcludeArchived: true,
		})
		if err != nil {
			return "", err
		}
		for _, conversation := range conversationsPage {
			if conversation.Name == channelName || conversation.NameNormalized == channelName {
				return conversation.ID, nil
			}
		}
		if more == "" {
			return "", errs.New("channel %q not found", channelName)
		}
		cursor = more
	}
}

func (s *slackInterface) LookupUserByEmail(ctx context.Context, email string) (app.ChatUser, error) {
	user, err := s.rtm.GetUserByEmailContext(ctx, email)
	if err != nil {
		return nil, err
	}
	presence, err := s.GetUserPresence(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &slackUser{info: user, presence: presence}, nil
}

func (s *slackInterface) GetUserInfoByID(ctx context.Context, chatID string) (app.ChatUser, error) {
	var eg errgroup.Group
	var user *slack.User
	var presence *slack.UserPresence
	eg.Go(func() (err error) {
		user, err = s.rtm.GetUserInfoContext(ctx, chatID)
		return err
	})
	eg.Go(func() (err error) {
		presence, err = s.GetUserPresence(ctx, chatID)
		return err
	})
	if err := eg.Wait(); err != nil {
		return nil, err
	}
	return &slackUser{info: user, presence: presence}, nil
}

func (s *slackInterface) GetUserPresence(ctx context.Context, chatID string) (*slack.UserPresence, error) {
	// TODO: can this use rtm.GetUserPresenceContext instead? Docs suggest that that one is
	// rate-limited, and there isn't any mention of rate limiting on this web-API version,
	// so maybe this is the easiest way to avoid any headaches.
	return s.api.GetUserPresenceContext(ctx, chatID)
}

func (s *slackInterface) FormatBold(msg string) string {
	return "*" + msg + "*"
}

func (s *slackInterface) FormatItalic(msg string) string {
	return "_" + msg + "_"
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
	if len(userLink) > 3 && userLink[0] == '<' && userLink[1] == '@' && userLink[len(userLink)-1] == '>' {
		return userLink[2 : len(userLink)-1]
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

func (mh *MessageHandle) SentTime() time.Time {
	parts := strings.SplitN(mh.Timestamp, ".", 2)
	sec, _ := strconv.ParseInt(parts[0], 10, 64)
	nano := int64(0)
	if len(parts) > 1 {
		for len(parts[1]) < 9 { // haha this is dumb
			parts[1] += "0"
		}
		nano, _ = strconv.ParseInt(parts[1][:9], 10, 64)
	}
	return time.Unix(sec, nano)
}

type slackUser struct {
	info     *slack.User
	presence *slack.UserPresence
}

func (u *slackUser) ChatID() string {
	return u.info.ID
}

func (u *slackUser) RealName() string {
	return u.info.Profile.RealName
}

func (u *slackUser) IsOnline() bool {
	return u.presence.Presence == "active"
}

func (u *slackUser) Timezone() *time.Location {
	return time.FixedZone(fmt.Sprintf("offset%d", u.info.TZOffset), u.info.TZOffset)
}
