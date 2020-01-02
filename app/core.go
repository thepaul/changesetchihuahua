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

	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/jtolds/changesetchihuahua/gerrit"
	"github.com/jtolds/changesetchihuahua/gerrit/events"
)

const (
	Version = "0.0.1"
)

var (
	admin = flag.String("admin", "", "Identity of an admin by email address. Will be contacted through the chat system, not email")

	personalReportHour = flag.Int("user-report-hour", 11, "Earliest hour (in 24-hour time) in user's local time when review report should be sent, if they are online. If they are not online, the system will keep checking every hour during working hours.")

	minIntervalBetweenReports = flag.Duration("min-interval-between-reports", time.Hour*10, "Minimum amount of time that must elapse before more personalized Gerrit reports are sent to a given user")

	globalNotifyChannel = flag.String("global-notify-channel", "", "A channel to which all generally-applicable notifications should be sent")

	globalReportChannel = flag.String("global-report-channel", "", "A channel to which a daily report will be sent to the global-notify-channel, noting what change sets have been waiting too long and who they're waiting for")

	globalReportHour = flag.Int("global-report-hour", 15, "Hour (in 24-hour time) in UTC when the daily report will be sent to global-report-channel")

	removeProjectPrefix = flag.String("remove-project-prefix", "", "A common prefix on project names which can be removed if present before displaying in links")

	inlineCommentMaxAge = flag.Duration("inline-comment-max-age", time.Hour, "Inline comments older than this will not be reported, even if not found in the cache")

	workNeededQuery = flag.String("work-needed-query", "is:open -is:wip -label:Code-Review=-2", "Gerrit query to use for determining change sets with work needed for the global report")
)

const (
	gerritQueryPageSize = 100
	globalReportMaxSize = 100

	workingDayStartHour = 0  // 9
	workingDayEndHour   = 24 // 17
)

type MessageHandle interface{}

type ChatSystem interface {
	SetIncomingMessageCallback(cb func(userID, chanID string, isDM bool, text string))
	GetInfoByID(ctx context.Context, chatID string) (ChatUser, error)
	LookupUserByEmail(ctx context.Context, email string) (string, error)
	LookupChannelByName(ctx context.Context, name string) (string, error)
	SendToUserByID(ctx context.Context, id, message string) (MessageHandle, error)
	SendToChannelByID(ctx context.Context, chanID, message string) (MessageHandle, error)

	FormatBold(msg string) string
	FormatItalic(msg string) string
	FormatChangeLink(project string, number int, url, subject string) string
	FormatUserLink(chatID string) string
	FormatLink(url, text string) string
	UnwrapUserLink(userLink string) string
}

type ChatUser interface {
	RealName() string
	IsOnline() bool
	TimeLocation() *time.Location
}

type App struct {
	logger       *zap.Logger
	chat         ChatSystem
	persistentDB *PersistentDB
	gerritClient *gerrit.Client

	reporterLock sync.Mutex

	adminChatID           string
	notifyChannelID       string
	globalReportChannelID string
	lookupOnce            sync.Once
}

func New(logger *zap.Logger, chat ChatSystem, persistentDB *PersistentDB, gerritClient *gerrit.Client) *App {
	app := &App{
		logger:        logger,
		chat:          chat,
		persistentDB:  persistentDB,
		gerritClient:  gerritClient,
	}
	startMsg := fmt.Sprintf("changeset-chihuahua version %s starting up", Version)
	chat.SetIncomingMessageCallback(app.IncomingChatCommand)
	go app.SendToAdmin(context.Background(), startMsg)
	return app
}

// waitGroup is like errgroup.Group but doesn't ask for or handle errors
type waitGroup struct {
	errgroup.Group
}

func (w *waitGroup) Go(f func()) {
	w.Group.Go(func() error {
		f()
		return nil
	})
}

func (w *waitGroup) Wait() {
	_ = w.Group.Wait()
}

const chatCommandHandlingTimeout = time.Minute * 10

