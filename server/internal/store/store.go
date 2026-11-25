// Package store is the SQLite database (D2): one file, WAL mode, a single writer connection and
// a pool of read-only connections. Schema changes are embedded, forward-only migrations.
package store

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"

	_ "github.com/ncruces/go-sqlite3/driver" // database/sql driver "sqlite3"
	_ "github.com/ncruces/go-sqlite3/embed"  // the SQLite build (WebAssembly, no cgo)
)

//go:embed migrations/*.sql
var migrations embed.FS

// Store is the database. Writes go through Write (one connection, so they are serialized);
// reads use Read() and never block the writer (WAL).
type Store struct {
	path string
	w    *sql.DB
	r    *sql.DB
	now  func() time.Time
}

// Open opens (or creates) the database file and applies pending migrations.
func Open(ctx context.Context, path string) (*Store, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return nil, err
	}
	pragmas := []string{
		"journal_mode(wal)",
		"busy_timeout(5000)",
		"foreign_keys(on)",
		// NORMAL is safe with WAL: an app crash loses nothing, a power cut can lose the last
		// moments but never corrupts the file. FULL would add an fsync to every goal.
		"synchronous(normal)",
	}
	dsn := func(extra ...string) string {
		v := url.Values{}
		for _, p := range append(pragmas, extra...) {
			v.Add("_pragma", p)
		}
		return "file:" + filepath.ToSlash(path) + "?" + v.Encode()
	}

	w, err := sql.Open("sqlite3", dsn()+"&_txlock=immediate")
	if err != nil {
		return nil, err
	}
	w.SetMaxOpenConns(1) // the single writer
	w.SetConnMaxIdleTime(0)

	r, err := sql.Open("sqlite3", dsn("query_only(on)"))
	if err != nil {
		w.Close()
		return nil, err
	}
	r.SetMaxOpenConns(4)

	s := &Store{path: path, w: w, r: r, now: time.Now}
	if err := s.migrate(ctx); err != nil {
		s.Close()
		return nil, fmt.Errorf("migrate %s: %w", path, err)
	}
	return s, nil
}

// Close closes both connection pools.
func (s *Store) Close() error {
	return errors.Join(s.r.Close(), s.w.Close())
}

// Path returns the database file.
func (s *Store) Path() string { return s.path }

// Read returns the read-only pool.
func (s *Store) Read() *sql.DB { return s.r }

// Write runs fn in a write transaction. Transactions are serialized: there is one writer.
func (s *Store) Write(ctx context.Context, fn func(*sql.Tx) error) error {
	tx, err := s.w.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}

func (s *Store) nowMs() int64 { return s.now().UnixMilli() }

// --- migrations -----------------------------------------------------------------------------

// migrateUpTo stops the migrations early (tests of a migration); 0 = all.
var migrateUpTo = 0

type migration struct {
	version int
	name    string
	sql     string
}

func loadMigrations() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, err
	}
	var out []migration
	for _, e := range entries {
		name := e.Name()
		num, _, ok := strings.Cut(name, "_")
		version, err := strconv.Atoi(num)
		if !ok || err != nil || !strings.HasSuffix(name, ".sql") {
			return nil, fmt.Errorf("bad migration file name %q, want NNNN_name.sql", name)
		}
		body, err := migrations.ReadFile("migrations/" + name)
		if err != nil {
			return nil, err
		}
		out = append(out, migration{version: version, name: name, sql: string(body)})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })
	for i, m := range out {
		if m.version != i+1 {
			return nil, fmt.Errorf("migrations must be numbered 1, 2, 3, …: %s", m.name)
		}
	}
	return out, nil
}

// migrate applies all pending migrations, each in its own transaction (forward only).
// TODO(M7): automatic backup before applying migrations to an existing database (D14).
func (s *Store) migrate(ctx context.Context) error {
	if _, err := s.w.ExecContext(ctx, `CREATE TABLE IF NOT EXISTS schema_migrations (
		version    INTEGER PRIMARY KEY,
		name       TEXT    NOT NULL,
		applied_at INTEGER NOT NULL
	) STRICT`); err != nil {
		return err
	}
	all, err := loadMigrations()
	if err != nil {
		return err
	}
	if migrateUpTo > 0 {
		all = all[:migrateUpTo]
	}
	var current int
	if err := s.w.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&current); err != nil {
		return err
	}
	if current > len(all) {
		return fmt.Errorf("database schema v%d is newer than this server (v%d) – update the server", current, len(all))
	}
	for _, m := range all[current:] {
		if err := s.apply(ctx, m); err != nil {
			return err
		}
	}
	return nil
}

// fkOffDirective in a migration's first line runs it with foreign keys off, as SQLite's
// procedure for rebuilding a table requires (https://sqlite.org/lang_altertable.html#otheralter).
// foreign_key_check runs before the commit, so the result must still be consistent.
const fkOffDirective = "-- kicker:foreign-keys-off"

func (s *Store) apply(ctx context.Context, m migration) error {
	conn, err := s.w.Conn(ctx) // the single writer connection, pinned for the PRAGMAs
	if err != nil {
		return err
	}
	defer conn.Close()
	fkOff := strings.HasPrefix(m.sql, fkOffDirective)
	if fkOff {
		if _, err := conn.ExecContext(ctx, `PRAGMA foreign_keys = OFF`); err != nil {
			return err
		}
		defer conn.ExecContext(context.WithoutCancel(ctx), `PRAGMA foreign_keys = ON`) //nolint:errcheck
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback() //nolint:errcheck
	if _, err := tx.ExecContext(ctx, m.sql); err != nil {
		return fmt.Errorf("%s: %w", m.name, err)
	}
	if fkOff {
		rows, err := tx.QueryContext(ctx, `PRAGMA foreign_key_check`)
		if err != nil {
			return err
		}
		bad := rows.Next()
		rows.Close()
		if bad {
			return fmt.Errorf("%s: foreign key violations after the migration", m.name)
		}
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_migrations (version, name, applied_at) VALUES (?, ?, ?)`,
		m.version, m.name, s.nowMs()); err != nil {
		return err
	}
	return tx.Commit()
}

// SchemaVersion returns the number of applied migrations.
func (s *Store) SchemaVersion(ctx context.Context) (int, error) {
	var v int
	err := s.r.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&v)
	return v, err
}
