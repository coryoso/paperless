package db

import (
	"context"
	"database/sql"
	"embed"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"paperless/internal/db/sqlc"

	_ "modernc.org/sqlite"
	_ "modernc.org/sqlite/vec"
)

//go:embed migrations/*.sql
var migrationFS embed.FS

type Store struct {
	conn    *sql.DB
	Queries *sqlc.Queries
}

func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, err
	}
	absolutePath, err := filepath.Abs(path)
	if err != nil {
		return nil, err
	}
	// Connection-local PRAGMAs must also apply when database/sql replaces a
	// connection after cancellation, not only to the first opened connection.
	dsn := url.URL{Scheme: "file", Path: absolutePath}
	query := url.Values{"_pragma": {"foreign_keys(1)"}}
	dsn.RawQuery = query.Encode()
	conn, err := sql.Open("sqlite", dsn.String())
	if err != nil {
		return nil, err
	}
	conn.SetMaxOpenConns(1)
	if _, err := conn.ExecContext(ctx, "PRAGMA foreign_keys = ON"); err != nil {
		conn.Close()
		return nil, err
	}
	if err := os.Chmod(path, 0o600); err != nil {
		conn.Close()
		return nil, fmt.Errorf("protect sqlite database: %w", err)
	}
	store := &Store{conn: conn, Queries: sqlc.New(conn)}
	if err := store.Migrate(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	if err := store.BackfillMetadata(ctx); err != nil {
		conn.Close()
		return nil, err
	}
	return store, nil
}

func (s *Store) Close() error {
	return s.conn.Close()
}

func (s *Store) Conn() *sql.DB {
	return s.conn
}

func (s *Store) Migrate(ctx context.Context) error {
	if _, err := s.conn.ExecContext(ctx, `
CREATE TABLE IF NOT EXISTS schema_migrations (
	version TEXT PRIMARY KEY,
	applied_at TEXT NOT NULL
)`); err != nil {
		return err
	}

	entries, err := migrationFS.ReadDir("migrations")
	if err != nil {
		return err
	}
	var names []string
	for _, entry := range entries {
		if !entry.IsDir() && strings.HasSuffix(entry.Name(), ".sql") {
			names = append(names, entry.Name())
		}
	}
	sort.Strings(names)

	for _, name := range names {
		applied, err := s.migrationApplied(ctx, name)
		if err != nil {
			return err
		}
		if applied {
			continue
		}
		sqlBytes, err := migrationFS.ReadFile("migrations/" + name)
		if err != nil {
			return err
		}
		tx, err := s.conn.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, string(sqlBytes)); err != nil {
			tx.Rollback()
			return fmt.Errorf("migration %s failed: %w", name, err)
		}
		if _, err := tx.ExecContext(
			ctx,
			"INSERT INTO schema_migrations (version, applied_at) VALUES (?, ?)",
			name,
			Now(),
		); err != nil {
			tx.Rollback()
			return err
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	return nil
}

func (s *Store) migrationApplied(ctx context.Context, name string) (bool, error) {
	var version string
	err := s.conn.QueryRowContext(ctx, "SELECT version FROM schema_migrations WHERE version = ?", name).Scan(&version)
	if err == sql.ErrNoRows {
		return false, nil
	}
	return err == nil, err
}

func Now() string {
	return time.Now().UTC().Format(time.RFC3339)
}

func BoolInt(value bool) int64 {
	if value {
		return 1
	}
	return 0
}
