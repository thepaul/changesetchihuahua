package app

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/gerrit"
	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

const (
	// default hour (in 24-hour time) in each user's local time zone when their personal
	// work needed report will be sent to them
	defaultPersonalReportHour = 11
	// default hour (in 24-hour time) in UTC when the daily report will be sent to the
	// global-report-channel
	defaultGlobalReportHour = 15
	// Gerrit query to use for determining change sets with work needed for the global report
	defaultWorkNeededQuery = "is:open -is:wip -label:Code-Review=-2"
)

var (
	minIntervalBetweenReports = flag.Duration("min-interval-between-reports", time.Hour*10, "Minimum amount of time that must elapse before more personalized Gerrit reports are sent to a given user")

	inlineCommentMaxAge = flag.Duration("inline-comment-max-age", time.Hour, "Inline comments older than this will not be reported, even if not found in the cache")
)

const (
	gerritQueryPageSize = 100
	globalReportMaxSize = 100

	workingDayStartHour = 9
	workingDayEndHour   = 17
)

// ChatSystem abstracts interaction with an instant-message chat system, so that this doesn't
// become dependent on Slack specifically. I believe this interface ought to work for Hipchat
// and IRC, but I'm not familiar with the APIs of many other multi-user chat networks.
type ChatSystem interface {
	SetIncomingMessageCallback(cb func(userID, chanID string, isDM bool, text string) string)
	GetInstallingUser(ctx context.Context) (string, error)

	GetUserInfoByID(ctx context.Context, chatID string) (ChatUser, error)
	LookupUserByEmail(ctx context.Context, email string) (ChatUser, error)
	LookupChannelByName(ctx context.Context, name string) (string, error)

	SendNotification(ctx context.Context, chatID, message string) (MessageHandle, error)
	SendPersonalReport(ctx context.Context, chatID, title string, reportItems []string) (MessageHandle, error)
	SendChannelNotification(ctx context.Context, chanID, message string) (MessageHandle, error)
	SendChannelReport(ctx context.Context, chanID, title string, reportItems []string) (MessageHandle, error)
}

type ChatSystemFormatter interface {
	FormatBold(msg string) string
	FormatItalic(msg string) string
	FormatBlockQuote(msg string) string
	FormatChangeLink(project string, number int, url, subject string) string
	FormatChannelLink(channelID string) string
	FormatUserLink(chatID string) string
	FormatLink(url, text string) string
	UnwrapUserLink(userLink string) string
	UnwrapChannelLink(channelLink string) string
	UnwrapLink(link string) string
}

// MessageHandle represents a sent message. Some chat systems support editing and deletion of
// sent messages, and we may take advantage of that at some point (but this is mostly ignored for
// now).
type MessageHandle interface {
	SentTime() time.Time
}

// ChatUser represents profile and presence information about a user on ChatSytem.
type ChatUser interface {
	ChatID() string
	RealName() string
	IsOnline() bool
	Timezone() *time.Location
}

// GerritClient abstracts interaction with the Gerrit client, largely just so that it can be
// possible to mock. Ew.
type GerritClient interface {
	QueryChangesEx(context.Context, []string, *gerrit.QueryChangesOpts) ([]gerrit.ChangeInfo, bool, error)
	GetChangeEx(context.Context, string, *gerrit.QueryChangesOpts) (gerrit.ChangeInfo, error)
	ListRevisionComments(context.Context, string, string) (map[string][]gerrit.CommentInfo, error)
	GetPatchSetInfo(context.Context, string, string) (gerrit.ChangeInfo, error)
	GetChangeReviewers(context.Context, string) ([]gerrit.ReviewerInfo, error)
	QueryAccountsEx(context.Context, string, *gerrit.QueryAccountsOpts) ([]gerrit.AccountInfo, bool, error)
	URLForChange(*gerrit.ChangeInfo) string
	Close() error
}

type gerritConnector interface {
	OpenGerrit(context.Context, string) (GerritClient, error)
}

type configItemType int

const (
	ConfigItemString = configItemType(iota)
	ConfigItemInt
	ConfigItemChannel
	ConfigItemUserList
	ConfigItemLink
)

type configItem struct {
	Name        string
	Description string
	ItemType    configItemType
}

var configItems = []configItem{
	{Name: "gerrit-address", Description: "URL to your Gerrit server (e.g. `https://gerrit-review.googlesource.com/`)", ItemType: ConfigItemLink},
	{Name: "admin-ids", Description: "comma-separated list of chat IDs of admins", ItemType: ConfigItemUserList},
	{Name: "remove-project-prefix", Description: "a common prefix on project names which can be removed if present before displaying in links (e.g., `myCompany/`)", ItemType: ConfigItemString},
	{Name: "global-notify-channel", Description: "A channel to which all generally-applicable notifications should be sent", ItemType: ConfigItemChannel},
	{Name: "global-report-channel", Description: "A channel to which a daily report will be sent, noting which change sets have been waiting too long and who they're waiting for", ItemType: ConfigItemChannel},
	{Name: "global-report-hour", Description: "Hour (in 24-hour time) in UTC when the daily report will be sent to global-report-channel", ItemType: ConfigItemInt},
	{Name: "personal-report-hour", Description: "Hour (in 24-hour time) in each user's local timezone when the daily review report should be sent, if they are online. If they are not online, the system will keep checking every hour during working hours.", ItemType: ConfigItemInt},
	{Name: "work-needed-query", Description: "Gerrit query to use for determining change sets with work needed for the global report", ItemType: ConfigItemString},
}

type App struct {
	logger       *zap.Logger
	chat         ChatSystem
	fmt          ChatSystemFormatter
	persistentDB *PersistentDB

	gerritConnector gerritConnector
	gerritLock      sync.Mutex
	gerritHandle    GerritClient

	reporterLock sync.Mutex

	// a send is done on this channel when PeriodicGlobalReport may need to reread config
	reconfigureChannel chan struct{}

	// A common prefix on project names which can be removed if present before displaying in
	// links; configure by way of remove-project-prefix config item
	removeProjectPrefix string
}

func New(ctx context.Context, logger *zap.Logger, chat ChatSystem, chatFormatter ChatSystemFormatter, persistentDB *PersistentDB, gerritConnector gerritConnector) *App {
	app := &App{
		logger:             logger,
		chat:               chat,
		fmt:                chatFormatter,
		persistentDB:       persistentDB,
		gerritConnector:    gerritConnector,
		reconfigureChannel: make(chan struct{}, 1),
	}
	logger.Info("team starting up")
	chat.SetIncomingMessageCallback(app.IncomingChatCommand)

	if err := app.initializeAdmins(ctx); err != nil {
		logger.Error("failed to initialize admin list", zap.Error(err))
	}
	if err := app.initializeGerrit(ctx); err != nil {
		logger.Error("failed to initialize Gerrit configuration", zap.Error(err))
	}
	app.removeProjectPrefix = persistentDB.JustGetConfig(ctx, "remove-project-prefix", "")
	return app
}

func (a *App) initializeAdmins(ctx context.Context) error {
	admins, err := a.persistentDB.GetConfig(ctx, "admin-ids", "")
	if err != nil {
		return errs.New("getting configured admin list from config db: %w", err)
	}
	if admins != "" {
		return nil
	}
	originalAdmin, err := a.chat.GetInstallingUser(ctx)
	if err != nil {
		return errs.New("getting installing user from chat system: %w", err)
	}
	a.logger.Info("setting initial team admin", zap.String("original-admin", originalAdmin))
	if err := a.persistentDB.SetConfig(ctx, "admin-ids", originalAdmin); err != nil {
		return errs.New("storing initial team admin config: %w", err)
	}
	return nil
}

func (a *App) initializeGerrit(ctx context.Context) error {
	gerritAddress, err := a.persistentDB.GetConfig(ctx, "gerrit-address", "")
	if err != nil {
		return errs.New("check for configured gerrit-address: %w", err)
	}
	if gerritAddress == "" {
		a.logger.Info("no gerrit-address configured yet")
		return nil
	}
	if err := a.ConfigureGerritServer(ctx, gerritAddress); err != nil {
		return errs.New("opening gerrit-address %q: %w", gerritAddress, err)
	}
	return nil
}

