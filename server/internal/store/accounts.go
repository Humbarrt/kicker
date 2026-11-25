package store

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"time"
)

// ErrExists is returned when a unique value (e.g. a username) is already taken.
var ErrExists = errors.New("already exists")

// Account is one row of accounts (spec 03 §2).
type Account struct {
	ID                 int64
	Username           string
	PasswordHash       string
	GroupID            int64
	Status             string // pending | approved | disabled
	PlayerID           *int64
	MustChangePassword bool
	CreatedAt          time.Time
	LastLoginAt        *time.Time
	// Sign-up request of a pending account: an existing player or a new player's name.
	RequestedPlayerID   *int64
	RequestedPlayerName *string
}

// Session is one row of sessions (spec 03 §5).
type Session struct {
	ID         int64
	AccountID  int64
	TokenHash  string
	Kind       string // web | app
	DeviceName string
	UserAgent  string
	CreatedAt  time.Time
	LastSeenAt time.Time
	ExpiresAt  time.Time
}

// AuditEntry is one row of audit_log.
type AuditEntry struct {
	ID        int64
	At        time.Time
	AccountID *int64
	IP        string
	Action    string
	Target    string
	Details   string
}

const accountCols = `id, username, password_hash, group_id, status, player_id, must_change_password, created_at, last_login_at,
	requested_player_id, requested_player_name`

func scanAccount(sc interface{ Scan(...any) error }) (Account, error) {
	var a Account
	var player, lastLogin, reqPlayer sql.NullInt64
	var reqName sql.NullString
	var created int64
	err := sc.Scan(&a.ID, &a.Username, &a.PasswordHash, &a.GroupID, &a.Status, &player, &a.MustChangePassword, &created, &lastLogin,
		&reqPlayer, &reqName)
	if errors.Is(err, sql.ErrNoRows) {
		return a, ErrNotFound
	}
	a.PlayerID = int64Ptr(player)
	a.RequestedPlayerID, a.RequestedPlayerName = int64Ptr(reqPlayer), strPtr(reqName)
	a.CreatedAt = time.UnixMilli(created)
	if lastLogin.Valid {
		t := time.UnixMilli(lastLogin.Int64)
		a.LastLoginAt = &t
	}
	return a, err
}

// isUnique reports whether err is a UNIQUE constraint violation.
func isUnique(err error) bool {
	return err != nil && strings.Contains(err.Error(), "UNIQUE constraint failed")
}

// CreateAccount inserts an account and returns its id. ErrExists if the username is taken.
func (s *Store) CreateAccount(ctx context.Context, a Account) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO accounts (username, password_hash, group_id, status, player_id, must_change_password,
				created_at, requested_player_id, requested_player_name) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			a.Username, a.PasswordHash, a.GroupID, a.Status, a.PlayerID, a.MustChangePassword, a.CreatedAt.UnixMilli(),
			a.RequestedPlayerID, a.RequestedPlayerName)
		if isUnique(err) {
			return ErrExists
		} else if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// AccountByUsername looks an account up, case-insensitively.
func (s *Store) AccountByUsername(ctx context.Context, username string) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE username = ?`, username))
}

// Account looks an account up by id.
func (s *Store) Account(ctx context.Context, id int64) (Account, error) {
	return scanAccount(s.r.QueryRowContext(ctx, `SELECT `+accountCols+` FROM accounts WHERE id = ?`, id))
}

// Accounts lists all accounts by username.
func (s *Store) Accounts(ctx context.Context) ([]Account, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+accountCols+` FROM accounts ORDER BY username`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Account
	for rows.Next() {
		a, err := scanAccount(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, a)
	}
	return out, rows.Err()
}

// ErrLastAdmin is returned when a change would leave no approved admin.
var ErrLastAdmin = errors.New("the last admin can't be removed, moved or disabled")

// adminsLeft counts approved admins other than exceptID, inside tx.
func adminsLeft(ctx context.Context, tx *sql.Tx, exceptID int64) (int, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a JOIN groups g ON g.id = a.group_id
		WHERE g.builtin = 'admin' AND a.status = 'approved' AND a.id <> ?`, exceptID).Scan(&n)
	return n, err
}

// isApprovedAdmin reports whether the account is an approved admin, inside tx.
func isApprovedAdmin(ctx context.Context, tx *sql.Tx, id int64) (bool, error) {
	var n int
	err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a JOIN groups g ON g.id = a.group_id
		WHERE a.id = ? AND g.builtin = 'admin' AND a.status = 'approved'`, id).Scan(&n)
	return n > 0, err
}

// guardLastAdmin refuses a change that takes away the last approved admin.
func guardLastAdmin(ctx context.Context, tx *sql.Tx, id int64) error {
	admin, err := isApprovedAdmin(ctx, tx, id)
	if err != nil || !admin {
		return err
	}
	n, err := adminsLeft(ctx, tx, id)
	if err != nil {
		return err
	}
	if n == 0 {
		return ErrLastAdmin
	}
	return nil
}

// CountAdmins returns the number of approved admins.
func (s *Store) CountAdmins(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts a JOIN groups g ON g.id = a.group_id
		WHERE g.builtin = 'admin' AND a.status = 'approved'`).Scan(&n)
	return n, err
}

// SetAccountStatus changes an account's status. ErrLastAdmin if it would leave no admin.
func (s *Store) SetAccountStatus(ctx context.Context, id int64, status string) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		if status != "approved" {
			if err := guardLastAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		return exec1Tx(ctx, tx, `UPDATE accounts SET status = ? WHERE id = ?`, status, id)
	})
}

