package slack

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"github.com/nlopes/slack"
	"github.com/nlopes/slack/slackutilsx"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/jtolds/changesetchihuahua/messages"
)

var (
	ClientID      = flag.String("slack-client-id", "13639549360.893676519079", "ID issued to this app by Slack")
	ClientSecret  = flag.String("slack-client-secret", "", "Client secret issued to this app by Slack")
	debugSlackLib = flag.Bool("debug-slack-lib", false, "Log debug information from Slack client library")
)

const (
	AddToSlackButton = `<a href="%s"><img alt="Add to Slack" height="40" width="139" src="https://platform.slack-edge.com/img/add_to_slack.png" srcset="https://platform.slack-edge.com/img/add_to_slack.png 1x, https://platform.slack-edge.com/img/add_to_slack@2x.png 2x" /></a>`

	slackAuthURL = "https://slack.com/oauth/authorize"
)

var appScopes = []string{
	"bot", // to work as a bot
}

var userScopes = []string{
	"identity.basic", // see basic info about the installing user
}

type slackInterface struct {
	api *slack.Client
	rtm *slack.RTM

	rootLogger *zap.Logger
	logger     *zap.Logger

	bot       slack.UserDetails
	team      slack.Team
	oauthData slack.OAuthResponse

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
	messages.ChatSystem

	HandleEvents(ctx context.Context) error
}

func NewSlackInterface(ctx context.Context, logger *zap.Logger, setupData string) (EventedChatSystem, error) {
	var oauthData slack.OAuthResponse
	if err := json.Unmarshal([]byte(setupData), &oauthData); err != nil {
		return nil, err
	}

	slackLogger := logWrapper{logger}
	slackOptions := []slack.Option{slack.OptionLog(slackLogger)}
	if *debugSlackLib {
		slackOptions = append(slackOptions, slack.OptionDebug(true))
	}
	slackAPI := slack.New(oauthData.Bot.BotAccessToken, slackOptions...)
	slackRTM := slackAPI.NewRTM()

	go slackRTM.ManageConnection()
	go func() {
		<-ctx.Done()
		_ = slackRTM.Disconnect()
	}()
	s := &slackInterface{
		api:        slackAPI,
		rtm:        slackRTM,
		oauthData:  oauthData,
		rootLogger: logger,
		logger:     logger,
	}
	return s, nil
}

func (s *slackInterface) SetIncomingMessageCallback(cb func(userID, chanID string, isDM bool, text string) string) {
	s.incomingMessageCallback = cb
}

func (s *slackInterface) UnmarshalMessageHandle(handleJSON string) (messages.MessageHandle, error) {
	var mh messageHandle
	if err := json.Unmarshal([]byte(handleJSON), &mh); err != nil {
		return nil, err
	}
	return &mh, nil
}