// waitGroup is a more errgroup.Group-like interface to sync.WaitGroup
type waitGroup struct {
	sync.WaitGroup
}

func (w *waitGroup) Go(f func()) {
	w.WaitGroup.Add(1)
	go func() {
		defer w.WaitGroup.Done()
		f()
	}()
}

const chatCommandHandlingTimeout = time.Minute * 10

func (a *App) Close() error {
	err := a.persistentDB.Close()
	a.gerritLock.Lock()
	if a.gerritHandle != nil {
		err = errs.Combine(err, a.gerritHandle.Close())
		a.gerritHandle = nil
	}
	a.gerritLock.Unlock()
	return err
}

func (a *App) getGerritClient() GerritClient {
	a.gerritLock.Lock()
	defer a.gerritLock.Unlock()
	return a.gerritHandle
}

func (a *App) GerritEvent(ctx context.Context, event events.GerritEvent) {
	if a.getGerritClient() == nil {
		a.logger.Info("dropping event, no gerrit client", zap.String("event-type", event.GetType()))
	}
	switch ev := event.(type) {
	case *events.CommentAddedEvent:
		a.CommentAdded(ctx, ev.Author, ev.Change, ev.PatchSet, ev.Comment, ev.EventCreatedAt())
	case *events.ReviewerAddedEvent:
		a.ReviewerAdded(ctx, ev.Reviewer, ev.Change, ev.EventCreatedAt())
	case *events.PatchSetCreatedEvent:
		a.PatchSetCreated(ctx, ev.Uploader, ev.Change, ev.PatchSet)
	case *events.ChangeAbandonedEvent:
		a.ChangeAbandoned(ctx, ev.Abandoner, ev.Change, ev.Reason)
	case *events.ChangeRestoredEvent:
		a.ChangeRestored(ctx, ev.Restorer, ev.Change, ev.PatchSet, ev.Reason)
	case *events.ChangeMergedEvent:
		a.ChangeMerged(ctx, ev.Submitter, ev.Change, ev.PatchSet)
	case *events.AssigneeChangedEvent:
		a.AssigneeChanged(ctx, ev.Changer, ev.Change, ev.OldAssignee)
	case *events.TopicChangedEvent:
		a.TopicChanged(ctx, ev.Changer, ev.Change)
	case *events.WipStateChangedEvent:
		a.WipStateChanged(ctx, ev.Changer, ev.Change, ev.PatchSet)
	case *events.DroppedOutputEvent:
		a.logger.Warn("gerrit reports dropped events")
	case *events.RefUpdatedEvent:
	case *events.ReviewerDeletedEvent:
	default:
		a.logger.Error("unknown event type", zap.String("event-type", event.GetType()))
	}
}

// IncomingChatCommand gets called by the ChatSystem when the bot sees a chat message. This might
// be a message in a channel that the bot is in, or it might be a direct message (isDM). If this
// returns a non-empty string, it will be sent to the original channel (or DM session) as a reply.
func (a *App) IncomingChatCommand(userID, chanID string, isDM bool, text string) (reply string) {
	if !isDM {
		return ""
	}
	if !strings.HasPrefix(text, "!") {
		return "" // only pay attention to !-prefixed messages, for now
	}
	logger := a.logger.With(zap.String("user-id", userID), zap.String("chan-id", chanID), zap.String("text", text))
	logger.Debug("got chat command")
	ctx, cancel := context.WithTimeout(context.Background(), chatCommandHandlingTimeout)
	defer cancel()

	if !a.isAdminUser(ctx, userID) {
		return "" // only pay attention to messages from admin, for now
	}

	parts := strings.Fields(text)
	command := parts[0]
	switch command {
	case "!personal-reports":
		logger.Info("admin requested unscheduled personal review reports")
		a.PersonalReports(ctx, time.Now())
	case "!global-report":
		logger.Info("admin requested unscheduled global report")
		chanID := a.persistentDB.JustGetConfig(ctx, "global-report-channel", "")
		if chanID == "" {
			return "No global report channel configured."
		}
		a.GlobalReport(ctx, time.Now(), chanID)
	case "!assoc":
		gerritUsername := ""
		chatID := ""
		if len(parts) == 3 {
			gerritUsername = parts[1]
			chatID = a.fmt.UnwrapUserLink(parts[2])
		}
		if chatID == "" {
			logger.Error("bad !assoc usage")
			return "bad !assoc usage (`!assoc <gerritUsername> <chatUser>`)"
		}
		logger = logger.With(zap.String("gerrit-username", gerritUsername), zap.String("chat-id", chatID))
		if err := a.persistentDB.AssociateChatIDWithGerritUser(ctx, gerritUsername, chatID); err != nil {
			logger.Error("failed to create manual association", zap.Error(err))
			return "failed to create association"
		}
		logger.Info("admin created manual association")
		return fmt.Sprintf("Ok, %s = %s", gerritUsername, a.fmt.FormatUserLink(chatID))
	case "!config":
		if len(parts) < 2 {
			return "bad !config usage (`!config <key> [<value>]`)"
		}
		key := parts[1]
		if len(parts) == 2 {
			curValue, err := a.getConfigItem(ctx, key)
			if err != nil {
				return fmt.Sprintf("failed to look up config value for %q: %v", key, err)
			}
			return fmt.Sprintf("%q => %q", key, curValue)
		}
		value := strings.Join(parts[2:], " ")
		err := a.setConfigItem(ctx, key, value)
		if err != nil {
			return fmt.Sprintf("failed to set %q: %v", key, err)
		}
	default:
		return "I don't understand that command."
	}
	return "Ok"
}

func findConfigItem(key string) *configItem {
	for _, configDef := range configItems {
		if configDef.Name == key {
			return &configDef
		}
	}
	return nil
}

func (a *App) getConfigItem(ctx context.Context, key string) (string, error) {
	curValue, err := a.persistentDB.GetConfig(ctx, key, "")
	if err != nil {
		a.logger.Error("failed to look up config", zap.Error(err))
		return "", errs.New("db error")
	}
	configDef := findConfigItem(key)
	if configDef == nil {
		return "", errs.New("%q is not a known config item.", key)
	}
	switch configDef.ItemType {
	case ConfigItemChannel:
		curValue = a.prepareChannelLink(ctx, "", curValue)
	case ConfigItemUserList:
		userIDs := strings.Split(curValue, ",")
		userLinks := make([]string, 0, len(userIDs))
		for _, userID := range userIDs {
			if userID == "" {
				continue
			}
			userLinks = append(userLinks, a.formatUserLink(nil, userID))
		}
		curValue = strings.Join(userLinks, ",")
	}
	return curValue, nil
}

func (a *App) setConfigItem(ctx context.Context, key, value string) error {
	configDef := findConfigItem(key)
	if configDef == nil {
		return errs.New("%q is not a known config item.", key)
	}
	switch configDef.ItemType {
	case ConfigItemInt:
		// just ensure it's valid; we'll still store it as a str
		_, err := strconv.ParseInt(value, 0, 32)
		if err != nil {
			return errs.New("%q is an invalid numeric value", value)
		}
	case ConfigItemChannel:
		value = a.fmt.UnwrapChannelLink(value)
		if value == "" {
			return errs.New("specify channels by letting the chat client link them")
		}
	case ConfigItemLink:
		value = a.fmt.UnwrapLink(value)
	case ConfigItemUserList:
		action := "replace"
		if strings.HasPrefix(value, "+") {
			action = "append"
			value = value[1:]
		}
		if strings.HasPrefix(value, "-") {
			action = "remove"
			value = value[1:]
		}
		fields := strings.Split(value, ",")
		userIDs := make([]string, 0, len(fields))
		for _, field := range fields {
			field = strings.TrimSpace(field)
			if field == "" {
				continue
			}
			userID := a.fmt.UnwrapUserLink(field)
			if userID == "" {
				return errs.New("specify users by letting the chat client link them")
			}
			userIDs = append(userIDs, userID)
		}
		oldValue := a.persistentDB.JustGetConfig(ctx, key, "")
		value = marshalUserSet(transformUserSet(parseUserSet(oldValue), userIDs, action))
	}
	err := a.persistentDB.SetConfig(ctx, key, value)
	if err != nil {
		a.logger.Error("failed to set config", zap.String("key", key), zap.String("value", value), zap.Error(err))
		return errs.New("db error")
	}

	// a few special config items need particular handling
	switch key {
	case "remove-project-prefix":
		a.removeProjectPrefix = value
	case "global-report-hour":
		a.reconfigureChannel <- struct{}{}
	case "gerrit-server":
		if err := a.ConfigureGerritServer(ctx, value); err != nil {
			a.logger.Error("failed to open new gerrit client", zap.Error(err))
			// but don't pass this error back to caller, as the config setting was successful
		}
	}

	return nil
}

