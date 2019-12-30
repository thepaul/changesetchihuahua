package app

import (
	"context"
	"strings"
	"sync"
	"time"

	"github.com/golang-migrate/migrate"
	"github.com/golang-migrate/migrate/database"
	"github.com/golang-migrate/migrate/database/postgres"
	"github.com/golang-migrate/migrate/database/sqlite3"
	"github.com/golang-migrate/migrate/source/go_bindata"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
	"go.uber.org/zap/zapcore"

	"github.com/jtolds/changesetchihuahua/app/dbx"
)

//go:generate go-bindata -pkg app -prefix migrations -modtime 1574794364 -mode 420 -o migrations.go -ignore=/\. migrations/

type UserDirectory struct {
	logger *zap.Logger
	db     *dbx.DB
	dbLock sync.Mutex // is this still necessary with sqlite?

	cacheLock sync.RWMutex
	cache     map[string]string
}

func NewUserDirectory(logger *zap.Logger, dbSource string) (*UserDirectory, error) {
	db, err := initializeDirectoryDB(logger, dbSource)
	if err != nil {
		return nil, err
	}
	return &UserDirectory{
		logger: logger,
		db:     db,
		cache:  make(map[string]string),
	}, nil
}

func openDirectoryDB(dbSource string) (*dbx.DB, string, error) {
	sourceSplit := strings.SplitN(dbSource, ":", 2)
	if len(sourceSplit) == 1 {
		return nil, "", errs.New("Invalid data source: %q. Example: sqlite:foo.db", dbSource)
	}
	driverName := sourceSplit[0]
	switch driverName {
	case "sqlite", "sqlite3":
		driverName = "sqlite3"
		dbSource = sourceSplit[1]
	case "postgres", "postgresql":
		driverName = "postgres"
	default:
		return nil, "", errs.New("unrecognized database driver name %q", driverName)
	}

	dbxDB, err := dbx.Open(driverName, dbSource)
	return dbxDB, driverName, err
}

func initializeDirectoryDB(logger *zap.Logger, dbSource string) (*dbx.DB, error) {
	db, driverName, err := openDirectoryDB(dbSource)
	if err != nil {
		return nil, err
	}

	migrationSource, err := bindata.WithInstance(bindata.Resource(AssetNames(), Asset))
	if err != nil {
		return nil, err
	}

	var migrationTarget database.Driver
	switch driverName {
	case "sqlite3":
		migrationTarget, err = sqlite3.WithInstance(db.DB, &sqlite3.Config{})
	case "postgres":
		migrationTarget, err = postgres.WithInstance(db.DB, &postgres.Config{})
	}
	if err != nil {
		return nil, err
	}

	migrator, err := migrate.NewWithInstance("go-bindata", migrationSource, "directory-db", migrationTarget)
	if err != nil {
		return nil, err
	}
	migrator.Log = newMigrateLogWrapper(logger)

	if err := migrator.Up(); err != nil {
		if err != migrate.ErrNoChange {
			return nil, err
		}
	}

	return db, nil
}

func (ud *UserDirectory) LookupGerritUser(ctx context.Context, gerritUsername string) (*dbx.GerritUser, error) {
	ud.dbLock.Lock()
	defer ud.dbLock.Unlock()

	return ud.db.Get_GerritUser_By_GerritUsername(ctx, dbx.GerritUser_GerritUsername(gerritUsername))
}

func (ud *UserDirectory) LookupChatIDForGerritUser(ctx context.Context, gerritUsername string) (string, error) {
	// check cache
	ud.cacheLock.RLock()
	chatID, found := ud.cache[gerritUsername]
	ud.cacheLock.RUnlock()
	if found {
		return chatID, nil
	}
	// consult db if necessary
	usermapRecord, err := ud.LookupGerritUser(ctx, gerritUsername)
	if err != nil {
		return "", err
	}
	chatID = usermapRecord.ChatId

	// update cache if successful
	ud.cacheLock.Lock()
	ud.cache[gerritUsername] = chatID
	ud.cacheLock.Unlock()

	return chatID, nil
}

