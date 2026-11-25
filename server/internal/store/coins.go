package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Coin is one row of coins (D2).
type Coin struct {
	UID        string
	PlayerID   int64
	Label      string
	Active     bool
	CreatedAt  time.Time
	LastSeenAt *time.Time
}

const coinCols = `uid, player_id, label, active, created_at, last_seen_at`

func scanCoin(sc interface{ Scan(...any) error }) (Coin, error) {
	var c Coin
	var created int64
	var seen sql.NullInt64
	err := sc.Scan(&c.UID, &c.PlayerID, &c.Label, &c.Active, &created, &seen)
	if errors.Is(err, sql.ErrNoRows) {
		return c, ErrNotFound
	}
	c.CreatedAt = time.UnixMilli(created)
	if seen.Valid {
		t := time.UnixMilli(seen.Int64)
		c.LastSeenAt = &t
	}
	return c, err
}

// Coins lists coins, newest first; playerID != 0 filters by player.
func (s *Store) Coins(ctx context.Context, playerID int64) ([]Coin, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT `+coinCols+` FROM coins WHERE ? = 0 OR player_id = ? ORDER BY created_at DESC`, playerID, playerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Coin{}
	for rows.Next() {
		c, err := scanCoin(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	return out, rows.Err()
}

// Coin returns one coin.
func (s *Store) Coin(ctx context.Context, uid string) (Coin, error) {
	return scanCoin(s.r.QueryRowContext(ctx, `SELECT `+coinCols+` FROM coins WHERE uid = ?`, uid))
}

// SaveCoin creates a coin or reassigns an existing one (player, label, active).
func (s *Store) SaveCoin(ctx context.Context, c Coin) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO coins (uid, player_id, label, active, created_at) VALUES (?, ?, ?, ?, ?)
			ON CONFLICT (uid) DO UPDATE SET player_id = excluded.player_id, label = excluded.label, active = excluded.active`,
			c.UID, c.PlayerID, c.Label, c.Active, s.nowMs())
		return err
	})
}

// DeleteCoin forgets a coin.
func (s *Store) DeleteCoin(ctx context.Context, uid string) error {
	return s.exec1(ctx, `DELETE FROM coins WHERE uid = ?`, uid)
}

// TouchCoin records that a coin was inserted.
func (s *Store) TouchCoin(ctx context.Context, uid string, at time.Time) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `UPDATE coins SET last_seen_at = ? WHERE uid = ?`, at.UnixMilli(), uid)
		return err
	})
}