func (a *App) ConfigureGerritServer(ctx context.Context, gerritAddress string) error {
	a.gerritLock.Lock()
	defer a.gerritLock.Unlock()

	a.logger.Info("updating gerrit-server", zap.String("new address", gerritAddress))
	if a.gerritHandle != nil {
		if err := a.gerritHandle.Close(); err != nil {
			a.logger.Error("failed to close old gerritHandle", zap.Error(err))
		}
	}
	gerritClient, err := a.gerritConnector.OpenGerrit(ctx, gerritAddress)
	if err != nil {
		return err
	}
	a.gerritHandle = gerritClient
	return nil
}

func (a *App) isAdminUser(ctx context.Context, chatID string) bool {
	admins := a.persistentDB.JustGetConfig(ctx, "admin-ids", "")
	adminSet := parseUserSet(admins)
	_, ok := adminSet[chatID]
	return ok
}

// getNewInlineComments fetches _all_ inline comments for the specified change and patchset
// (because apparently that's the only way we can do it), and puts them all in the allInline map.
// It then identifies those inline comments which were posted by the given gerrit user and after
// the specified time, and consults the db to see which, if any, have not already been seen and
// reported. The remainder are returned as newInline (which maps comment IDs to their timestamps).
func (a *App) getNewInlineComments(ctx context.Context, changeID, patchSetID, username string, cutOffTime time.Time) (allInline map[string]*gerrit.CommentInfo, newInline map[string]time.Time, err error) {
	allComments, err := a.getGerritClient().ListRevisionComments(ctx, changeID, patchSetID)
	if err != nil {
		return nil, nil, err
	}
	allInline = make(map[string]*gerrit.CommentInfo)
	newInline = make(map[string]time.Time)
	for filePath, fileComments := range allComments {
		for _, commentInfo := range fileComments {
			commentInfoCopy := commentInfo
			commentInfoCopy.Path = filePath
			allInline[commentInfo.ID] = &commentInfoCopy
			if commentInfo.Author.Username == username {
				updatedTime := gerrit.ParseTimestamp(commentInfo.Updated)
				if updatedTime.After(cutOffTime) {
					newInline[commentInfo.ID] = updatedTime
				}
			}
		}
	}
	if err := a.persistentDB.IdentifyNewInlineComments(ctx, newInline); err != nil {
		return nil, nil, err
	}
	return allInline, newInline, nil
}

// findReviewMessageID attempts to find the message ID for a particular review, given the change
// ID, revision number, author, content, and event time.
func (a *App) findReviewMessageID(ctx context.Context, changeID string, patchSetNumber int, authorUsername string, content string, eventTime time.Time) string {
	// try to get a link to the toplevel comment and its inline comments
	reviewInfo, err := a.getGerritClient().GetChangeEx(ctx, changeID, &gerrit.QueryChangesOpts{
		DescribeDetailedAccounts: true,
		DescribeMessages:         true,
	})
	if err != nil {
		a.logger.Error("could not query change via API", zap.Error(err),
			zap.String("change-id", changeID))
		// fallback: don't link to toplevel comment
		return ""
	}
	thisMessage, err := a.pickBestMatchMessage(patchSetNumber, authorUsername, eventTime, content, reviewInfo.Messages)
	if err != nil {
		a.logger.Error("could not identify toplevel message from clues in gerrit event", zap.Error(err), zap.String("change-id", changeID), zap.Int("patch-set", patchSetNumber), zap.String("author", authorUsername), zap.String("content", content), zap.Int("messages-found", len(reviewInfo.Messages)))
		// fallback: don't link to toplevel comment
		return ""
	}
	return thisMessage.ID
}

// findReviewerUpdateRecord attempts to use the Gerrit API to identify the reviewer update record
// closest to the time of the specified event that adds the specified user as a reviewer to the
// specified change. This isn't 100% guaranteed accurate, but should be sufficient.
//
// *HEURISTICS
func (a *App) findReviewerUpdateRecord(ctx context.Context, changeID, reviewerUsername string, eventTime time.Time) (gerrit.ReviewerUpdateInfo, error) {
	changeInfo, err := a.getGerritClient().GetChangeEx(ctx, changeID, &gerrit.QueryChangesOpts{
		DescribeDetailedAccounts: true,
		DescribeReviewerUpdates:  true,
	})
	if err != nil {
		return gerrit.ReviewerUpdateInfo{}, err
	}

	var closest gerrit.ReviewerUpdateInfo
	const maxTimeDiff = time.Hour
	closestTimeDiff := maxTimeDiff
	for _, rui := range changeInfo.ReviewerUpdates {
		if (rui.State == "REVIEWER" || rui.State == "CC") && rui.Reviewer.Username == reviewerUsername {
			timeDiff := absDuration(eventTime.Sub(gerrit.ParseTimestamp(rui.Updated)))
			if timeDiff < closestTimeDiff {
				closest = rui
				closestTimeDiff = timeDiff
			}
		}
	}
	if closestTimeDiff < maxTimeDiff {
		return closest, nil
	}
	return gerrit.ReviewerUpdateInfo{}, fmt.Errorf("no match found out of %d records", len(changeInfo.ReviewerUpdates))
}

// pickBestMatchMessage identifies which ChangeMessageInfo from the messages on a changeset is most
// likely the one identified by the given revision number, author, content, and event time. There
// is apparently no way to identify it exactly given the information we get from a gerrit event,
// and the time recorded for this message may not be exactly the same as the time for the received
// event, so finding the closest one is the best we can do.
//
// *HEURISTICS
func (a *App) pickBestMatchMessage(revisionNum int, authorUsername string, sentTime time.Time, topMessage string, messages []gerrit.ChangeMessageInfo) (*gerrit.ChangeMessageInfo, error) {
	var matches []*gerrit.ChangeMessageInfo
	for i, message := range messages {
		if message.RevisionNumber == revisionNum && message.Author.Username == authorUsername && message.Message == topMessage {
			matches = append(matches, &messages[i])
		}
	}
	if len(matches) == 0 {
		return nil, errs.New("no possible matches to choose from")
	}
	// if this finds multiple matches, find the closest in time
	closest := matches[0]
	closestTime := gerrit.ParseTimestamp(closest.Date)
	for _, message := range matches[1:] {
		messageTime := gerrit.ParseTimestamp(message.Date)
		if timeCloserTo(sentTime, messageTime, closestTime) {
			closest = message
			closestTime = messageTime
		}
	}
	return closest, nil
}

var (
	commentsXCommentsRegex = regexp.MustCompile(`(^|\n\n)\([0-9]+ comments?\)\s*$`)
	commentsPatchSetRegex  = regexp.MustCompile(`^\s*Patch Set [0-9]+:\s*`)
)

