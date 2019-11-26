package app

import (
	"context"
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"

	"github.com/jtolds/changesetchihuahua/app/dbx"
)

type UserDirectory struct {
	logger *zap.Logger
	db     *dbx.DB
}

func NewUserDirectory(logger *zap.Logger, dbSource string) (*UserDirectory, error) {
	db, err := openDirectoryDB(dbSource)
	if err != nil {
		return nil, err
	}
	return &UserDirectory{
		logger: logger,
		db:     db,
	}, nil
}

func openDirectoryDB(dbSource string) (*dbx.DB, error) {
	sourceSplit := strings.SplitN(dbSource, ":", 2)
	if len(sourceSplit) == 1 {
		return nil, errs.New("Invalid data source: %q. Example: sqlite:foo.db", dbSource)
	}
	dbDriver := sourceSplit[0]
	if dbDriver == "sqlite" || dbDriver == "sqlite3" {
		return dbx.Open("sqlite3", dbSource[7:])
	}
	if dbDriver == "postgres" || dbDriver == "postgresql" {
		return dbx.Open("postgres", dbSource)
	}
	return nil, errs.New("DB driver %q not recognized", sourceSplit[0])
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