func (a *App) IncomingChatCommand(userID, chanID string, isDM bool, text string) {
	if !isDM {
		return
	}
	if !strings.HasPrefix(text, "!") {
		return // only pay attention to !-prefixed messages, for now
	}
	logger := a.logger.With(zap.String("user-id", userID), zap.String("chan-id", chanID), zap.String("text", text))
	logger.Debug("got chat command")
	ctx, cancel := context.WithTimeout(context.Background(), chatCommandHandlingTimeout)
	defer cancel()

	adminChatID := a.getAdminID()
	if userID != adminChatID {
		return
	}
	reply := func(template string, args ...interface{}) {
		_, _ = a.chat.SendToUserByID(ctx, userID, fmt.Sprintf(template, args...))
	}

	if text == "!personal-reports" {
		logger.Info("admin requested unscheduled personal review reports")
		a.PersonalReports(ctx, time.Now())
	} else if text == "!global-report" {
		logger.Info("admin requested unscheduled global report")
		chanID := a.getReportChannelID()
		if chanID == "" {
			reply("No global report channel configured.")
			return
		}
		a.GlobalReport(ctx, time.Now(), chanID)
	} else if strings.HasPrefix(text, "!assoc ") {
		parts := strings.Split(text, " ")
		gerritUsername := ""
		chatID := ""
		if len(parts) == 3 {
			gerritUsername = parts[1]
			chatID = a.chat.UnwrapUserLink(parts[2])
		}
		if chatID == "" {
			reply("bad !assoc usage [!assoc <gerritUsername> <chatUser>]")
			logger.Error("bad !assoc usage")
			return
		}
		logger = logger.With(zap.String("gerrit-username", gerritUsername), zap.String("chat-id", chatID))
		if err := a.persistentDB.AssociateChatIDWithGerritUser(ctx, gerritUsername, chatID); err != nil {
			logger.Error("failed to create manual association", zap.Error(err))
			reply("failed to create association")
		} else {
			logger.Info("admin created manual association")
			reply("ok, %s = %s", gerritUsername, a.chat.FormatUserLink(chatID))
		}
	}
}