// CommentAdded is called when we receive a Gerrit comment-added event.
//
// Because we are dealing with software, and software is terrible, the events that come from the
// Gerrit webhooks plugin do not contain any info about inline comments. We have to get the info
// from the API.
//
// Worse, there is apparently _no_ reliable way to identify which inline comments are associated
// with this "comment-added" event. We could fudge it by using the timestamps, but we'd be bound
// to get too many inline comments or too few in certain cases, depending on our implementation.
// Instead of opening that pandora's box of confusion, we will try to keep track of which inline
// comments we have seen before. Luckily, they do all have unique identifiers.
//
// Notification rules:
//
// - If change owner has posted a top-level comment, notify all the reviewers.
// - If someone else has posted a top-level comment, notify change owner.
// - For all new inline comments, notify the change owner and all prior thread participants (except
//   for the commenter).
func (a *App) CommentAdded(ctx context.Context, author events.Account, change events.Change, patchSet events.PatchSet, comment string, eventTime time.Time) {
	owner := &change.Owner
	commenterLink := a.prepareUserLink(ctx, &author)
	changeLink := a.formatChangeLink(&change)

	var topCommentLink string
	topCommentID := a.findReviewMessageID(ctx, change.BestID(), patchSet.Number, author.Username, comment, eventTime)
	if topCommentID != "" {
		topCommentLink = fmt.Sprintf("%s#message-%s", changeLink, topCommentID)
	}

	// strip off the "Patch Set X:"	bit at the top; we'll convey that piece of info separately.
	comment = commentsPatchSetRegex.ReplaceAllString(comment, "")
	// and strip off the "(X comments)" bit at the bottom; we'll send the comments verbatim.
	comment = commentsXCommentsRegex.ReplaceAllString(comment, "")
	comment = strings.TrimSpace(comment)
	// and if it's still longer than a single line, prepend a newline so it's easier to read
	isMultiline := false
	if strings.Contains(comment, "\n") {
		isMultiline = true
		comment = "\n" + a.fmt.FormatBlockQuote(comment)
	}

	// get all inline comments and identify which ones are new
	allInline, newInline, err := a.getNewInlineComments(ctx, change.BestID(), strconv.Itoa(patchSet.Number), author.Username, eventTime.Add(-*inlineCommentMaxAge))
	if err != nil {
		a.logger.Error("could not query inline comments via API", zap.Error(err), zap.String("change-id", change.BestID()), zap.Int("patch-set", patchSet.Number))
		// fallback: use empty maps; assume there just aren't any inline comments to deal with
	}

	var tellChangeOwner string
	var tellNotifyChannel string
	var tellReviewers string

	var withComments string
	if len(newInline) > 0 {
		if isMultiline {
			withComments = "\n"
		} else {
			withComments = " "
		}
		plural := ""
		if len(newInline) != 1 {
			plural = "s"
		}
		withComments += a.fmt.FormatItalic("(with " + a.maybeLink(topCommentLink, fmt.Sprintf("%d inline comment%s", len(newInline), plural)) + ")")
	}

	tellNotifyChannel = fmt.Sprintf("%s %s %s patchset %d: %s%s", author.Name, a.maybeLink(topCommentLink, "commented on"), changeLink, patchSet.Number, comment, withComments)

	msg := fmt.Sprintf("%s %s %s patchset %d: %s", commenterLink, a.maybeLink(topCommentLink, "commented on"), changeLink, patchSet.Number, comment)
	if owner.Username != author.Username {
		tellChangeOwner = msg
	} else if comment != "" {
		// Only pass comment on to reviewers when the comment is non-empty and was authored
		// by the change owner. Otherwise, the comment is likely intended primarily for the
		// owner.
		tellReviewers = msg + withComments
	}

	threadParticipants := make(map[events.Account][]string)
	newInlineCommentsSorted := sortInlineComments(allInline, newInline)
	for _, commentInfo := range newInlineCommentsSorted {
		message := commentInfo.Message
		if strings.Contains(message, "\n") {
			message = "\n" + a.fmt.FormatBlockQuote(message)
		}
		sourceLink := a.formatSourceLink(change.URL, patchSet.Number, commentInfo)
		// add to notification message for change owner
		if owner.Username != author.Username {
			tellChangeOwner += fmt.Sprintf("\n%s: %s", sourceLink, message)
		}
		// notify prior thread participants
		inlineNotified := map[string]struct{}{author.Username: {}, owner.Username: {}}
		priorComment, priorOk := allInline[commentInfo.InReplyTo]
		for priorOk {
			if _, alreadyNotified := inlineNotified[priorComment.Author.Username]; !alreadyNotified {
				inlineNotified[priorComment.Author.Username] = struct{}{}
				msg := fmt.Sprintf("%s replied to a thread on %s: %s: %s", commenterLink, changeLink, sourceLink, message)
				priorCommentAuthor := accountFromAccountInfo(&priorComment.Author)
				threadParticipants[*priorCommentAuthor] = append(threadParticipants[*priorCommentAuthor], msg)
			}
			priorComment, priorOk = allInline[priorComment.InReplyTo]
		}
	}

	var wg waitGroup
	defer wg.Wait()

	if tellChangeOwner != "" {
		wg.Go(func() {
			a.notify(ctx, owner, tellChangeOwner)
		})
	}
	wg.Go(func() {
		a.generalNotify(ctx, tellNotifyChannel)
	})
	wg.Go(func() {
		if tellReviewers != "" {
			a.notifyAllReviewers(ctx, change.BestID(), tellReviewers, []string{author.Username, change.Owner.Username})
		}
		// notify thread participants after reviewers; since thread participants are likely
		// reviewers themselves, these thread updates will make most sense in the context
		// of the above notification.
		for destAccount, messages := range threadParticipants {
			combinedMsg := strings.Join(messages, "\n")
			wg.Go(func() {
				a.notify(ctx, &destAccount, combinedMsg)
			})
		}
	})
}

type byPathLineAndTime []*gerrit.CommentInfo

func (b byPathLineAndTime) Len() int      { return len(b) }
func (b byPathLineAndTime) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byPathLineAndTime) Less(i, j int) bool {
	if b[i].Path < b[j].Path {
		return true
	}
	if b[i].Path > b[j].Path {
		return false
	}
	if b[i].Line < b[j].Line {
		return true
	}
	if b[i].Line > b[j].Line {
		return false
	}
	return b[i].Updated < b[j].Updated
}

func sortInlineComments(allComments map[string]*gerrit.CommentInfo, newComments map[string]time.Time) []*gerrit.CommentInfo {
	sliceToSort := make([]*gerrit.CommentInfo, 0, len(newComments))
	for commentID := range newComments {
		sliceToSort = append(sliceToSort, allComments[commentID])
	}
	sort.Sort(byPathLineAndTime(sliceToSort))
	return sliceToSort
}

type byLastUpdateTime []gerrit.ChangeInfo

func (b byLastUpdateTime) Len() int      { return len(b) }
func (b byLastUpdateTime) Swap(i, j int) { b[i], b[j] = b[j], b[i] }
func (b byLastUpdateTime) Less(i, j int) bool {
	// these timestamps are strings, but they are rfc-3339 timestamps and are thus
	// sortable without parsing.
	return b[i].Updated < b[j].Updated
}

