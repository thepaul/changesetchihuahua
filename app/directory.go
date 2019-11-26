package app

import (
	"context"
	"database/sql"
	"strings"

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
}

func NewUserDirectory(logger *zap.Logger, dbSource string) (*UserDirectory, error) {
	db, err := initializeDirectoryDB(logger, dbSource)
	if err != nil {
		return nil, err
	}
	return &UserDirectory{
		logger: logger,
		db:     db,
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

	migrationSource, err := bindata.WithInstance(bindata.Resource(AssetNames(),
		func(name string) ([]byte, error) {
			return Asset(name)
		}))
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

func newMigrateLogWrapper(logger *zap.Logger) migrateLogWrapper {
	verboseWanted := logger.Check(zapcore.DebugLevel, "") != nil
	sugar := logger.WithOptions(zap.AddCallerSkip(1)).Sugar()
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

func (ud *UserDirectory) LookupByGerritUsername(ctx context.Context, gerritUsername string) (string, error) {
	gerritUser, err := ud.db.Get_GerritUser_By_GerritUsername(ctx, dbx.GerritUser_GerritUsername(gerritUsername))
	if err != nil {
		return "", err
	}
	// for now we'll treat this the same as not found. to be improved later
	if gerritUser.SlackId == nil {
		return "", sql.ErrNoRows
	}
	return *gerritUser.SlackId, nil
}
