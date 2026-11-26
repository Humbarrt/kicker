package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
)

// Schema returns the number of migrations this server knows: the newest schema it can open.
func Schema() int {
	all, err := loadMigrations()
	if err != nil {
		return 0
	}
	return len(all)
}

// Snapshot writes a consistent copy of the database to dst (VACUUM INTO), while the server runs:
// it goes through the single writer, so no write is half in it. Sessions are left out – a
// restored kicker logs everybody in again (spec 10 §2).
func (s *Store) Snapshot(ctx context.Context, dst string) error {
	if err := os.Remove(dst); err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	if _, err := s.w.ExecContext(ctx, `VACUUM INTO ?`, dst); err != nil {
		return fmt.Errorf("snapshot: %w", err)
	}
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(dst)+"?_pragma=foreign_keys(off)")
	if err != nil {
		return err
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	if _, err := db.ExecContext(ctx, `DELETE FROM sessions`); err != nil {
		return fmt.Errorf("snapshot without sessions: %w", err)
	}
	_, err = db.ExecContext(ctx, `VACUUM`)
	return err
}

// CheckFile checks a database file that isn't open (a backup being restored): SQLite's integrity
// check, and its schema version, which must not be newer than this server's.
func CheckFile(ctx context.Context, path string) (schema int, err error) {
	db, err := sql.Open("sqlite3", "file:"+filepath.ToSlash(path)+"?"+url.Values{"mode": {"ro"}}.Encode())
	if err != nil {
		return 0, err
	}
	defer db.Close()
	var ok string
	if err := db.QueryRowContext(ctx, `PRAGMA integrity_check`).Scan(&ok); err != nil {
		return 0, fmt.Errorf("not a kicker database: %w", err)
	}
	if ok != "ok" {
		return 0, fmt.Errorf("the database is damaged: %s", ok)
	}
	if err := db.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&schema); err != nil {
		return 0, fmt.Errorf("not a kicker database: %w", err)
	}
	if schema > Schema() {
		return schema, fmt.Errorf("the backup is from a newer kicker (schema v%d, this one knows v%d): update first", schema, Schema())
	}
	return schema, nil
}