// getNewInlineComments fetches _all_ inline comments for the specified change and patchset
// (because apparently that's the only way we can do it), and puts them all in the allInline map.
// It then identifies those inline comments which were posted by the given gerrit user and after
// the specified time, and consults the db to see which, if any, have not already been seen and
// reported. The remainder are returned as newInline (which maps comment IDs to their timestamps).
func (a *App) getNewInlineComments(ctx context.Context, changeID, patchSetID, username string, cutOffTime time.Time) (allInline map[string]*gerrit.CommentInfo, newInline map[string]time.Time, err error) {
	allComments, err := a.gerritClient.ListRevisionComments(ctx, changeID, patchSetID)
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
			if commentInfo.Author.Username == username || commentInfo.Author.Username == "thepaul" {
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

var (
	commentsXCommentsRegex = regexp.MustCompile(`\s*\n\n\([0-9]+ comments?\)\s*$`)
	commentsPatchSetRegex  = regexp.MustCompile(`^\s*Patch Set [0-9]+:\s*`)
)

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

	// strip off the "Patch Set X:"	bit at the top; we'll convey that piece of info separately.
	comment = commentsPatchSetRegex.ReplaceAllString(comment, "")
	// and strip off the "(X comments)" bit at the bottom; we'll send the comments verbatim.
	comment = commentsXCommentsRegex.ReplaceAllString(comment, "")
	comment = strings.TrimSpace(comment)
	// and if it's still longer than a single line, prepend a newline so it's easier to read
	if strings.Contains(comment, "\n") {
		comment = "\n" + comment
	}

	allInline, newInline, err := a.getNewInlineComments(ctx, change.ID, strconv.Itoa(patchSet.Number), author.Username, eventTime.Add(-*inlineCommentMaxAge))
	if err != nil {
		a.logger.Error("could not query inline comments via API", zap.Error(err), zap.String("change-id", change.ID), zap.Int("patch-set", patchSet.Number))
		// fallback: use empty maps; assume there just aren't any inline comments to deal with
	}

	var wg waitGroup
	defer wg.Wait()

	msg := fmt.Sprintf("%s commented on %s patchset %d: %s", commenterLink, changeLink, patchSet.Number, comment)
	var tellChangeOwner string
	if owner.Username != author.Username {
		tellChangeOwner = msg
	} else if comment != "" {
		wg.Go(func() {
			a.notifyAllReviewers(ctx, change.ID, msg, []string{author.Username, change.Owner.Username})
		})
	}
	newInlineCommentsSorted := sortInlineComments(allInline, newInline)
	for _, commentInfo := range newInlineCommentsSorted {
		// add to notification message for change owner
		if owner.Username != author.Username {
			tellChangeOwner += fmt.Sprintf("\n%s: %s",
				a.formatSourceLink(change.URL, patchSet.Number, commentInfo),
				commentInfo.Message)
		}
		// notify prior thread participants
		inlineNotified := map[string]struct{}{author.Username: {}, owner.Username: {}}
		priorComment, priorOk := allInline[commentInfo.InReplyTo]
		for priorOk {
			if _, ok := inlineNotified[priorComment.Author.Username]; !ok {
				inlineNotified[priorComment.Author.Username] = struct{}{}
				msg := fmt.Sprintf("%s replied to a thread on %s: %s", commenterLink, changeLink, commentInfo.Message)
				wg.Go(func() {
					a.notify(ctx, accountFromAccountInfo(&priorComment.Author), msg)
				})
			}
			priorComment, priorOk = allInline[priorComment.InReplyTo]
		}
	}
	if tellChangeOwner != "" {
		wg.Go(func() {
			a.notify(ctx, owner, tellChangeOwner)
		})
	}
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

func (a *App) ReviewerAdded(ctx context.Context, reviewer events.Account, change events.Change, eventTime time.Time) {
	changeLink := a.formatChangeLink(&change)
	// We want to determine _who_ added the reviewer. If it was the reviewer themself, we
	// don't need to notify. and if it wasn't, we want to say who did it in the notification.
	// Unfortunately, the reviewer-added event doesn't carry that information. We turn to the
	// Gerrit API again.
	changeInfo, err := a.gerritClient.GetChangeEx(ctx, change.ID, &gerrit.QueryChangesOpts{
		DescribeDetailedAccounts: true,
		DescribeReviewerUpdates:  true,
	})
	if err != nil {
		a.logger.Error("could not query inline comments via API", zap.Error(err), zap.String("change-id", change.ID))
		a.notify(ctx, &reviewer, fmt.Sprintf("You were added as a reviewer or CC for %s", changeLink))
		return
	}

	// Identify the reviewer update closest to the time of our event that adds the specified
	// user. This isn't 100% guaranteed accurate, but should be sufficient.
	var closest gerrit.ReviewerUpdateInfo
	const maxTimeDiff = time.Hour
	closestTimeDiff := maxTimeDiff
	for _, rui := range changeInfo.ReviewerUpdates {
		if (rui.State == "REVIEWER" || rui.State == "CC") && rui.Reviewer.Username == reviewer.Username {
			timeDiff := absDuration(eventTime.Sub(gerrit.ParseTimestamp(rui.Updated)))
			if closestTimeDiff == maxTimeDiff || timeDiff < closestTimeDiff {
				closest = rui
				closestTimeDiff = timeDiff
			}
		}
	}

	// now, do appropriate notifications based on the results
	if closestTimeDiff < maxTimeDiff {
		// we identified a likely update record
		updater := accountFromAccountInfo(&closest.UpdatedBy)
		updaterLink := a.prepareUserLink(ctx, updater)
		what := "as a reviewer"
		if closest.State == "CC" {
			what = "to be CC'd"
		}
		if updater.Username == reviewer.Username {
			// the reviewer added themself.
			if reviewer.Username != change.Owner.Username {
				// notify the owner
				a.notify(ctx, &change.Owner, fmt.Sprintf("%s signed up %s on your change %s",
					updaterLink, what, changeLink))
			}
		} else {
			// the updater added someone else as a reviewer
			a.notify(ctx, &reviewer, fmt.Sprintf("%s added you %s on %s",
				updaterLink, what, changeLink))
			// if the owner is the same as the reviewer _or_ the updater, we don't need to notify them also
			if change.Owner.Username != reviewer.Username && change.Owner.Username != updater.Username {
				a.notify(ctx, &change.Owner, fmt.Sprintf("%s added %s %s on your change %s",
					updaterLink, a.prepareUserLink(ctx, &reviewer), what, changeLink))
			}
		}
	} else {
		// the records in Gerrit are alarmingly different from what the event told us. oh well?
		a.logger.Error("could not identify reviewer update entity via API", zap.String("change-id", change.ID), zap.String("reviewer", reviewer.Username), zap.Time("event-time", eventTime))
		a.notify(ctx, &reviewer, fmt.Sprintf("You were added as a reviewer or CC for %s", changeLink))
	}
}

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
	changeInfo, err := a.gerritClient.GetPatchSetInfo(ctx, change.ID, strconv.Itoa(patchSet.Number))
	if err != nil {
		a.logger.Error("could not fetch patchset info for change, so can not notify reviewers",
			zap.Error(err), zap.String("change-id", change.ID))
		return
	}
	var reviewerMsg, ccMsg string
	if patchSet.Number == 1 {
		// this is a whole new changeset. notify accordingly.
		reviewerMsg = fmt.Sprintf("%s created a new changeset %s, with you as a reviewer.",
			uploaderLink, changeLink)
		ccMsg = fmt.Sprintf("%s created a new changeset %s, with you CC'd.",
			uploaderLink, changeLink)
	} else {
		changeType := "REWORK"
		// this map should have exactly one entry, but we can't predict its key :/
		for _, revisionInfo := range changeInfo.Revisions {
			changeType = revisionInfo.Kind
		}
		switch changeType {
		case "TRIVIAL_REBASE":
			reviewerMsg = fmt.Sprintf("%s rebased change %s into patchset #%d",
				uploaderLink, changeLink, patchSet.Number)
		case "NO_CHANGE", "NO_CODE_CHANGE":
			reviewerMsg = fmt.Sprintf("%s created a new patchset #%d on change %s (no code changes)",
				uploaderLink, patchSet.Number, changeLink)
		default:
			diffURL := a.gerritClient.DiffURLBetweenPatchSets(&changeInfo, patchSet.Number-1, patchSet.Number)
			reviewerMsg = fmt.Sprintf("%s uploaded a new patchset #%d on change %s (%s)",
				uploaderLink, patchSet.Number, changeLink, a.chat.FormatLink(diffURL, "see changes"))
		}
		ccMsg = reviewerMsg
	}

	// send the reviewerMsg or ccMsg, as appropriate, to all relevant users
	haveNotified := map[string]struct{}{uploader.Username: {}, change.Owner.Username: {}}
	messages := []string{reviewerMsg, ccMsg}
	for i, reviewerGroup := range []string{"REVIEWER", "CC"} {
		useMsg := messages[i]
		for _, reviewer := range changeInfo.Reviewers[reviewerGroup] {
			if _, ok := haveNotified[reviewer.Username]; !ok {
				haveNotified[reviewer.Username] = struct{}{}
				reviewerInfo := accountFromAccountInfo(&reviewer)
				wg.Go(func() {
					a.notify(ctx, reviewerInfo, useMsg)
				})
			}
		}
	}
}

func (a *App) ChangeAbandoned(ctx context.Context, abandoner events.Account, change events.Change, reason string) {
	msg := fmt.Sprintf("%s marked change %s as abandoned with the message: %s",
		a.prepareUserLink(ctx, &abandoner), a.formatChangeLink(&change), reason)
	if abandoner.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, msg)
	}

	a.notifyAllReviewers(ctx, change.ID, msg, []string{abandoner.Username, change.Owner.Username})
}

