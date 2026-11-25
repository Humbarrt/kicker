package store

import (
	"context"
	"database/sql"
	"errors"
	"time"
)

// Player is one row of players (D2).
type Player struct {
	ID         int64
	Name       string
	Color      string // #rrggbb
	AvatarHash string // "" = no picture
	IsGuest    bool
	ArchivedAt *time.Time
	CreatedAt  time.Time
	AccountID  *int64     // the linked account, if any
	LastPlayed *time.Time // the end of their last rated game; only set by Players
}

// ErrInUse is returned when a row can't be deleted because other data refers to it.
var ErrInUse = errors.New("still in use")

const playerCols = `p.id, p.name, p.color, COALESCE(p.avatar_hash, ''), p.is_guest, p.archived_at, p.created_at,
	(SELECT a.id FROM accounts a WHERE a.player_id = p.id)`

func scanPlayer(sc interface{ Scan(...any) error }, extra ...any) (Player, error) {
	var p Player
	var archived, account sql.NullInt64
	var created int64
	err := sc.Scan(append([]any{&p.ID, &p.Name, &p.Color, &p.AvatarHash, &p.IsGuest, &archived, &created, &account}, extra...)...)
	if errors.Is(err, sql.ErrNoRows) {
		return p, ErrNotFound
	}
	p.CreatedAt = time.UnixMilli(created)
	if archived.Valid {
		t := time.UnixMilli(archived.Int64)
		p.ArchivedAt = &t
	}
	p.AccountID = int64Ptr(account)
	return p, err
}

// Players lists players by name, the guest first; archived ones only with all.
func (s *Store) Players(ctx context.Context, all bool) ([]Player, error) {
	// The last game uses the index ratings (player_id, ended_at, game_id): one seek per player.
	rows, err := s.r.QueryContext(ctx, `SELECT `+playerCols+`, (SELECT MAX(r.ended_at) FROM ratings r WHERE r.player_id = p.id)
		FROM players p WHERE ? OR p.archived_at IS NULL ORDER BY p.is_guest DESC, p.name COLLATE NOCASE`, all)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []Player{}
	for rows.Next() {
		var last sql.NullInt64
		p, err := scanPlayer(rows, &last)
		if err != nil {
			return nil, err
		}
		if last.Valid {
			t := time.UnixMilli(last.Int64)
			p.LastPlayed = &t
		}
		out = append(out, p)
	}
	return out, rows.Err()
}

// Player returns one player.
func (s *Store) Player(ctx context.Context, id int64) (Player, error) {
	return scanPlayer(s.r.QueryRowContext(ctx, `SELECT `+playerCols+` FROM players p WHERE p.id = ?`, id))
}

// GuestPlayer returns the built-in guest.
func (s *Store) GuestPlayer(ctx context.Context) (Player, error) {
	return scanPlayer(s.r.QueryRowContext(ctx, `SELECT `+playerCols+` FROM players p WHERE p.is_guest = 1`))
}

// ColorUse counts the active players per color.
func (s *Store) ColorUse(ctx context.Context) (map[string]int, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT color, COUNT(*) FROM players WHERE archived_at IS NULL AND is_guest = 0 GROUP BY color`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]int{}
	for rows.Next() {
		var c string
		var n int
		if err := rows.Scan(&c, &n); err != nil {
			return nil, err
		}
		m[c] = n
	}
	return m, rows.Err()
}

// CreatePlayer inserts a player; with linkAccount != 0 the account is linked in the same
// transaction. ErrExists if the name is taken or the account already has a player.
func (s *Store) CreatePlayer(ctx context.Context, p Player, linkAccount int64) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO players (name, color, created_at) VALUES (?, ?, ?)`, p.Name, p.Color, s.nowMs())
		if isUnique(err) {
			return ErrExists
		} else if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		if linkAccount != 0 {
			res, err := tx.ExecContext(ctx, `UPDATE accounts SET player_id = ? WHERE id = ? AND player_id IS NULL`, id, linkAccount)
			if err != nil {
				return err
			}
			if n, _ := res.RowsAffected(); n == 0 {
				return ErrExists
			}
		}
		return nil
	})
	return id, err
}

// PlayerChange is a partial update.
type PlayerChange struct {
	Name     *string
	Color    *string
	Avatar   *string // "" = remove
	Archived *bool
}

// UpdatePlayer applies a change. ErrExists if the new name is taken.
func (s *Store) UpdatePlayer(ctx context.Context, id int64, ch PlayerChange) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var one int
		if err := tx.QueryRowContext(ctx, `SELECT 1 FROM players WHERE id = ?`, id).Scan(&one); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		set := func(q string, v any) error {
			err := exec1Tx(ctx, tx, q, v, id)
			if isUnique(err) {
				return ErrExists
			}
			return err
		}
		if ch.Name != nil {
			if err := set(`UPDATE players SET name = ? WHERE id = ?`, *ch.Name); err != nil {
				return err
			}
		}
		if ch.Color != nil {
			if err := set(`UPDATE players SET color = ? WHERE id = ?`, *ch.Color); err != nil {
				return err
			}
		}
		if ch.Avatar != nil {
			var v any
			if *ch.Avatar != "" {
				v = *ch.Avatar
			}
			if err := set(`UPDATE players SET avatar_hash = ? WHERE id = ?`, v); err != nil {
				return err
			}
		}
		if ch.Archived != nil {
			var v any
			if *ch.Archived {
				v = s.nowMs()
			}
			if err := set(`UPDATE players SET archived_at = ? WHERE id = ?`, v); err != nil {
				return err
			}
		}
		return nil
	})
}

// AvatarInUse reports whether any player uses the picture.
func (s *Store) AvatarInUse(ctx context.Context, hash string) (bool, error) {
	var n int
	err := s.r.QueryRowContext(ctx, `SELECT COUNT(*) FROM players WHERE avatar_hash = ?`, hash).Scan(&n)
	return n > 0, err
}

// DeletePlayer deletes a player that never played and has no account and no coins.
// ErrInUse otherwise: archive it instead.
func (s *Store) DeletePlayer(ctx context.Context, id int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var n int
		err := tx.QueryRowContext(ctx, `SELECT
			(SELECT COUNT(*) FROM game_events WHERE player_id = ?) +
			(SELECT COUNT(*) FROM accounts WHERE player_id = ?) +
			(SELECT COUNT(*) FROM coins WHERE player_id = ?) +
			(SELECT COUNT(*) FROM table_registrations WHERE player_id = ?) +
			(SELECT COUNT(*) FROM players WHERE id = ? AND is_guest = 1)`, id, id, id, id, id).Scan(&n)
		if err != nil {
			return err
		}
		if n > 0 {
			return ErrInUse
		}
		return exec1Tx(ctx, tx, `DELETE FROM players WHERE id = ?`, id)
	})
}

// SetAccountPlayer links an account to a player (0 = unlink). ErrExists if the player already
// has another account.
func (s *Store) SetAccountPlayer(ctx context.Context, accountID, playerID int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var v any
		if playerID != 0 {
			v = playerID
		}
		err := exec1Tx(ctx, tx, `UPDATE accounts SET player_id = ? WHERE id = ?`, v, accountID)
		if isUnique(err) {
			return ErrExists
		}
		return err
	})
}
