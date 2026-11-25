package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Sign-up approval, reset codes, the recovery key and the session list (spec 03 §3, §5, §7).

// ApproveAccount approves a pending account: it joins groupID, gets playerID (0 = none) and its
// sign-up request is cleared – in one transaction. ErrNotFound if it isn't pending (any more),
// ErrExists if the player already has an account.
func (s *Store) ApproveAccount(ctx context.Context, id, groupID, playerID int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var player any
		if playerID != 0 {
			player = playerID
		}
		err := exec1Tx(ctx, tx, `UPDATE accounts SET status = 'approved', group_id = ?, player_id = ?,
			requested_player_id = NULL, requested_player_name = NULL WHERE id = ? AND status = 'pending'`, groupID, player, id)
		if isUnique(err) {
			return ErrExists
		}
		return err
	})
}

// DeletePendingAccount deletes an account that is still pending (a rejected sign-up).
func (s *Store) DeletePendingAccount(ctx context.Context, id int64) error {
	return s.exec1(ctx, `DELETE FROM accounts WHERE id = ? AND status = 'pending'`, id)
}

// CountPending counts accounts waiting for approval.
func (s *Store) CountPending(ctx context.Context) (int, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM accounts WHERE status = 'pending'`).Scan(&n)
	return n, err
}

// ResetCode is a password reset code (only its hash is stored).
type ResetCode struct {
	AccountID int64
	CodeHash  string
	ExpiresAt time.Time
	CreatedBy *int64
	CreatedAt time.Time
}

// SetResetCode stores an account's reset code, replacing an older one.
func (s *Store) SetResetCode(ctx context.Context, rc ResetCode) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO reset_codes (account_id, code_hash, expires_at, created_by, created_at)
			VALUES (?, ?, ?, ?, ?) ON CONFLICT (account_id) DO UPDATE SET code_hash = excluded.code_hash,
			expires_at = excluded.expires_at, created_by = excluded.created_by, created_at = excluded.created_at`,
			rc.AccountID, rc.CodeHash, rc.ExpiresAt.UnixMilli(), rc.CreatedBy, rc.CreatedAt.UnixMilli())
		return err
	})
}

// ResetCodeOf returns an account's reset code; ErrNotFound if it has none.
func (s *Store) ResetCodeOf(ctx context.Context, accountID int64) (ResetCode, error) {
	var rc ResetCode
	var expires, created int64
	var by sql.NullInt64
	err := s.r.QueryRowContext(ctx, `SELECT account_id, code_hash, expires_at, created_by, created_at FROM reset_codes
		WHERE account_id = ?`, accountID).Scan(&rc.AccountID, &rc.CodeHash, &expires, &by, &created)
	if errors.Is(err, sql.ErrNoRows) {
		return rc, ErrNotFound
	}
	rc.ExpiresAt, rc.CreatedAt, rc.CreatedBy = time.UnixMilli(expires), time.UnixMilli(created), int64Ptr(by)
	return rc, err
}

// UseResetCode sets the new password hash, clears must_change_password, deletes the code and
// every session of the account – in one transaction.
func (s *Store) UseResetCode(ctx context.Context, accountID int64, passwordHash string) (sessions int, err error) {
	err = s.Write(ctx, func(tx *sql.Tx) error {
		if err := exec1Tx(ctx, tx, `DELETE FROM reset_codes WHERE account_id = ?`, accountID); err != nil {
			return err
		}
		if err := exec1Tx(ctx, tx, `UPDATE accounts SET password_hash = ?, must_change_password = 0 WHERE id = ?`,
			passwordHash, accountID); err != nil {
			return err
		}
		res, err := tx.ExecContext(ctx, `DELETE FROM sessions WHERE account_id = ?`, accountID)
		if err != nil {
			return err
		}
		n, _ := res.RowsAffected()
		sessions = int(n)
		return nil
	})
	return sessions, err
}

// DeleteExpiredResetCodes removes codes that expired before now.
func (s *Store) DeleteExpiredResetCodes(ctx context.Context, now time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `DELETE FROM reset_codes WHERE expires_at < ?`, now.UnixMilli())
		return err
	})
}

// RecoveryKey returns the recovery key's hash and when it was made; ok is false if there is none.
func (s *Store) RecoveryKey(ctx context.Context) (hash string, created time.Time, ok bool, err error) {
	var ms int64
	err = s.r.QueryRowContext(ctx, `SELECT hash, created_at FROM recovery_key WHERE id = 1`).Scan(&hash, &ms)
	if errors.Is(err, sql.ErrNoRows) {
		return "", time.Time{}, false, nil
	}
	return hash, time.UnixMilli(ms), err == nil, err
}

// SetRecoveryKey replaces the recovery key's hash.
func (s *Store) SetRecoveryKey(ctx context.Context, hash string, created time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO recovery_key (id, hash, created_at) VALUES (1, ?, ?)
			ON CONFLICT (id) DO UPDATE SET hash = excluded.hash, created_at = excluded.created_at`, hash, created.UnixMilli())
		return err
	})
}

// Sessions lists an account's sessions, most recently used first.
func (s *Store) Sessions(ctx context.Context, accountID int64) ([]Session, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT id, account_id, token_hash, kind, device_name, user_agent, created_at,
		last_seen_at, expires_at FROM sessions WHERE account_id = ? ORDER BY last_seen_at DESC`, accountID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Session{}
	for rows.Next() {
		var se Session
		var created, seen, expires int64
		if err := rows.Scan(&se.ID, &se.AccountID, &se.TokenHash, &se.Kind, &se.DeviceName, &se.UserAgent, &created, &seen,
			&expires); err != nil {
			return nil, err
		}
		se.CreatedAt, se.LastSeenAt, se.ExpiresAt = time.UnixMilli(created), time.UnixMilli(seen), time.UnixMilli(expires)
		out = append(out, se)
	}
	return out, rows.Err()
}

// DeleteSessionOf removes one session of an account; ErrNotFound if it isn't theirs.
func (s *Store) DeleteSessionOf(ctx context.Context, accountID, sessionID int64) error {
	return s.exec1(ctx, `DELETE FROM sessions WHERE id = ? AND account_id = ?`, sessionID, accountID)
}