func (a *App) ChangeMerged(ctx context.Context, submitter events.Account, change events.Change, patchSet events.PatchSet) {
	msg := fmt.Sprintf("%s merged patchset #%d of change %s.", a.prepareUserLink(ctx, &submitter), patchSet.Number, a.formatChangeLink(&change))
	if submitter.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner, msg)
	}

	a.notifyAllReviewers(ctx, change.ID, msg, []string{submitter.Username, change.Owner.Username})
}

func (a *App) AssigneeChanged(_ context.Context, _ events.Account, _ events.Change, _ events.Account) {
	// We don't use the Assignee field for anything right now, I think. Probably good to ignore this
}

func (a *App) notifyAllReviewers(ctx context.Context, changeID, msg string, except []string) {
	reviewers, err := a.gerritClient.GetChangeReviewers(ctx, changeID)
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
	defer wg.Wait()
	for _, reviewer := range reviewers {
		if _, ok := haveNotified[reviewer.Username]; !ok {
			haveNotified[reviewer.Username] = struct{}{}
			reviewerInfo := accountFromAccountInfo(&reviewer.AccountInfo)
			wg.Go(func() { a.notify(ctx, reviewerInfo, msg) })
		}
	}
}

func (a *App) notify(ctx context.Context, gerritUser *events.Account, message string) {
	chatID := a.lookupGerritUser(ctx, gerritUser)
	if chatID == "" {
		return
	}
	_, err := a.chat.SendToUserByID(ctx, chatID, message)
	if err != nil {
		a.logger.Error("failed to send notification",
			zap.Error(err),
			zap.String("chat-id", chatID),
			zap.String("gerrit-username", gerritUser.Username))
	}
}

