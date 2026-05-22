package internal

import (
	"database/sql"
	_ "embed"
	"fmt"
	"log/slog"
	"strings"

	"github.com/golang-migrate/migrate/v4"
	_ "github.com/golang-migrate/migrate/v4/database/sqlite3"
	_ "github.com/golang-migrate/migrate/v4/source/file"
	_ "github.com/mattn/go-sqlite3"
)

type migrateLogger struct{}

func (l *migrateLogger) Printf(format string, v ...interface{}) {
	slog.Info("migration", "message", strings.TrimSpace(fmt.Sprintf(format, v...)))
}

func (l *migrateLogger) Verbose() bool {
	return true
}

func Migrate(migrationsPath, dbPath string) error {
	m, err := migrate.New("file://"+migrationsPath, "sqlite3://"+dbPath)
	if err != nil {
		return err
	}
	m.Log = &migrateLogger{}
	defer func() {
		if sErr, dErr := m.Close(); sErr != nil || dErr != nil {
			slog.Error("migration close error", "source", sErr, "db", dErr)
		}
	}()

	err = m.Up()
	if err != nil && err != migrate.ErrNoChange {
		return err
	}

	return nil
}

func Connect(dbPath string) (*sql.DB, error) {
	dsn := dbPath
	if strings.Contains(dsn, "?") {
		dsn += "&"
	} else {
		dsn += "?"
	}
	queryParams := []string{"_busy_timeout=5000", "_journal_mode=WAL", "_loc=UTC", "_datetime_format=rfc3339"}
	dsn += strings.Join(queryParams, "&")
	db, err := sql.Open("sqlite3", dsn)
	if err != nil {
		return nil, fmt.Errorf("failed to open database: %w", err)
	}

	if err = db.Ping(); err != nil {
		return nil, fmt.Errorf("failed to connect to database: %w", err)
	}
	slog.Info("connected to database", "dsn", dsn)
	return db, nil
}
