package app

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
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
)

const (
	gerritQueryPageSize = 100

	workingDayStartHour = 0  // 9
	workingDayEndHour   = 24 // 17
)

type MessageHandle interface{}

type ChatSystem interface {
	SupportsMessageEditing() bool
	SupportsMessageDeletion() bool
	SetIncomingMessageCallback(cb func(userID, chanID, text string))

	SendToUserByEmail(ctx context.Context, email, message string) (MessageHandle, error)
	SendToUserByID(ctx context.Context, id, message string) (MessageHandle, error)

	GetInfoByID(ctx context.Context, chatID string) (ChatUser, error)
	LookupUserByEmail(ctx context.Context, email string) (string, error)

	FormatChangeLink(project string, number int, url, subject string) string
}

type ChatUser interface {
	RealName() string
	IsOnline() bool
	TimeLocation() *time.Location
}

type App struct {
	logger       *zap.Logger
	chat         ChatSystem
	directory    *UserDirectory
	gerritClient *gerrit.Client

	reporterLock sync.Mutex
}

func New(logger *zap.Logger, chat ChatSystem, directory *UserDirectory, gerritClient *gerrit.Client) *App {
	app := &App{
		logger:       logger,
		chat:         chat,
		directory:    directory,
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

func (a *App) IncomingChatCommand(userID, chanID, text string) {
	a.logger.Debug("got", zap.String("userid", userID), zap.String("chanid", chanID), zap.String("text", text))
	if strings.HasPrefix(chanID, "D") && userID == "U0DJLDUSG" {
		if text == "!personal-reports" {
			a.ReportWaitingChangeSets(context.Background(), time.Now())
		} else if strings.HasPrefix(text, "!assoc ") {
			parts := strings.Split(text, " ")
			if len(parts) != 3 {
				a.logger.Error("bad !assoc usage", zap.String("issuer", userID))
				return
			}
		}
	}
}

func (a *App) CommentAdded(ctx context.Context, author events.Account, change events.Change, comment string) {
	if change.Owner.Username != author.Username {
		a.notify(ctx, accountInfoFromEvent(&change.Owner),
			fmt.Sprintf("%s commented on your changeset #%d (%q): %s",
				author.Name, change.Number, change.Topic, comment))
	}
	// else, determine somehow if this is a reply to another user, and notify them?
}

func (a *App) ReviewerAdded(ctx context.Context, reviewer events.Account, change events.Change) {
	a.notify(ctx, accountInfoFromEvent(&reviewer),
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
			a.notify(ctx, accountInfoFromEvent(&change.Owner),
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
				a.notify(ctx, accountInfoFromEvent(&reviewer),
					fmt.Sprintf("%s uploaded a new patchset on change #%d (%q), on which you are a reviewer",
						uploader.Name, change.Number, change.Topic))
			})
		}
	}

	wg.Wait()
}

func (a *App) ChangeAbandoned(ctx context.Context, abandoner events.Account, change events.Change, reason string) {
	var wg waitGroup
	if abandoner.Username != change.Owner.Username {
		wg.Go(func() {
			a.notify(ctx, accountInfoFromEvent(&change.Owner),
				fmt.Sprintf("%s marked your change #%d (%q) as abandoned with the message: %s",
					abandoner.Name, change.Number, change.Topic, reason))
		})
	}

	wg.Wait()
}

func (a *App) notify(ctx context.Context, gerritUser gerrit.AccountInfo, message string) {
	chatID := a.lookupGerritUser(ctx, gerritUser)
	if chatID == "" {
		return
	}
	_, err := a.chat.SendToUserByID(ctx, chatID, message)
	if err != nil {
		a.logger.Warn("failed to send notification",
			zap.String("chat-id", chatID),
			zap.String("gerrit-username", gerritUser.Username))
	}
}

func (a *App) SendToAdmin(ctx context.Context, message string) {
	if *admin == "" {
		a.logger.Error("not sending message to admin; no admin configured", zap.String("message", message))
		return
	}
	if _, err := a.chat.SendToUserByEmail(ctx, *admin, message); err != nil {
		a.logger.Error("not sending message to admin; could not look up admin", zap.String("admin-email", *admin), zap.String("message", message), zap.Error(err))
		return
	}
}

func (a *App) lookupGerritUser(ctx context.Context, user gerrit.AccountInfo) string {
	chatID, err := a.directory.LookupChatIDForGerritUser(ctx, user.Username)
	if err == nil {
		return chatID
	}
	if !errors.Is(err, sql.ErrNoRows) {
		a.logger.Error("failed to query directory db", zap.Error(err))
		return ""
	}
	if user.Email == "" {
		a.logger.Info("user not found in directory",
			zap.String("gerrit-username", user.Username))
		return ""
	}
	// fall back to checking for the same email address in the chat system
	chatID, err = a.chat.LookupUserByEmail(ctx, user.Email)
	if err != nil {
		a.logger.Warn("user not found in directory",
			zap.String("gerrit-username", user.Username),
			zap.String("gerrit-email", user.Email),
			zap.Error(err))
		return ""
	}
	// success- save this association in the db
	err = a.directory.AssociateChatIDWithGerritUser(ctx, user.Username, chatID)
	if err != nil {
		a.logger.Error("found new user association, but could not update directory db",
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
// keeping down the load on the Slack and Gerrit servers. If this needs to be snappier,
// though, this is probably a good place to start parallelizing.
func (a *App) ReportWaitingChangeSetsToUser(ctx context.Context, logger *zap.Logger, now, cutOffTime time.Time, acct *gerrit.AccountInfo) {
	logger = logger.With(
		zap.String("gerrit-username", acct.Username),
		zap.String("gerrit-email", acct.Email),
		zap.String("gerrit-name", acct.Name))

	// see if we even know who this is in chat
	chatUser, err := a.directory.LookupGerritUser(ctx, acct.Username)
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
		logger.Debug("User not online. Skipping.")
	}
	now = now.In(tz)
	if !a.isGoodTimeForReport(acct, chatInfo, now) {
		logger.Debug("Not a good time for a report. Skipping.",
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
	if err := a.directory.UpdateLastReportTime(ctx, acct.Username, now); err != nil {
		logger.Error("could not update last-report time in directory", zap.Error(err))
		return
	}

	if len(changes) == 0 {
		logger.Debug("No reviews assigned. No report needed.")
		return
	}

	// build the report
	reportItems := []string{"*Reviews assigned to you*"}
	for _, ch := range changes {
		createdOn := gerrit.ParseTimestamp(ch.Created)
		lastUpdated := gerrit.ParseTimestamp(ch.Updated)
		reviewItem := fmt.Sprintf("%s\nRequested %s · Updated %s",
			a.formatChangeLink(&ch),
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

	logger.Debug("successfully sent report")
}

func (a *App) formatChangeLink(ch *gerrit.ChangeInfo) string {
	project := ch.Project
	if *removeProjectPrefix != "" && strings.HasPrefix(project, *removeProjectPrefix) {
		project = project[len(*removeProjectPrefix):]
	}
	return a.chat.FormatChangeLink(project, ch.Number, a.gerritClient.URLForChange(ch), ch.Subject)
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
	queryString := fmt.Sprintf("reviewer:\"%s\" is:open -reviewedby:\"%s\"",
		gerritUsername, gerritUsername)
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

func accountInfoFromEvent(eventUser *events.Account) gerrit.AccountInfo {
	return gerrit.AccountInfo{
		Name:     eventUser.Name,
		Email:    eventUser.Email,
		Username: eventUser.Username,
	}
}