func (ud *UserDirectory) AssociateChatIDWithGerritUser(ctx context.Context, gerritUsername, chatID string) error {
	err := func() error {
		ud.dbLock.Lock()
		defer ud.dbLock.Unlock()

		return ud.db.CreateNoReturn_GerritUser(ctx, dbx.GerritUser_GerritUsername(gerritUsername), dbx.GerritUser_ChatId(chatID), dbx.GerritUser_Create_Fields{})
	}()
	if err != nil {
		return err
	}
	// if update was successful, this call is responsible for adding to cache
	ud.cacheLock.Lock()
	ud.cache[gerritUsername] = chatID
	ud.cacheLock.Unlock()

	ud.logger.Debug("associated gerrit user to chat ID",
		zap.String("gerrit-username", gerritUsername),
		zap.String("chat-id", chatID))
	return nil
}

func (ud *UserDirectory) GetAllUsersWhoseLastReportWasBefore(ctx context.Context, t time.Time) ([]*dbx.GerritUser, error) {
	ud.dbLock.Lock()
	defer ud.dbLock.Unlock()

	return ud.db.All_GerritUser_By_LastReport_Less(ctx, dbx.GerritUser_LastReport(t))
}

func (ud *UserDirectory) UpdateLastReportTime(ctx context.Context, gerritUsername string, when time.Time) error {
	ud.dbLock.Lock()
	defer ud.dbLock.Unlock()

	return ud.db.UpdateNoReturn_GerritUser_By_GerritUsername(ctx,
		dbx.GerritUser_GerritUsername(gerritUsername),
		dbx.GerritUser_Update_Fields{LastReport: dbx.GerritUser_LastReport(when)})
}

func (ud *UserDirectory) IdentifyNewInlineComments(ctx context.Context, commentsByID map[string]time.Time) (err error) {
	if len(commentsByID) == 0 {
		return nil
	}
	alternatives := make([]string, 0, len(commentsByID))
	queryArgs := make([]interface{}, 0, len(commentsByID))
	for commentID, _ := range commentsByID {
		alternatives = append(alternatives, "comment_id = ?")
		queryArgs = append(queryArgs, commentID)
	}
	query := `SELECT comment_id FROM inline_comments WHERE (` + strings.Join(alternatives, " OR ") + `)`

	ud.dbLock.Lock()
	defer ud.dbLock.Unlock()

	rows, err := ud.db.DB.QueryContext(ctx, query, queryArgs...)
	if err != nil {
		return err
	}
	defer func() { err = errs.Combine(err, rows.Close()) }()

	ud.logger.Sugar().Debugf("%d elements in commentsByID to start with", len(commentsByID))
	for rows.Next() {
		var foundCommentID string
		if err := rows.Scan(&foundCommentID); err != nil {
			return err
		}
		ud.logger.Sugar().Debugf("%s is not a new comment id", foundCommentID)
		delete(commentsByID, foundCommentID)
	}
	ud.logger.Sugar().Debugf("%d elements in commentsByID now", len(commentsByID))

	if len(commentsByID) > 0 {
		values := make([]string, 0, len(commentsByID))
		queryArgs := make([]interface{}, 0, len(commentsByID)*2)
		for commentID, timeStamp := range commentsByID {
			values = append(values, "(?, ?)")
			queryArgs = append(queryArgs, commentID, timeStamp.UTC())
		}
		query := `INSERT INTO inline_comments (comment_id, updated_at) VALUES ` + strings.Join(values, ", ") + ` ON CONFLICT (comment_id) DO UPDATE SET updated_at = EXCLUDED.updated_at`
		_, err := ud.db.ExecContext(ctx, query, queryArgs...)
		if err != nil {
			return err
		}
	}
	return nil
}

// newMigrateLogWrapper is used to wrap a zap.Logger in a way that is usable
// by golang-migrate.
func newMigrateLogWrapper(logger *zap.Logger) migrateLogWrapper {
	verboseWanted := logger.Check(zapcore.DebugLevel, "") != nil
	sugar := logger.Named("migrate").WithOptions(zap.AddCallerSkip(1)).Sugar()
	return migrateLogWrapper{
		logger:  sugar,
		verbose: verboseWanted,
	}
}

type migrateLogWrapper struct {
	logger  *zap.SugaredLogger
	verbose bool
}

func (w migrateLogWrapper) Printf(format string, v ...interface{}) {
	w.logger.Infof(format, v...)
}

func (w migrateLogWrapper) Verbose() bool {
	return w.verbose
}