// ReviewerAdded is called when we receive a Gerrit reviewer-added event.
func (a *App) ReviewerAdded(ctx context.Context, reviewer events.Account, change events.Change, eventTime time.Time) {
	changeLink := a.formatChangeLink(&change)

	// We want to determine _who_ added the reviewer. If it was the reviewer themself, we
	// don't need to notify. and if it wasn't, we want to say who did it in the notification.
	// Unfortunately, the reviewer-added event doesn't carry that information. We turn to the
	// Gerrit API again.
	reviewerUpdate, err := a.findReviewerUpdateRecord(ctx, change.BestID(), reviewer.Username, eventTime)

	if err != nil {
		// the records in Gerrit are alarmingly different from what the event told us. oh well?
		a.logger.Error("could not identify reviewer update entity via API", zap.Error(err), zap.String("change-id", change.BestID()), zap.String("reviewer", reviewer.Username), zap.Time("event-time", eventTime))
		a.notify(ctx, &reviewer, fmt.Sprintf("You were added as a reviewer or CC for %s", changeLink))
		a.generalNotify(ctx, fmt.Sprintf("%s was added as a reviewer or CC for %s", reviewer.Name, changeLink))
		return
	}

	// now, do appropriate notifications based on the results
	updater := accountFromAccountInfo(&reviewerUpdate.UpdatedBy)
	updaterLink := a.prepareUserLink(ctx, updater)
	what := "as a reviewer"
	if reviewerUpdate.State == "CC" {
		what = "to be CC'd"
	}
	if updater.Username == reviewer.Username {
		// the reviewer added themself.
		if reviewer.Username != change.Owner.Username {
			// notify the owner
			a.notify(ctx, &change.Owner, fmt.Sprintf("%s signed up %s on your change %s",
				updaterLink, what, changeLink))
		}
		a.generalNotify(ctx, fmt.Sprintf("%s signed up %s on change %s", updater.Name, what, changeLink))
	} else {
		// the updater added someone else as a reviewer
		a.notify(ctx, &reviewer, fmt.Sprintf("%s added you %s on %s",
			updaterLink, what, changeLink))
		// if the owner is the same as the reviewer _or_ the updater, we don't need to notify them also
		if change.Owner.Username != reviewer.Username && change.Owner.Username != updater.Username {
			a.notify(ctx, &change.Owner, fmt.Sprintf("%s added %s %s on your change %s",
				updaterLink, a.prepareUserLink(ctx, &reviewer), what, changeLink))
		}
		a.generalNotify(ctx, fmt.Sprintf("%s added %s %s on change %s", updater.Name, reviewer.Name, what, changeLink))
	}
}

// PatchSetCreated is called when we receive a Gerrit patchset-created event.
func (a *App) PatchSetCreated(ctx context.Context, uploader events.Account, change events.Change, patchSet events.PatchSet) {
	var wg waitGroup
	defer wg.Wait()

	uploaderLink := a.prepareUserLink(ctx, &uploader)
	changeLink := a.formatChangeLink(&change)

	if uploader.Username != change.Owner.Username {
		wg.Go(func() {
			a.notify(ctx, &change.Owner,
				fmt.Sprintf("%s uploaded a new patchset #%d on your change %s",
					uploaderLink, patchSet.Number, changeLink))
		})
	}

	// events.Change does have an AllReviewers field, but apparently it's not populated for
	// patchset-created events. It's API time
	changeInfo, err := a.getGerritClient().GetPatchSetInfo(ctx, change.BestID(), strconv.Itoa(patchSet.Number))
	if err != nil {
		a.logger.Error("could not fetch patchset info for change, so can not notify reviewers",
			zap.Error(err), zap.String("change-id", change.BestID()))
		// changeInfo will be an empty gerrit.ChangeInfo record, which should work below,
		// using REWORK as a default change type and with an empty reviewers map. Continue
		// for the sake of the general notification.
	}

	var reviewerMsg, ccMsg, generalMsg string
	if patchSet.Number == 1 {
		// this is a whole new changeset. notify accordingly.
		reviewerMsg = fmt.Sprintf("%s pushed a new changeset %s, with you as a reviewer.",
			uploaderLink, changeLink)
		ccMsg = fmt.Sprintf("%s pushed a new changeset %s, with you CC'd.",
			uploaderLink, changeLink)
		generalMsg = fmt.Sprintf("%s pushed a new changeset %s.",
			uploaderLink, changeLink)
	} else {
		changeType := "REWORK"
		// this map should have exactly one entry or zero entries, but we can't predict the key
		for _, revisionInfo := range changeInfo.Revisions {
			changeType = revisionInfo.Kind
		}
		switch changeType {
		case "TRIVIAL_REBASE":
			generalMsg = fmt.Sprintf("%s rebased change %s into patchset #%d",
				uploader.Name, changeLink, patchSet.Number)
			reviewerMsg = fmt.Sprintf("%s rebased change %s into patchset #%d",
				uploaderLink, changeLink, patchSet.Number)
		case "NO_CHANGE", "NO_CODE_CHANGE":
			generalMsg = fmt.Sprintf("%s created a new patchset #%d on change %s (no code changes)",
				uploader.Name, patchSet.Number, changeLink)
			reviewerMsg = fmt.Sprintf("%s created a new patchset #%d on change %s (no code changes)",
				uploaderLink, patchSet.Number, changeLink)
		default:
			diffURL := diffURLBetweenPatchSets(&change, patchSet.Number-1, patchSet.Number)
			generalMsg = fmt.Sprintf("%s uploaded a new patchset #%d on change %s (%s)",
				uploader.Name, patchSet.Number, changeLink, a.fmt.FormatLink(diffURL, "see changes"))
			reviewerMsg = fmt.Sprintf("%s uploaded a new patchset #%d on change %s (%s)",
				uploaderLink, patchSet.Number, changeLink, a.fmt.FormatLink(diffURL, "see changes"))
		}
		ccMsg = reviewerMsg
	}

	// send the reviewerMsg or ccMsg, as appropriate, to all relevant users
	haveNotified := map[string]struct{}{uploader.Username: {}, change.Owner.Username: {}}
	messages := []string{reviewerMsg, ccMsg}
	for i, reviewerGroup := range []string{"REVIEWER", "CC"} {
		useMsg := messages[i]
		for _, reviewer := range changeInfo.Reviewers[reviewerGroup] {
			if _, alreadyNotified := haveNotified[reviewer.Username]; !alreadyNotified {
				haveNotified[reviewer.Username] = struct{}{}
				reviewerInfo := accountFromAccountInfo(&reviewer)
				wg.Go(func() {
					a.notify(ctx, reviewerInfo, useMsg)
				})
			}
		}
	}

	// and, finally, send the general notification
	wg.Go(func() {
		a.generalNotify(ctx, generalMsg)
	})
}

// ChangeAbandoned is called when we receive a Gerrit change-abandoned event.
func (a *App) ChangeAbandoned(ctx context.Context, abandoner events.Account, change events.Change, reason string) {
	abandonerLink := a.prepareUserLink(ctx, &abandoner)
	changeLink := a.formatChangeLink(&change)
	if abandoner.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, fmt.Sprintf(
			"%s marked your change %s as abandoned with the message: %s",
			abandonerLink, changeLink, reason))
	}
	reviewerMsg := fmt.Sprintf("%s marked change %s as abandoned with the message: %s",
		abandonerLink, changeLink, reason)
	a.notifyAllReviewers(ctx, change.BestID(), reviewerMsg, []string{abandoner.Username, change.Owner.Username})
	generalMsg := fmt.Sprintf("%s marked change %s as abandoned with the message: %s",
		abandoner.Name, changeLink, reason)
	a.generalNotify(ctx, generalMsg)
}

// ChangeRestored is called when we receive a Gerrit change-restored event.
func (a *App) ChangeRestored(ctx context.Context, restorer events.Account, change events.Change, patchSet events.PatchSet, reason string) {
	restorerLink := a.prepareUserLink(ctx, &restorer)
	changeLink := a.formatChangeLink(&change)
	if restorer.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, fmt.Sprintf(
			"%s restored your change %s using patchset #%d with the message: %s",
			restorerLink, changeLink, patchSet.Number, reason))
	}
	reviewerMsg := fmt.Sprintf("%s restored change %s using patchset #%d with the message: %s",
		restorerLink, changeLink, patchSet.Number, reason)
	a.notifyAllReviewers(ctx, change.BestID(), reviewerMsg, []string{restorer.Username, change.Owner.Username})
	generalMsg := fmt.Sprintf("%s restored change %s using patchset #%d with the message: %s",
		restorer.Name, changeLink, patchSet.Number, reason)
	a.generalNotify(ctx, generalMsg)
}