const startLookupsTimeout = 10 * time.Minute

func (a *App) doStartTimeLookups() {
	a.lookupOnce.Do(func() {
		ctx, cancel := context.WithTimeout(context.Background(), startLookupsTimeout)
		defer cancel()

		var wg waitGroup
		wg.Go(func() {
			chatID, err := a.chat.LookupUserByEmail(ctx, *admin)
			if err != nil {
				a.logger.Error("could not look up admin in chat system", zap.String("admin-email", *admin), zap.Error(err))
			} else {
				a.adminChatID = chatID
			}
		})
		if *globalNotifyChannel != "" {
			wg.Go(func() {
				chanID, err := a.chat.LookupChannelByName(ctx, *globalNotifyChannel)
				if err != nil {
					a.logger.Error("could not look up notify channel in chat system", zap.String("channel-name", *globalNotifyChannel), zap.Error(err))
				} else {
					a.notifyChannelID = chanID
				}
			})
		}
		if *globalReportChannel != "" {
			wg.Go(func() {
				chanID, err := a.chat.LookupChannelByName(ctx, *globalReportChannel)
				if err != nil {
					a.logger.Error("could not look up global report channel in chat system", zap.String("channel-name", *globalReportChannel), zap.Error(err))
				} else {
					a.globalReportChannelID = chanID
				}
			})
		}
		wg.Wait()
	})
}

func (a *App) getAdminID() string {
	a.doStartTimeLookups()
	return a.adminChatID
}

func (a *App) getNotifyChannelID() string {
	a.doStartTimeLookups()
	return a.notifyChannelID
}

func (a *App) getReportChannelID() string {
	a.doStartTimeLookups()
	return a.globalReportChannelID
}