// SetAccountGroup moves an account to a group. ErrLastAdmin if it would leave no admin.
func (s *Store) SetAccountGroup(ctx context.Context, id, groupID int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var builtin sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT builtin FROM groups WHERE id = ?`, groupID).Scan(&builtin); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if builtin.String != "admin" {
			if err := guardLastAdmin(ctx, tx, id); err != nil {
				return err
			}
		}
		return exec1Tx(ctx, tx, `UPDATE accounts SET group_id = ? WHERE id = ?`, groupID, id)
	})
}

// DeleteAccount deletes an account and its sessions. ErrLastAdmin for the last admin.
func (s *Store) DeleteAccount(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		if err := guardLastAdmin(ctx, tx, id); err != nil {
			return err
		}
		return exec1Tx(ctx, tx, `DELETE FROM accounts WHERE id = ?`, id)
	})
}

// SetPasswordHash replaces an account's password hash.
func (s *Store) SetPasswordHash(ctx context.Context, id int64, hash string, mustChange bool) error {
	return s.exec1(ctx, `UPDATE accounts SET password_hash = ?, must_change_password = ? WHERE id = ?`, hash, mustChange, id)
}

// TouchLogin sets last_login_at.
func (s *Store) TouchLogin(ctx context.Context, id int64, at time.Time) error {
	return s.exec1(ctx, `UPDATE accounts SET last_login_at = ? WHERE id = ?`, at.UnixMilli(), id)
}

// exec1 runs a statement that must change exactly one row.
func (s *Store) exec1(ctx context.Context, query string, args ...any) error {
	return s.Write(ctx, func(tx *sql.Tx) error { return exec1Tx(ctx, tx, query, args...) })
}

func exec1Tx(ctx context.Context, tx *sql.Tx, query string, args ...any) error {
	res, err := tx.ExecContext(ctx, query, args...)
	if err != nil {
		return err
	}
	if n, _ := res.RowsAffected(); n == 0 {
		return ErrNotFound
	}
	return nil
}

// --- sessions ---------------------------------------------------------------------------------

// CreateSession inserts a session and returns its id.
func (s *Store) CreateSession(ctx context.Context, se Session) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO sessions (account_id, token_hash, kind, device_name, user_agent, created_at, last_seen_at, expires_at)
			VALUES (?, ?, ?, ?, ?, ?, ?, ?)`, se.AccountID, se.TokenHash, se.Kind, se.DeviceName, se.UserAgent,
			se.CreatedAt.UnixMilli(), se.LastSeenAt.UnixMilli(), se.ExpiresAt.UnixMilli())
		if err != nil {
			return err
		}
		id, err = res.LastInsertId()
		return err
	})
	return id, err
}

// SessionByTokenHash returns a session (expired or not) with its account.
func (s *Store) SessionByTokenHash(ctx context.Context, hash string) (Session, Account, error) {
	var se Session
	var created, seen, expires int64
	row := s.r.QueryRowContext(ctx, `SELECT id, account_id, token_hash, kind, device_name, user_agent, created_at, last_seen_at, expires_at
		FROM sessions WHERE token_hash = ?`, hash)
	err := row.Scan(&se.ID, &se.AccountID, &se.TokenHash, &se.Kind, &se.DeviceName, &se.UserAgent, &created, &seen, &expires)
	if errors.Is(err, sql.ErrNoRows) {
		return se, Account{}, ErrNotFound
	} else if err != nil {
		return se, Account{}, err
	}
	se.CreatedAt, se.LastSeenAt, se.ExpiresAt = time.UnixMilli(created), time.UnixMilli(seen), time.UnixMilli(expires)
	a, err := s.Account(ctx, se.AccountID)
	return se, a, err
}

// TouchSession renews a session.
func (s *Store) TouchSession(ctx context.Context, id int64, seen, expires time.Time) error {
	return s.exec1(ctx, `UPDATE sessions SET last_seen_at = ?, expires_at = ? WHERE id = ?`, seen.UnixMilli(), expires.UnixMilli(), id)
}

// DeleteSession removes one session.
func (s *Store) DeleteSession(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE id = ?`, id)
		return err
	})
}

// DeleteSessions removes all sessions of an account except one (0 = all). Returns how many.
func (s *Store) DeleteSessions(ctx context.Context, accountID, exceptID int64) (int, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ? AND id <> ?`, accountID, exceptID)
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}

// DeleteExpiredSessions removes sessions that expired before now.
func (s *Store) DeleteExpiredSessions(ctx context.Context, now time.Time) (int, error) {
	var n int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE expires_at < ?`, now.UnixMilli())
		if err != nil {
			return err
		}
		n, err = res.RowsAffected()
		return err
	})
	return int(n), err
}

// --- audit log --------------------------------------------------------------------------------

// Audit appends to the audit log.
func (s *Store) Audit(ctx context.Context, e AuditEntry) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO audit_log (at, account_id, ip, action, target, details) VALUES (?, ?, ?, ?, ?, ?)`,
			e.At.UnixMilli(), e.AccountID, e.IP, e.Action, e.Target, e.Details)
		return err
	})
}

// AuditLog returns the newest entries first.
func (s *Store) AuditLog(ctx context.Context, limit int) ([]AuditEntry, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, at, account_id, ip, action, target, details FROM audit_log ORDER BY id DESC LIMIT ?`, limit)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []AuditEntry
	for rows.Next() {
		var e AuditEntry
		var at int64
		var acc sql.NullInt64
		if err := rows.Scan(&e.ID, &at, &acc, &e.IP, &e.Action, &e.Target, &e.Details); err != nil {
			return nil, err
		}
		e.At, e.AccountID = time.UnixMilli(at), int64Ptr(acc)
		out = append(out, e)
	}
	return out, rows.Err()
}
