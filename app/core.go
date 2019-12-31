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

	reportTimeHour = flag.Int("user-report-hour", 11, "Hour (in 24-hour time) in user's local time when review report should be sent, if they are online")

	minIntervalBetweenReports = flag.Duration("min-interval-between-reports", time.Hour*6, "Minimum amount of time that must elapse before more personalized Gerrit reports are sent to a given user")

	removeProjectPrefix = flag.String("remove-project-prefix", "", "A common prefix on project names which can be removed if present before displaying in links")

	inlineCommentMaxAge = flag.Duration("inline-comment-max-age", time.Hour, "Inline comments older than this will not be reported, even if not found in the cache")
)

const (
	gerritQueryPageSize = 100

	workingDayStartHour = 0  // 9
	workingDayEndHour   = 24 // 17
)

type MessageHandle interface{}

type ChatSystem interface {
	SetIncomingMessageCallback(cb func(userID, chanID string, isDM bool, text string))
	GetInfoByID(ctx context.Context, chatID string) (ChatUser, error)
	LookupUserByEmail(ctx context.Context, email string) (string, error)
	SendToUserByID(ctx context.Context, id, message string) (MessageHandle, error)

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

	adminChatID     string
	adminLookupOnce sync.Once
}

func New(logger *zap.Logger, chat ChatSystem, persistentDB *PersistentDB, gerritClient *gerrit.Client) *App {
	app := &App{
		logger:       logger,
		chat:         chat,
		persistentDB: persistentDB,
		gerritClient: gerritClient,
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

	adminChatID := a.getAdminID(ctx)
	if userID != adminChatID {
		return
	}
	reply := func(template string, args ...interface{}) {
		_, _ = a.chat.SendToUserByID(ctx, userID, fmt.Sprintf(template, args...))
	}

	if text == "!personal-reports" {
		logger.Info("admin requested personal review reports")
		a.ReportWaitingChangeSets(ctx, time.Now())
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
	commenterChatID := a.lookupGerritUser(ctx, &author)
	commenterLink := a.formatUserLink(&author, commenterChatID)
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
	var tellChangeOwner string
	if owner.Username != author.Username {
		tellChangeOwner = fmt.Sprintf("%s commented on %s patchset %d: %s", commenterLink, changeLink, patchSet.Number, comment)
	} else {
		for _, reviewer := range change.AllReviewers {
			msg := fmt.Sprintf("%s commented on %s patchset %d, for which you are a reviewer: %s", commenterLink, changeLink, patchSet.Number, comment)
			wg.Go(func() {
				a.notify(ctx, &reviewer, msg)
			})
		}
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

	wg.Wait()
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

func (a *App) ReviewerAdded(ctx context.Context, reviewer events.Account, change events.Change) {
	a.notify(ctx, &reviewer,
		fmt.Sprintf("You were added as a reviewer for changeset #%d (%q)",
			change.Number, change.Topic))
	// we want to tell change.Owner about this, but only if they weren't the one who added
	// the reviewer. unclear how to tell yet.
	//a.notify(ctx, change.Owner,
	//	fmt.Sprintf("%s was added as a reviewer on your changeset #%d (%q)",
	//		reviewer.Name, change.Number, change.Topic))
}

func (a *App) PatchSetCreated(ctx context.Context, uploader events.Account, change events.Change, patchSet events.PatchSet) {
	var wg waitGroup
	if uploader.Username != change.Owner.Username {
		wg.Go(func() {
			a.notify(ctx, &change.Owner,
				fmt.Sprintf("%s uploaded a new patchset on your change #%d (%q)",
					uploader.Name, change.Number, change.Topic))
		})
	}

	for _, reviewer := range change.AllReviewers {
		if reviewer.Username == uploader.Username {
			// skip same user
			continue
		}
		if uploader.Username != reviewer.Username {
			wg.Go(func() {
				a.notify(ctx, &reviewer,
					fmt.Sprintf("%s uploaded a new patchset on change #%d (%q), on which you are a reviewer",
						uploader.Name, change.Number, change.Topic))
			})
		}
	}

	wg.Wait()
}

func (a *App) ChangeAbandoned(ctx context.Context, abandoner events.Account, change events.Change, reason string) {
	if abandoner.Username != change.Owner.Username {
		a.notify(ctx, &change.Owner,
			fmt.Sprintf("%s marked your change #%d (%q) as abandoned with the message: %s",
				abandoner.Name, change.Number, change.Topic, reason))
	}
}

func (a *App) ChangeMerged(ctx context.Context, submitter events.Account, change events.Change, patchSet events.PatchSet) {
	if submitter.Username != change.Owner.Username {
		submitterChatID := a.lookupGerritUser(ctx, &submitter)
		a.notify(ctx, &change.Owner, fmt.Sprintf("%s merged patchset #%d of your change %s.", a.formatUserLink(&submitter, submitterChatID), patchSet.Number, a.formatChangeLink(&change)))
	}
}

func (a *App) AssigneeChanged(ctx context.Context, changer events.Account, change events.Change, oldAssignee events.Account) {
	// We don't use the Assignee field for anything right now, I think. Probably good to ignore this
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

func (a *App) getAdminID(ctx context.Context) string {
	a.adminLookupOnce.Do(func() {
		chatID, err := a.chat.LookupUserByEmail(ctx, *admin)
		if err != nil {
			a.logger.Error("could not look up admin in chat system", zap.String("admin-email", *admin), zap.Error(err))
			return
		}
		a.adminChatID = chatID
	})
	return a.adminChatID
}

func (a *App) SendToAdmin(ctx context.Context, message string) {
	adminChatID := a.getAdminID(ctx)
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
		a.logger.Warn("user not found in chat system",
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

func (a *App) PeriodicReportWaitingChangeSets(ctx context.Context, getTime func() time.Time) error {
	// this is similar to time.Ticker, but should always run close to the top of the UTC hour.
	now := getTime()
	timer := time.NewTimer(now.UTC().Truncate(time.Hour).Add(time.Hour).Sub(now))

	for {
		select {
		case t := <-timer.C:
			a.ReportWaitingChangeSets(ctx, t)
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

func (a *App) ReportWaitingChangeSets(ctx context.Context, t time.Time) {
	// a big dumb hammer to keep multiple calls to this from stepping on each other.
	// this is necessary since we allow calls from places other than
	// PeriodicReportWaitingChangeSets() for debugging reasons.
	a.reporterLock.Lock()
	defer a.reporterLock.Unlock()

	logger := a.logger.With(zap.Time("report-start", t))
	logger.Debug("initiating current changeset reports")

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
		a.ReportWaitingChangeSetsToUser(ctx, logger, t, cutOffTime, &acct)
	}
}

// ReportWaitingChangeSetsToUser looks up information on the given user, and if it is an
// appropriate time, sends them a report on the changesets currently waiting for their
// review.
//
// Note this is pretty inefficient with respect to execution time and data transferred.
// Since I don't expect this to be dealing with very large amounts of data, and performance
// is very non-critical here, I would rather let it be a little slow as a simplistic way of
// keeping down the load on the chat and Gerrit servers. If this needs to be snappier,
// though, this is probably a good place to start parallelizing.
func (a *App) ReportWaitingChangeSetsToUser(ctx context.Context, logger *zap.Logger, now, cutOffTime time.Time, acct *gerrit.AccountInfo) {
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
	reportItems := []string{"*Reviews assigned to you*"}
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

func (a *App) formatUserLink(account *events.Account, chatID string) string {
	if chatID != "" {
		return a.chat.FormatUserLink(chatID)
	}
	// if we don't have a chat ID for them, just return the gerrit account description
	return account.String()
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
	var timeUnits string
	var plural string
	var unitDuration time.Duration
	deltaDescriptor := "ago"

	if delta < 0 {
		deltaDescriptor = "from now"
		delta = -delta
	}
	if delta < time.Second {
		// no sub-second precision here
		return "now"
	}
	if delta < time.Minute {
		timeUnits = "second"
		unitDuration = time.Second
	} else if delta < time.Hour {
		timeUnits = "minute"
		unitDuration = time.Minute
	} else if delta < (time.Hour * 24) {
		timeUnits = "hour"
		unitDuration = time.Hour
	} else {
		timeUnits = "day"
		unitDuration = time.Hour * 24
	}
	count := delta.Round(unitDuration) / unitDuration
	if count != 1 {
		plural = "s"
	}
	return fmt.Sprintf("%d %s%s %s", count, timeUnits, plural, deltaDescriptor)
}

func (a *App) isGoodTimeForReport(_ *gerrit.AccountInfo, _ ChatUser, t time.Time) bool {
	hour := t.Hour()
	return hour >= *reportTimeHour && hour >= workingDayStartHour && hour < workingDayEndHour
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

// See https://review.dev.storj.io/Documentation/user-search.html for the various
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
