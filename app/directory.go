package app

import (
	"context"
	"database/sql"
	"strings"

	_ "github.com/mattn/go-sqlite3"
	"github.com/zeebo/errs"
	"go.uber.org/zap"
)

//go:generate dbx.v1 golang -p app -d postgres -d sqlite3 usermap.dbx .

type UserDirectory struct {
	logger *zap.Logger
	db     *sql.DB
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

func openDirectoryDB(dbSource string) (*sql.DB, error) {
	sourceSplit := strings.SplitN(dbSource, ":", 2)
	if len(sourceSplit) == 1 {
		return nil, errs.New("Invalid data source: %q. Example: sqlite:foo.db", dbSource)
	}
	dbDriver := sourceSplit[0]
	if dbDriver == "sqlite" || dbDriver == "sqlite3" {
		return sql.Open("sqlite3", dbSource[7:])
	}
	if dbDriver == "postgres" || dbDriver == "postgresql" {
		return sql.Open("postgres", dbSource)
	}
	return nil, errs.New("DB driver %q not recognized", sourceSplit[0])
}

func (ud *UserDirectory) LookupByGerritUsername(ctx context.Context, gerritUsername string) (string, error) {
	ud.db.QueryRowContext(ctx, `SELECT slack_id FROM usermap WHERE gerrit_username = $1`)
}
