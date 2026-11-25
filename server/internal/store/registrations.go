package store

import (
	"context"
	"database/sql"
)

// Registration is a row of table_registrations with its sources (spec 02 §4).
type Registration struct {
	Side     string
	Slot     int
	PlayerID *int64 // nil = unknown coin
	CoinUID  *string
	Sources  []RegSource
}

// RegSource is a row of table_registration_sources.
type RegSource struct {
	Via   string // coin | corner | manual
	Ref   string
	Stale bool
}

// Registrations returns the saved registrations, ordered by side and slot.
func (s *Store) Registrations(ctx context.Context) ([]Registration, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT r.side, r.slot, r.player_id, r.coin_uid, s.via, s.ref, s.stale
		FROM table_registrations r JOIN table_registration_sources s USING (side, slot)
		ORDER BY r.side = 'away', r.slot, s.position`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []Registration
	for rows.Next() {
		var r Registration
		var player sql.NullInt64
		var coin sql.NullString
		var src RegSource
		if err := rows.Scan(&r.Side, &r.Slot, &player, &coin, &src.Via, &src.Ref, &src.Stale); err != nil {
			return nil, err
		}
		if n := len(out); n > 0 && out[n-1].Side == r.Side && out[n-1].Slot == r.Slot {
			out[n-1].Sources = append(out[n-1].Sources, src)
			continue
		}
		r.PlayerID, r.CoinUID, r.Sources = int64Ptr(player), strPtr(coin), []RegSource{src}
		out = append(out, r)
	}
	return out, rows.Err()
}

// SaveRegistrations replaces all registrations.
func (s *Store) SaveRegistrations(ctx context.Context, regs []Registration) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		if _, err := tx.ExecContext(ctx, `DELETE FROM table_registrations`); err != nil {
			return err
		}
		for _, r := range regs {
			if _, err := tx.ExecContext(ctx, `INSERT INTO table_registrations (side, slot, player_id, coin_uid) VALUES (?, ?, ?, ?)`,
				r.Side, r.Slot, r.PlayerID, r.CoinUID); err != nil {
				return err
			}
			for i, src := range r.Sources {
				if _, err := tx.ExecContext(ctx, `INSERT INTO table_registration_sources (side, slot, via, ref, stale, position)
					VALUES (?, ?, ?, ?, ?, ?)`, r.Side, r.Slot, src.Via, src.Ref, src.Stale, i); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
