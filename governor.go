package main

import (
	"bufio"
	"context"
	"flag"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/lib/pq"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"golang.org/x/sync/errgroup"

	"github.com/jtolds/changesetchihuahua/app"
	"github.com/jtolds/changesetchihuahua/gerrit"
	"github.com/jtolds/changesetchihuahua/gerrit/events"
	"github.com/jtolds/changesetchihuahua/slack"
)

var notificationTimeout = flag.Duration("notify-timeout", time.Minute*30, "Maximum amount of time to spend trying to deliver a notification")

type Governor struct {
	topContext context.Context
	logger     *zap.Logger

	teamsLock sync.Mutex
	teamData  map[string]string
	teams     map[string]*Team

	teamFileLock sync.Mutex
	teamFile     string
}

type Team struct {
	id        string
	ctx       context.Context
	canceler  context.CancelFunc
	logger    *zap.Logger
	errGroup  errgroup.Group
	teamApp   *app.App
	slackConn slack.EventedChatSystem
}

type vanillaGerritConnector struct {}

func (v vanillaGerritConnector) OpenGerrit(ctx context.Context, address string) (app.GerritClient, error) {
	return gerrit.OpenClient(ctx, address)
}

func NewGovernor(ctx context.Context, logger *zap.Logger, teamFile string) (*Governor, error) {
	teamData, err := readTeamFile(teamFile)
	if err != nil {
		if !os.IsNotExist(err) {
			return nil, err
		}
		teamData = make(map[string]string)
	}
	g := &Governor{
		topContext: ctx,
		logger:     logger,
		teamData:   teamData,
		teams:      make(map[string]*Team),
		teamFile:   teamFile,
	}
	logger.Info("changeset-chihuahua governor starting up", zap.String("version", Version), zap.Int("num-teams", len(g.teamData)))

	for teamID, setupData := range g.teamData {
		if err := g.StartTeam(teamID, setupData); err != nil {
			logger.Error("failed to start team", zap.String("team-id", teamID), zap.Error(err))
		}
	}
	return g, nil
}

func readTeamFile(fileName string) (teamData map[string]string, err error) {
	f, err := os.Open(fileName)
	if err != nil {
		return nil, err
	}
	defer func() { err = errs.Combine(err, f.Close()) }()

	teamData = make(map[string]string)
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		teamLine := strings.TrimSpace(scanner.Text())
		if teamLine == "" || strings.HasPrefix(teamLine, "#") {
			continue
		}
		parts := strings.SplitN(teamLine, " ", 2)
		if len(parts) < 2 {
			return nil, errs.New("invalid team definition line in %q", fileName)
		}
		teamData[parts[0]] = parts[1]
	}
	if err := scanner.Err(); err != nil {
		return nil, errs.New("reading from %q: %v", fileName, err)
	}
	return teamData, nil
}

func (g *Governor) NewTeam(teamID string, setupData string) error {
	g.teamsLock.Lock()
	defer g.teamsLock.Unlock()

	if _, ok := g.teams[teamID]; ok {
		return errs.New("team %s is already active", teamID)
	}
	if strings.ContainsAny(teamID, " \n") {
		return errs.New("invalid team ID")
	}
	if strings.Contains(setupData, "\n") {
		return errs.New("invalid setup data")
	}
	if err := g.appendTeamDefinition(teamID, setupData); err != nil {
		return errs.New("could not add team definition: %v", err)
	}
	return g.startTeam(teamID, setupData)
}

func (g *Governor) StartTeam(teamID, setupData string) error {
	g.teamsLock.Lock()
	defer g.teamsLock.Unlock()

	if _, ok := g.teams[teamID]; ok {
		return errs.New("team %s is already active", teamID)
	}
	return g.startTeam(teamID, setupData)
}

// must be called with teamsLock held
func (g *Governor) startTeam(teamID, setupData string) error {
	team, err := g.addTeam(teamID, setupData)
	if err != nil {
		return err
	}
	team.errGroup.Go(func() error {
		return team.slackConn.HandleEvents(team.ctx)
	})
	team.errGroup.Go(func() error {
		return team.teamApp.PeriodicGlobalReport(team.ctx, time.Now)
	})
	team.errGroup.Go(func() error {
		return team.teamApp.PeriodicPersonalReports(team.ctx, time.Now)
	})
	go func() {
		err := team.errGroup.Wait()
		team.logger.Info("Team errgroup exited", zap.String("team-id", team.id), zap.Error(err))
		err = team.Close()
		if err != nil {
			team.logger.Error("failed to close team", zap.Error(err))
		}
	}()
	return nil
}

func (g *Governor) addTeam(teamID, setupData string) (team *Team, err error) {
	teamLogger := g.logger.Named(teamID)
	ctx, cancel := context.WithCancel(g.topContext)
	defer func() {
		if err != nil {
			cancel()
		}
	}()

	slackConn, err := slack.NewSlackInterface(ctx, teamLogger.Named("chat"), setupData)
	if err != nil {
		return nil, errs.New("could not initialize slack connection: %v", err)
	}
	teamDBSource, err := addSearchPath(*persistentDBSource, "team-"+teamID)
	if err != nil {
		return nil, errs.New("could not parse %q: %v", *persistentDBSource, err)
	}
	persistentDB, err := app.NewPersistentDB(teamLogger.Named("db"), teamDBSource)
	if err != nil {
		return nil, errs.New("could not open db: %v", err)
	}
	thisApp := app.New(ctx, teamLogger, slackConn, &slack.Formatter{}, persistentDB, vanillaGerritConnector{})

	team = &Team{
		id:        teamID,
		ctx:       ctx,
		canceler:  cancel,
		logger:    teamLogger,
		teamApp:   thisApp,
		slackConn: slackConn,
	}
	g.teams[teamID] = team
	return team, nil
}

func (g *Governor) GerritEventReceived(teamID string, event events.GerritEvent) {
	g.teamsLock.Lock()
	team, ok := g.teams[teamID]
	g.teamsLock.Unlock()
	if !ok {
		g.logger.Info("received event for unknown team", zap.String("team-id", teamID))
		return
	}

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), *notificationTimeout)
		defer cancel()

		team.teamApp.GerritEvent(ctx, event)
	}()
}

func (g *Governor) appendTeamDefinition(teamID, setupData string) (err error) {
	g.teamFileLock.Lock()
	defer g.teamFileLock.Unlock()

	f, err := os.OpenFile(g.teamFile, os.O_APPEND|os.O_WRONLY|os.O_CREATE, 0o644)
	if err != nil {
		return err
	}
	defer func() { err = errs.Combine(err, f.Close()) }()

	dataLine := teamID + " " + setupData + "\n"
	if _, err := f.Write([]byte(dataLine)); err != nil {
		return err
	}
	return nil
}

func (t *Team) Close() error {
	t.canceler()
	return t.teamApp.Close()
}

func addSearchPath(dbURL, schemaName string) (string, error) {
	u, err := url.Parse(dbURL)
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "postgres", "postgresql":
		query := u.Query()
		query.Set("options", "--search_path="+pq.QuoteIdentifier(schemaName))
		u.RawQuery = query.Encode()
	case "sqlite", "sqlite3":
		addSuffix := ""
		if strings.HasSuffix(u.Opaque, ".db") {
			addSuffix = ".db"
			u.Opaque = u.Opaque[:len(u.Opaque)-3]
		}
		u.Opaque += "." + schemaName + addSuffix
	default:
		return "", errs.New("unrecognized db scheme %q", u.Scheme)
	}
	return u.String(), nil
}
