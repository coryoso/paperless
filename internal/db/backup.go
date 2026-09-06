package db

import (
	"context"
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Snapshot writes a transactionally consistent copy of the open database.
// The destination must not already exist.
func (s *Store) Snapshot(ctx context.Context, destination string) error {
	if _, err := os.Stat(destination); err == nil {
		return fmt.Errorf("database snapshot already exists: %s", destination)
	} else if !os.IsNotExist(err) {
		return err
	}
	escaped := strings.ReplaceAll(filepath.Clean(destination), "'", "''")
	_, err := s.conn.ExecContext(ctx, "VACUUM INTO '"+escaped+"'")
	return err
}

func IntegrityCheck(ctx context.Context, path string) error {
	conn, err := sql.Open("sqlite", path)
	if err != nil {
		return err
	}
	defer conn.Close()
	var result string
	if err := conn.QueryRowContext(ctx, "PRAGMA integrity_check").Scan(&result); err != nil {
		return err
	}
	if result != "ok" {
		return fmt.Errorf("SQLite integrity check failed: %s", result)
	}
	return nil
}