// ChangeMerged is called when we receive a Gerrit change-merged event.
func (a *App) ChangeMerged(ctx context.Context, submitter events.Account, change events.Change, patchSet events.PatchSet) {
	submitterLink := a.prepareUserLink(ctx, &submitter)
	changeLink := a.formatChangeLink(&change)
	if submitter.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, fmt.Sprintf(
			"%s merged patchset #%d of your change %s.",
			submitterLink, patchSet.Number, changeLink))
	}
	reviewerMsg := fmt.Sprintf("%s merged patchset #%d of change %s.",
		submitterLink, patchSet.Number, changeLink)
	a.notifyAllReviewers(ctx, change.BestID(), reviewerMsg, []string{submitter.Username, change.Owner.Username})
	generalMsg := fmt.Sprintf("%s merged patchset #%d of change %s.",
		submitter.Name, patchSet.Number, changeLink)
	a.generalNotify(ctx, generalMsg)
}

// TopicChanged is called when we receive a Gerrit topic-changed event.
func (a *App) TopicChanged(ctx context.Context, changer events.Account, change events.Change) {
	changerLink := a.prepareUserLink(ctx, &changer)
	changeLink := a.formatChangeLink(&change)
	if changer.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, fmt.Sprintf(
			"%s changed the topic of your change %s to %q.",
			changerLink, changeLink, change.Topic))
	}
	reviewerMsg := fmt.Sprintf("%s changed the topic of changeset %s to %q.",
		changerLink, changeLink, change.Topic)
	a.notifyAllReviewers(ctx, change.BestID(), reviewerMsg, []string{changer.Username, change.Owner.Username})
	generalMsg := fmt.Sprintf("%s changed the topic of changeset %s to %q.",
		changer.Name, changeLink, change.Topic)
	a.generalNotify(ctx, generalMsg)
}

// WipStateChanged is called when we receive a Gerrit topic-changed event.
func (a *App) WipStateChanged(ctx context.Context, changer events.Account, change events.Change, _ events.PatchSet) {
	changerLink := a.prepareUserLink(ctx, &changer)
	changeLink := a.formatChangeLink(&change)
	what := "as ready for review"
	if change.WIP {
		what = "as a Work In Progress"
	}
	if changer.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, fmt.Sprintf("%s marked your change %s %s.", changerLink, changeLink, what))
	}
	reviewerMsg := fmt.Sprintf("%s marked change %s %s.", changerLink, changeLink, what)
	a.notifyAllReviewers(ctx, change.BestID(), reviewerMsg, []string{changer.Username, change.Owner.Username})
	generalMsg := fmt.Sprintf("%s marked change %s %s.", changer.Name, changeLink, what)
	a.generalNotify(ctx, generalMsg)
}

// AssigneeChanged is called when we receive a Gerrit assignee-changed event.
func (a *App) AssigneeChanged(_ context.Context, _ events.Account, _ events.Change, _ events.Account) {
	// We don't use the Assignee field for anything right now, I think. Probably good to ignore this
}

func (a *App) notifyAllReviewers(ctx context.Context, changeID, msg string, except []string) {
	reviewers, err := a.getGerritClient().GetChangeReviewers(ctx, changeID)
	if err != nil {
		a.logger.Error("could not query reviewers for change, so can not notify reviewers",
			zap.Error(err), zap.String("change-id", changeID))
		return
	}
	haveNotified := make(map[string]struct{})
	for _, exception := range except {
		haveNotified[exception] = struct{}{}
	}

	var wg waitGroup
	for _, reviewer := range reviewers {
		if _, alreadyNotified := haveNotified[reviewer.Username]; !alreadyNotified {
			haveNotified[reviewer.Username] = struct{}{}
			reviewerInfo := accountFromAccountInfo(&reviewer.AccountInfo)
			wg.Go(func() { a.notify(ctx, reviewerInfo, msg) })
		}
	}
	wg.Wait()
}

func (a *App) notify(ctx context.Context, gerritUser *events.Account, message string) {
	chatID := a.lookupGerritUser(ctx, gerritUser)
	if chatID == "" {
		return
	}
	_, err := a.chat.SendNotification(ctx, chatID, message)
	if err != nil {
		a.logger.Error("failed to send notification",
			zap.Error(err),
			zap.String("chat-id", chatID),
			zap.String("gerrit-username", gerritUser.Username))
	}
}

func (a *App) generalNotify(ctx context.Context, message string) {
	chanID := a.persistentDB.JustGetConfig(ctx, "global-notify-channel", "")
	if chanID == "" {
		// no global notify channel configured.
		return
	}
	_, err := a.chat.SendChannelNotification(ctx, chanID, message)
	if err != nil {
		a.logger.Error("failed to send notification to channel",
			zap.Error(err),
			zap.String("channel-id", chanID),
			zap.String("message", message))
	}
}

func (a *App) lookupGerritUser(ctx context.Context, user *events.Account) string {
	chatID, err := a.persistentDB.LookupChatIDForGerritUser(ctx, user.Username)
	if err == nil {
		return chatID
	}
	if !errors.Is(err, sql.ErrNoRows) {
		a.logger.Error("failed to query persistent db", zap.Error(err))
		return ""
	}
	if user.Email == "" {
		a.logger.Info("user not found in persistent db",
			zap.String("gerrit-username", user.Username))
		return ""
	}
	// fall back to checking for the same email address in the chat system
	chatUserInfo, err := a.chat.LookupUserByEmail(ctx, user.Email)
	if err != nil {
		a.logger.Info("user not found in chat system",
			zap.String("gerrit-username", user.Username),
			zap.String("gerrit-email", user.Email),
			zap.Error(err))
		return ""
	}
	chatID = chatUserInfo.ChatID()
	// success- save this association
	err = a.persistentDB.AssociateChatIDWithGerritUser(ctx, user.Username, chatID)
	if err != nil {
		a.logger.Error("found new user association, but could not update persistent db",
			zap.String("gerrit-username", user.Username),
			zap.String("chat-id", chatID),
			zap.Error(err))
	}
	return chatID
}

func (a *App) PeriodicGlobalReport(ctx context.Context, getTime func() time.Time) error {
	chanID := a.persistentDB.JustGetConfig(ctx, "global-report-channel", "")
	if chanID == "" {
		// no global report configured
		return nil
	}
	timer := time.NewTimer(a.nextGlobalReportTime(ctx, getTime()))

	for {
		select {
		case t := <-timer.C:
			a.GlobalReport(ctx, t, chanID)
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		case <-a.reconfigureChannel:
		}
		timer.Reset(a.nextGlobalReportTime(ctx, getTime()))
	}
}

func (a *App) nextGlobalReportTime(ctx context.Context, now time.Time) time.Duration {
	globalReportHour := a.persistentDB.JustGetConfigInt(ctx, "global-report-hour", defaultGlobalReportHour)
	nextReport := time.Date(now.Year(), now.Month(), now.Day(), globalReportHour, 0, 0, 0, time.UTC)
	if now.After(nextReport) {
		nextReport = nextReport.AddDate(0, 0, 1)
	}
	for nextReport.Weekday() == time.Saturday || nextReport.Weekday() == time.Sunday {
		nextReport = nextReport.AddDate(0, 0, 1)
	}
	return nextReport.Sub(now)
}

func (a *App) PeriodicPersonalReports(ctx context.Context, getTime func() time.Time) error {
	// this is similar to time.Ticker, but should always run close to the top of the UTC hour.
	now := getTime()
	timer := time.NewTimer(now.UTC().Truncate(time.Hour).Add(time.Hour).Sub(now))

	for {
		select {
		case t := <-timer.C:
			a.PersonalReports(ctx, t)
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}

		now = getTime()
		timer.Reset(now.UTC().Truncate(time.Hour).Add(time.Hour).Sub(now))
	}
}