func (a *App) SendToAdmin(ctx context.Context, message string) {
	adminChatID := a.getAdminID()
	if adminChatID == "" {
		a.logger.Error("not sending message to admin; no admin configured", zap.String("message", message))
		return
	}
	if _, err := a.chat.SendToUserByID(ctx, adminChatID, message); err != nil {
		a.logger.Error("failed to send message to admin", zap.String("admin-chat-id", adminChatID), zap.String("message", message), zap.Error(err))
		return
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
	chatID, err = a.chat.LookupUserByEmail(ctx, user.Email)
	if err != nil {
		a.logger.Info("user not found in chat system",
			zap.String("gerrit-username", user.Username),
			zap.String("gerrit-email", user.Email),
			zap.Error(err))
		return ""
	}
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
	chanID := a.getReportChannelID()
	if chanID == "" {
		// no global report configured
		return nil
	}
	timer := time.NewTimer(a.nextGlobalReportTime(getTime()))

	for {
		select {
		case t := <-timer.C:
			a.GlobalReport(ctx, t, chanID)
		case <-ctx.Done():
			if !timer.Stop() {
				<-timer.C
			}
			return ctx.Err()
		}
		timer.Reset(a.nextGlobalReportTime(getTime()))
	}
}

func (a *App) nextGlobalReportTime(now time.Time) time.Duration {
	nextReport := time.Date(now.Year(), now.Month(), now.Day(), *globalReportHour, 0, 0, 0, time.UTC)
	if now.After(nextReport) {
		nextReport = time.Date(now.Year(), now.Month(), now.Day()+1, *globalReportHour, 0, 0, 0, time.UTC)
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

	changes, more, err := a.gerritClient.QueryChangesEx(ctx, []string{*workNeededQuery}, &gerrit.QueryChangesOpts{
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
			waitingMsg = a.chat.FormatBold("submittable")
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
		ownerName := a.chat.FormatItalic("(" + change.Owner.Name + ")")

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
			a.chat.FormatItalic(staleness),
			a.chat.FormatItalic(age),
			waitingMsg))
	}
	if more {
		messages = append(messages, a.chat.FormatItalic("More change sets awaiting action exist. List is too long!"))
	}

	logger.Debug("global report complete", zap.Int("num-messages", len(messages)))
	totalMessage := a.chat.FormatBold("Waiting for review") + "\n\n" + strings.Join(messages, "\n\n")
	if _, err := a.chat.SendToChannelByID(ctx, chanID, totalMessage); err != nil {
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

	// get all accounts gerrit knows about
	accounts, _, err := a.gerritClient.QueryAccountsEx(ctx, "is:active",
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
	chatInfo, err := a.chat.GetInfoByID(ctx, chatUser.ChatId)
	if err != nil {
		logger.Error("Chat system failed to look up user! Possibly deleted?",
			zap.Error(err))
		return
	}
	tz := chatInfo.TimeLocation()
	logger = logger.With(
		zap.String("real-name", chatInfo.RealName()),
		zap.String("tz", tz.String()))

	// check if it's an ok time to send this user their review report
	if !chatInfo.IsOnline() {
		logger.Info("User not online. Skipping.")
		return
	}
	now = now.In(tz)
	if !a.isGoodTimeForReport(acct, chatInfo, now) {
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
	reportItems := []string{a.chat.FormatBold("Reviews assigned to you")}
	for _, ch := range changes {
		createdOn := gerrit.ParseTimestamp(ch.Created)
		lastUpdated := gerrit.ParseTimestamp(ch.Updated)
		reviewItem := fmt.Sprintf("%s\nRequested %s · Updated %s",
			a.formatChangeInfoLink(&ch),
			prettyDate(now, createdOn),
			prettyDate(now, lastUpdated))
		reportItems = append(reportItems, reviewItem)
	}
	report := strings.Join(reportItems, "\n\n")
	logger = logger.With(zap.Int("review-items-pending", len(changes)))

	// and finally, send it out
	if _, err := a.chat.SendToUserByID(ctx, chatUser.ChatId, report); err != nil {
		logger.Error("failed to send report to chat")
		return
	}

	logger.Info("successfully sent report")
}

func (a *App) formatChangeLink(ch *events.Change) string {
	return a.chat.FormatChangeLink(shortenProjectName(ch.Project), ch.Number, ch.URL, ch.Subject)
}

func (a *App) formatChangeInfoLink(ch *gerrit.ChangeInfo) string {
	return a.chat.FormatChangeLink(shortenProjectName(ch.Project), ch.Number, a.gerritClient.URLForChange(ch), ch.Subject)
}

func (a *App) prepareUserLink(ctx context.Context, account *events.Account) string {
	chatID := a.lookupGerritUser(ctx, account)
	return a.formatUserLink(account, chatID)
}

func (a *App) formatUserLink(account *events.Account, chatID string) string {
	if chatID != "" {
		return a.chat.FormatUserLink(chatID)
	}
	// if we don't have a chat ID for them, do our best from the Gerrit account info
	if account.Name != "" {
		return account.Name
	}
	return account.Username
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
	return a.chat.FormatLink(link, linkText)
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

func (a *App) isGoodTimeForReport(_ *gerrit.AccountInfo, _ ChatUser, t time.Time) bool {
	hour := t.Hour()
	return hour >= *personalReportHour && hour >= workingDayStartHour && hour < workingDayEndHour
}

func (a *App) allPendingReviewsFor(ctx context.Context, gerritUsername string) ([]gerrit.ChangeInfo, error) {
	queryString := fmt.Sprintf("(reviewer:\"%[1]s\" OR cc:\"%[1]s\") is:open -reviewedby:\"%[1]s\" -owner:\"%[1]s\" -is:wip",
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
		changePage, more, err := a.gerritClient.QueryChangesEx(ctx, []string{queryString}, opts)
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

func accountFromAccountInfo(accountInfo *gerrit.AccountInfo) *events.Account {
	return &events.Account{
		Name:     accountInfo.Name,
		Email:    accountInfo.Email,
		Username: accountInfo.Username,
	}
}

func shortenProjectName(projectName string) string {
	if *removeProjectPrefix != "" && strings.HasPrefix(projectName, *removeProjectPrefix) {
		projectName = projectName[len(*removeProjectPrefix):]
	}
	return projectName
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