func (s *slackInterface) HandleEvents(ctx context.Context) error {
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
	case "connection_error":
		return s.handleConnectionError(ctx, event.Data.(*slack.ConnectionErrorEvent))
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

func (s *slackInterface) handleHello(ctx context.Context, _ *slack.HelloEvent) error {
	s.logger.Info("got hello from slack")
	return nil
}

func (s *slackInterface) handleConnecting(ctx context.Context, eventData *slack.ConnectingEvent) error {
	s.logger.Info("connecting", zap.Int("attempt", eventData.Attempt), zap.Int("connection-count", eventData.ConnectionCount))
	return nil
}

func (s *slackInterface) handleConnectionError(ctx context.Context, eventData *slack.ConnectionErrorEvent) error {
	s.logger.Info("connection error", zap.Int("attempt", eventData.Attempt), zap.Duration("connection-count", eventData.Backoff), zap.Error(eventData.ErrorObj))
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

func (s *slackInterface) GetInstallingUser(ctx context.Context) (string, error) {
	return s.oauthData.UserID, nil
}

func (s *slackInterface) SendNotification(ctx context.Context, id, message string) (messages.MessageHandle, error) {
	// TODO: can the IM channel be cached? is it expected to remain valid as long as the userid?
	_, _, chanID, err := s.rtm.OpenIMChannelContext(ctx, id)
	if err != nil {
		return nil, err
	}
	return s.PostMessage(ctx, chanID, message)
}

func (s *slackInterface) SendPersonalReport(ctx context.Context, chatID, title string, items []string) (messages.MessageHandle, error) {
	return s.SendNotification(ctx, chatID, fmt.Sprintf("*%s*\n%s", title, strings.Join(items, "\n\n")))
}

func (s *slackInterface) SendChannelNotification(ctx context.Context, chanID, message string) (messages.MessageHandle, error) {
	return s.PostMessage(ctx, chanID, message)
}

func (s *slackInterface) SendChannelReport(ctx context.Context, chatID, title string, items []string) (messages.MessageHandle, error) {
	return s.PostMessage(ctx, chatID, fmt.Sprintf("*%s*\n\n%s", title, strings.Join(items, "\n\n")))
}

func (s *slackInterface) PostMessage(ctx context.Context, chanID, message string) (messages.MessageHandle, error) {
	ch, tm, err := s.api.PostMessageContext(ctx, chanID, slack.MsgOptionText(message, false))
	if err != nil {
		return nil, err
	}
	return &messageHandle{Channel: ch, Timestamp: tm}, nil
}

func (s *slackInterface) LookupChannelByName(ctx context.Context, channelName string) (string, error) {
	channelName = strings.TrimLeft(channelName, "#")
	cursor := ""
	for {
		conversationsPage, more, err := s.api.GetConversationsForUserContext(ctx, &slack.GetConversationsForUserParameters{
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

func (s *slackInterface) LookupUserByEmail(ctx context.Context, email string) (messages.ChatUser, error) {
	user, err := s.api.GetUserByEmailContext(ctx, email)
	if err != nil {
		return nil, err
	}
	presence, err := s.GetUserPresence(ctx, user.ID)
	if err != nil {
		return nil, err
	}
	return &slackUser{info: user, presence: presence}, nil
}

func (s *slackInterface) GetUserInfoByID(ctx context.Context, chatID string) (messages.ChatUser, error) {
	var eg errgroup.Group
	var user *slack.User
	var presence *slack.UserPresence
	eg.Go(func() (err error) {
		user, err = s.api.GetUserInfoContext(ctx, chatID)
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

func (s *slackInterface) InformBuildStarted(ctx context.Context, mh messages.MessageHandle, link string) error {
	// do nothing for now- not clear we can do anything very useful here that isn't overly
	// noisy for the users
	return nil
}

func (s *slackInterface) InformBuildSuccess(ctx context.Context, mh messages.MessageHandle, link string) error {
	mhObj, ok := mh.(*messageHandle)
	if !ok {
		return errs.New("given message handle is a %T, not a *messageHandle", mh)
	}
	return s.rtm.AddReactionContext(ctx, "white_check_mark", slack.NewRefToMessage(mhObj.Channel, mhObj.Timestamp))
}

func (s *slackInterface) InformBuildFailure(ctx context.Context, mh messages.MessageHandle, link string) error {
	mhObj, ok := mh.(*messageHandle)
	if !ok {
		return errs.New("given message handle is a %T, not a *messageHandle", mh)
	}
	_, _, err := s.rtm.PostMessageContext(ctx, mhObj.Channel, slack.MsgOptionText("Build failure: " + link, false), slack.MsgOptionTS(mhObj.Timestamp))
	reactionErr := s.rtm.AddReactionContext(ctx, "x", slack.NewRefToMessage(mhObj.Channel, mhObj.Timestamp))
	return errs.Combine(err, reactionErr)
}

func (s *slackInterface) InformBuildAborted(ctx context.Context, mh messages.MessageHandle, link string) error {
	mhObj, ok := mh.(*messageHandle)
	if !ok {
		return errs.New("given message handle is a %T, not a *messageHandle", mh)
	}
	return s.rtm.AddReactionContext(ctx, "no_entry_sign", slack.NewRefToMessage(mhObj.Channel, mhObj.Timestamp))
}

func GetOAuthToken(ctx context.Context, clientID, clientSecret, code, redirectURI string) (resp *slack.OAuthResponse, err error) {
	return slack.GetOAuthResponseContext(ctx, http.DefaultClient, clientID, clientSecret, code, redirectURI)
}

// GetOAuthV2Token is based on slack.GetOAuthResponseContext(), but uses oauth.v2.access (and
// hence a slightly different response type).
func GetOAuthV2Token(ctx context.Context, clientID, clientSecret, code, redirectURI string) (resp *OAuthV2Response, err error) {
	values := url.Values{
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"code":          {code},
		"redirect_uri":  {redirectURI},
	}
	response := &OAuthV2Response{}
	if err := postForm(ctx, slack.APIURL+"oauth.v2.access", values, response); err != nil {
		return nil, err
	}
	return response, response.Err()
}

// postForm is very similar to slack.postForm(); reimplemented for the sake of getOAuthToken().
func postForm(ctx context.Context, endpoint string, values url.Values, intf interface{}) error {
	reqBody := strings.NewReader(values.Encode())
	req, err := http.NewRequest("POST", endpoint, reqBody)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(ctx)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return errs.New("unexpected status code %d from Slack", resp.StatusCode)
	}
	parser := json.NewDecoder(resp.Body)
	if err := parser.Decode(intf); err != nil {
		return err
	}
	return nil
}

type OAuthV2ResponseTeam struct {
	Name string `json:"name"`
	ID   string `json:"id"`
}

type OAuthV2ResponseUser struct {
	ID          string `json:"id"`
	Scope       string `json:"scope"`
	AccessToken string `json:"access_token"`
	TokenType   string `json:"token_type"`
}

type OAuthV2Response struct {
	AccessToken string              `json:"access_token"`
	TokenType   string              `json:"token_type"`
	Scope       string              `json:"scope"`
	BotUserID   string              `json:"bot_user_id"`
	AppID       string              `json:"app_id"`
	Team        OAuthV2ResponseTeam `json:"team"`
	Enterprise  OAuthV2ResponseTeam `json:"enterprise"`
	AuthedUser  OAuthV2ResponseUser `json:"authed_user"`
	slack.SlackResponse
}

func escapeText(t string) string {
	return slackutilsx.EscapeMessage(t)
}

type Formatter struct {}

func (f *Formatter) FormatBold(msg string) string {
	return "*" + msg + "*"
}

func (f *Formatter) FormatItalic(msg string) string {
	return "_" + msg + "_"
}

func (f *Formatter) FormatBlockQuote(msg string) string {
	lines := strings.Split(msg, "\n")
	return "> " + strings.Join(lines, "\n> ")
}

func (f *Formatter) FormatChangeLink(project string, number int, url, subject string) string {
	return fmt.Sprintf("[%s@%d] %s", escapeText(project), number, f.FormatLink(url, subject))
}

func (f *Formatter) FormatUserLink(chatID string) string {
	return fmt.Sprintf("<@%s>", chatID)
}

func (f *Formatter) FormatChannelLink(channelID string) string {
	return fmt.Sprintf("<#%s>", channelID)
}

func (f *Formatter) FormatLink(url, text string) string {
	return fmt.Sprintf("<%s|%s>", url, escapeText(text))
}

func (f *Formatter) UnwrapUserLink(userLink string) string {
	if len(userLink) > 3 && userLink[0] == '<' && userLink[1] == '@' && userLink[len(userLink)-1] == '>' {
		return userLink[2 : len(userLink)-1]
	}
	return ""
}

func (f *Formatter) UnwrapChannelLink(channelLink string) string {
	if len(channelLink) > 3 && channelLink[0] == '<' && channelLink[1] == '#' && channelLink[len(channelLink)-1] == '>' {
		channelLink = channelLink[2 : len(channelLink)-1]
		if pos := strings.Index(channelLink, "|"); pos >= 0 {
			channelLink = channelLink[0:pos]
		}
		return channelLink
	}
	return ""
}

func (f *Formatter) UnwrapLink(link string) string {
	if link[0] == '<' && link[len(link)-1] == '>' {
		link = link[1:len(link)-1]
		if pos := strings.Index(link, "|"); pos >= 0 {
			link = link[0:pos]
		}
	}
	return link
}

// messageHandle provides a handle to a Slack message, which can be used to change
// or delete that message later.
type messageHandle struct {
	Channel   string
	Timestamp string
}

func (mh *messageHandle) SentTime() time.Time {
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

func (mh *messageHandle) MarshalJSON() ([]byte, error) {
	return json.Marshal(*mh)
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

func AssembleSlackAuthURL(redirectURL string) string {
	values := make(url.Values)
	values.Set("client_id", *ClientID)
	values.Set("scope", strings.Join(appScopes, ","))
	values.Set("user_scope", strings.Join(userScopes, ","))
	values.Set("redirect_uri", redirectURL)
	return slackAuthURL + "?" + values.Encode()
}