func (a *App) GlobalReport(ctx context.Context, t time.Time, chanID string) {
	defer func() {
		rec := recover()
		if rec != nil {
			a.logger.Error("panic creating global report", zap.Any("panic-message", rec))
		}
	}()

	logger := a.logger.With(zap.Time("global-report-start", t), zap.String("channel-id", chanID))
	logger.Debug("initiating global current changeset report")
	gerritClient := a.getGerritClient()
	if gerritClient == nil {
		logger.Info("skipping report- no gerrit client")
		return
	}

	workNeededQuery := a.persistentDB.JustGetConfig(ctx, "work-needed-query", defaultWorkNeededQuery)
	changes, more, err := gerritClient.QueryChangesEx(ctx, []string{workNeededQuery}, &gerrit.QueryChangesOpts{
		Limit:                    globalReportMaxSize,
		DescribeLabels:           true,
		DescribeDetailedLabels:   true,
		DescribeCurrentRevision:  true,
		DescribeDetailedAccounts: true,
		DescribeMessages:         true,
		DescribeSubmittable:      true,
		DescribeWebLinks:         true,
		DescribeCheck:            true,
	})
	if err != nil {
		logger.Error("failed to query changesets needing attention from Gerrit", zap.Error(err))
		return
	}
	logger.Debug("global report processing change info", zap.Int("num-changes", len(changes)))
	sort.Sort(byLastUpdateTime(changes))

	var messages []string
changeLoop:
	for _, change := range changes {
		// note all users that have voted on this patchset
		usersWithVotes := map[string]struct{}{}
		didntVoteMax := map[string]struct{}{}
		for _, labelInfo := range change.Labels {
			if labelInfo.Optional {
				// voting on an optional label doesn't relieve you of duty
				continue
			}
			for _, vote := range labelInfo.All {
				if vote.Value != nil && *vote.Value > 0 {
					usersWithVotes[vote.Username] = struct{}{}
					if *vote.Value < vote.PermittedVotingRange.Max {
						didntVoteMax[vote.Username] = struct{}{}
					}
				}
			}
		}

		// note all users that have commented on this patchset
		usersWithComments := map[string]struct{}{}
		if currentRevision, ok := change.Revisions[change.CurrentRevision]; ok {
			for _, message := range change.Messages {
				if message.RevisionNumber < int(currentRevision.Number) {
					// only paying attention to comments on this patchset right now
					continue
				}
				if strings.HasPrefix(message.Tag, "autogenerated:") {
					// don't count autogenerated comments like "Uploaded patch set X."
					// or "Build Started"
					continue
				}
				usersWithComments[message.Author.Username] = struct{}{}
			}
		}

		var waitingMsg string
		if change.Submittable {
			// change is already submittable, so we'll just note that
			waitingMsg = a.fmt.FormatBold("submittable")
		} else {
			// determine which reviewers are being waited on (reviewers who haven't
			// voted or commented)
			var waitingOn []string
			for _, reviewer := range change.Reviewers["REVIEWER"] {
				if reviewer.Username == change.Owner.Username {
					continue
				}
				if _, ok := usersWithVotes[reviewer.Username]; ok {
					// reviewer has issued a vote on this patchset; not waiting on them
					continue
				}
				if _, ok := usersWithComments[reviewer.Username]; ok {
					// reviewer has commented on this patchset; not waiting on them
					continue
				}
				waitingOn = append(waitingOn, a.prepareUserLink(ctx, accountFromAccountInfo(&reviewer)))
			}
			if len(waitingOn) == 0 {
				// Not waiting on any reviewers. Need to determine whether this is
				// because there aren't enough reviewers or because they want more
				// changes to be made. Since we can't easily tell how many votes
				// gerrit wants (why doesn't the change.Requirements ever seem to
				// get populated?) for now, the rule will be: if all the reviewers
				// voted the maximum but the change is still not submittable, then
				// more reviewers are needed. Otherwise, let the owner deal with
				// it.
				if len(didntVoteMax) == 0 {
					waitingMsg = "needs more reviewers"
				} else {
					continue changeLoop
				}
			} else {
				waitingMsg = "Waiting on " + strings.Join(waitingOn, ", ")
			}
		}

		// don't make a user link for the owner, so they don't get tagged unnecessarily
		ownerName := a.fmt.FormatItalic("(" + change.Owner.Name + ")")

		var staleness string
		staleTime := t.Sub(gerrit.ParseTimestamp(change.Updated))
		if staleTime < (time.Hour * 24) {
			staleness = "fresh"
		} else {
			staleness = prettyTimeDelta(staleTime) + " stale"
		}

		var age string
		sinceCreated := t.Sub(gerrit.ParseTimestamp(change.Created))
		if sinceCreated < time.Minute {
			age = "newly created"
		} else {
			age = prettyTimeDelta(sinceCreated) + " old"
		}

		messages = append(messages, fmt.Sprintf("%s %s\n%s · %s · %s",
			a.formatChangeInfoLink(&change),
			ownerName,
			a.fmt.FormatItalic(staleness),
			a.fmt.FormatItalic(age),
			waitingMsg))
	}
	if more {
		messages = append(messages, a.fmt.FormatItalic("More change sets awaiting action exist. List is too long!"))
	}

	logger.Debug("global report complete", zap.Int("num-messages", len(messages)))
	if _, err := a.chat.SendChannelReport(ctx, chanID, "Waiting for review", messages); err != nil {
		logger.Error("failed to send global message report", zap.Error(err))
	}
}

func (a *App) PersonalReports(ctx context.Context, t time.Time) {
	// a big dumb hammer to keep multiple calls to this from stepping on each other. this is
	// necessary since we allow calls from places other than PeriodicPersonalReports() for
	// debugging reasons.
	a.reporterLock.Lock()
	defer a.reporterLock.Unlock()

	logger := a.logger.With(zap.Time("personal-reports-start", t))
	logger.Debug("initiating personal current changeset reports")
	gerritClient := a.getGerritClient()
	if gerritClient == nil {
		logger.Info("skipping reports- no gerrit client")
		return
	}

	// get all accounts gerrit knows about
	accounts, _, err := gerritClient.QueryAccountsEx(ctx, "is:active",
		&gerrit.QueryAccountsOpts{DescribeDetails: true})
	if err != nil {
		logger.Error("failed to query current accounts on Gerrit", zap.Error(err))
		return
	}

	// if a user already had a report shown to them since this time, we won't show another
	// report even if it is their reporting time again (e.g. because of a time zone change).
	cutOffTime := t.Add(-*minIntervalBetweenReports)

	for _, acct := range accounts {
		a.PersonalReportToUser(ctx, logger, t, cutOffTime, &acct)
	}
}

// PersonalReportToUser looks up information on the given user, and if it is an appropriate
// time, sends them a report on the changesets currently waiting for their review.
//
// Note this is pretty inefficient with respect to execution time and data transferred.
// Since I don't expect this to be dealing with very large amounts of data, and performance
// is very non-critical here, I would rather let it be a little slow as a simplistic way of
// keeping down the load on the chat and Gerrit servers. If this needs to be snappier,
// though, this is probably a good place to start parallelizing.
func (a *App) PersonalReportToUser(ctx context.Context, logger *zap.Logger, now, cutOffTime time.Time, acct *gerrit.AccountInfo) {
	defer func() {
		rec := recover()
		if rec != nil {
			a.logger.Error("panic creating personal report", zap.String("gerrit-username", acct.Username), zap.Any("panic-message", rec))
		}
	}()

	logger = logger.With(
		zap.String("gerrit-username", acct.Username),
		zap.String("gerrit-email", acct.Email),
		zap.String("gerrit-name", acct.Name))

	// see if we even know who this is in chat
	chatUser, err := a.persistentDB.LookupGerritUser(ctx, acct.Username)
	if err != nil {
		// we don't know who this is. can't send a report.
		logger.Info("No association with user in chat. Skipping.")
		return
	}
	logger = logger.With(zap.String("chat-id", chatUser.ChatId))

	// check last report time
	if chatUser.LastReport != nil && chatUser.LastReport.After(cutOffTime) {
		logger.Info("Skipping report for user due to recent report",
			zap.Time("last-report", *chatUser.LastReport))
		return
	}

	// get updated user info from chat system
	chatInfo, err := a.chat.GetUserInfoByID(ctx, chatUser.ChatId)
	if err != nil {
		logger.Error("Chat system failed to look up user! Possibly deleted?",
			zap.Error(err))
		return
	}
	tz := chatInfo.Timezone()
	logger = logger.With(
		zap.String("real-name", chatInfo.RealName()),
		zap.String("tz", tz.String()))

	// check if it's an ok time to send this user their review report
	if !chatInfo.IsOnline() {
		logger.Info("User not online. Skipping.")
		return
	}
	now = now.In(tz)
	if !a.isGoodTimeForReport(ctx, acct, chatInfo, now) {
		logger.Info("Not a good time for a report. Skipping.",
			zap.Time("user-localtime", now))
		return
	}

	// actually get their reviews according to Gerrit
	changes, err := a.allPendingReviewsFor(ctx, acct.Username)
	if err != nil {
		logger.Error("failed to query Gerrit for pending reviews", zap.Error(err))
		return
	}

	// If we made it this far, record the report in the db. it might still fail,
	// but it's not absolutely critical that all reports reach the user.
	if err := a.persistentDB.UpdateLastReportTime(ctx, acct.Username, now); err != nil {
		logger.Error("could not update last-report time in persistent db", zap.Error(err))
		return
	}

	if len(changes) == 0 {
		logger.Info("No reviews assigned. No report needed.")
		return
	}

	// build the report
	reportItems := make([]string, 0, len(changes))
	for _, ch := range changes {
		createdOn := gerrit.ParseTimestamp(ch.Created)
		lastUpdated := gerrit.ParseTimestamp(ch.Updated)
		reviewItem := fmt.Sprintf("%s\nRequested %s · Updated %s",
			a.formatChangeInfoLink(&ch),
			prettyDate(now, createdOn),
			prettyDate(now, lastUpdated))
		reportItems = append(reportItems, reviewItem)
	}
	logger = logger.With(zap.Int("review-items-pending", len(changes)))

	// and finally, send it out
	_, err = a.chat.SendPersonalReport(ctx, chatUser.ChatId, "Reviews assigned to you", reportItems)
	if err != nil {
		logger.Error("failed to send report to chat")
		return
	}

	logger.Info("successfully sent report")
}

func (a *App) formatChangeLink(ch *events.Change) string {
	return a.fmt.FormatChangeLink(a.shortenProjectName(ch.Project), ch.Number, ch.URL, ch.Subject)
}

func (a *App) formatChangeInfoLink(ch *gerrit.ChangeInfo) string {
	return a.fmt.FormatChangeLink(a.shortenProjectName(ch.Project), ch.Number, a.getGerritClient().URLForChange(ch), ch.Subject)
}

func (a *App) prepareUserLink(ctx context.Context, account *events.Account) string {
	chatID := a.lookupGerritUser(ctx, account)
	return a.formatUserLink(account, chatID)
}

func (a *App) formatUserLink(account *events.Account, chatID string) string {
	if chatID != "" {
		return a.fmt.FormatUserLink(chatID)
	}
	// if we don't have a chat ID for them, do our best from the Gerrit account info
	if account.Name != "" {
		return account.Name
	}
	return account.Username
}

func (a *App) prepareChannelLink(ctx context.Context, channelName, channelID string) string {
	if channelID == "" {
		var err error
		channelID, err = a.chat.LookupChannelByName(ctx, channelName)
		if err != nil {
			return channelName
		}
	}
	return a.fmt.FormatChannelLink(channelID)
}

func (a *App) formatSourceLink(changeURL string, patchSet int, commentInfo *gerrit.CommentInfo) string {
	link := fmt.Sprintf("%s/%d/", changeURL, patchSet)
	var linkText string
	if commentInfo.Path == "" {
		linkText = fmt.Sprintf("patchset %d", patchSet)
	} else {
		link += commentInfo.Path
		linkText = commentInfo.Path
	}
	if commentInfo.Line > 0 {
		lineStr := strconv.Itoa(commentInfo.Line)
		link += "#" + lineStr
		linkText += ":" + lineStr
	}
	return a.fmt.FormatLink(link, linkText)
}

func (a *App) maybeLink(url, linkText string) string {
	if url == "" {
		return linkText
	}
	return a.fmt.FormatLink(url, linkText)
}

func prettyDate(now, then time.Time) string {
	delta := now.Sub(then)
	deltaDescriptor := " ago"

	if delta < 0 {
		deltaDescriptor = " from now"
		delta = -delta
	}
	if delta < time.Second {
		// no sub-second precision here
		return "now"
	}
	return prettyTimeDelta(delta) + deltaDescriptor
}

func prettyTimeDelta(delta time.Duration) string {
	var timeUnits string
	var plural string
	var unitDuration time.Duration
	switch {
	case delta < time.Minute:
		timeUnits = "second"
		unitDuration = time.Second
	case delta < time.Hour:
		timeUnits = "minute"
		unitDuration = time.Minute
	case delta < (time.Hour * 24):
		timeUnits = "hour"
		unitDuration = time.Hour
	default:
		timeUnits = "day"
		unitDuration = time.Hour * 24
	}
	count := delta.Round(unitDuration) / unitDuration
	if count != 1 {
		plural = "s"
	}
	return fmt.Sprintf("%d %s%s", count, timeUnits, plural)
}

func (a *App) isGoodTimeForReport(ctx context.Context, _ *gerrit.AccountInfo, _ ChatUser, t time.Time) bool {
	hour := t.Hour()
	day := t.Weekday()
	if day == time.Saturday || day == time.Sunday {
		return false
	}
	if hour < workingDayStartHour || hour >= workingDayEndHour {
		return false
	}
	personalReportHour := a.persistentDB.JustGetConfigInt(ctx, "personal-report-hour", defaultPersonalReportHour)
	return hour >= personalReportHour
}

func (a *App) allPendingReviewsFor(ctx context.Context, gerritUsername string) ([]gerrit.ChangeInfo, error) {
	queryString := fmt.Sprintf("reviewer:\"%[1]s\" is:open -reviewedby:\"%[1]s\" -owner:\"%[1]s\" -is:wip",
		gerritUsername)
	return a.getAllChangesMatching(ctx, queryString, &gerrit.QueryChangesOpts{
		Limit:                    gerritQueryPageSize,
		DescribeCurrentRevision:  true,
		DescribeDetailedAccounts: true,
	})
}

// See https://gerrit-review.googlesource.com/Documentation/user-search.html for the various
// query operators.
func (a *App) getAllChangesMatching(ctx context.Context, queryString string, opts *gerrit.QueryChangesOpts) (changes []gerrit.ChangeInfo, err error) {
	for {
		opts.StartAt = len(changes)
		changePage, more, err := a.getGerritClient().QueryChangesEx(ctx, []string{queryString}, opts)
		if err != nil {
			return nil, err
		}
		changes = append(changes, changePage...)
		if !more {
			break
		}
	}
	return changes, nil
}

func (a *App) shortenProjectName(projectName string) string {
	if a.removeProjectPrefix != "" && strings.HasPrefix(projectName, a.removeProjectPrefix) {
		projectName = projectName[len(a.removeProjectPrefix):]
	}
	return projectName
}

func diffURLBetweenPatchSets(change *events.Change, patchSetNum1, patchSetNum2 int) string {
	return change.URL + fmt.Sprintf("/%d..%d", patchSetNum1, patchSetNum2)
}

func accountFromAccountInfo(accountInfo *gerrit.AccountInfo) *events.Account {
	return &events.Account{
		Name:     accountInfo.Name,
		Email:    accountInfo.Email,
		Username: accountInfo.Username,
	}
}

// timeCloserTo determines whether t1 is closer than t2 to some reference time t.
func timeCloserTo(t, t1, t2 time.Time) bool {
	return absDuration(t.Sub(t1)) < absDuration(t.Sub(t2))
}

// absDuration returns the absolute value of a duration.
func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
