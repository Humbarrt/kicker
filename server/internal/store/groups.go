package store

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"strings"
	"time"
)

// Group is one row of groups with its permission values (spec 03 §4).
type Group struct {
	ID          int64
	Name        string
	Builtin     string // admin | member | anybody | "" for own groups
	CreatedAt   time.Time
	Permissions map[string]int // missing = off; empty for Admin (it has everything)
	Members     int            // accounts in the group
}

// Groups returns all groups: the built-in ones first, then by name.
func (s *Store) Groups(ctx context.Context) ([]Group, error) {
	rows, err := s.r.QueryContext(ctx, `SELECT g.id, g.name, COALESCE(g.builtin, ''), g.created_at,
		(SELECT COUNT(*) FROM accounts a WHERE a.group_id = g.id)
		FROM groups g`)
	if err != nil {
		return nil, err
	}
	var out []Group
	idx := map[int64]int{}
	for rows.Next() {
		var g Group
		var created int64
		if err := rows.Scan(&g.ID, &g.Name, &g.Builtin, &created, &g.Members); err != nil {
			rows.Close()
			return nil, err
		}
		g.CreatedAt, g.Permissions = time.UnixMilli(created), map[string]int{}
		idx[g.ID] = len(out)
		out = append(out, g)
	}
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, err
	}
	prows, err := s.r.QueryContext(ctx, `SELECT group_id, permission, value FROM group_permissions`)
	if err != nil {
		return nil, err
	}
	defer prows.Close()
	for prows.Next() {
		var id int64
		var key string
		var v int
		if err := prows.Scan(&id, &key, &v); err != nil {
			return nil, err
		}
		if i, ok := idx[id]; ok {
			out[i].Permissions[key] = v
		}
	}
	// own groups after the built-in ones, by name
	return sortGroups(out), prows.Err()
}

func sortGroups(gs []Group) []Group {
	rank := map[string]int{"admin": 0, "member": 1, "anybody": 2, "": 3}
	sort.SliceStable(gs, func(i, j int) bool {
		if ri, rj := rank[gs[i].Builtin], rank[gs[j].Builtin]; ri != rj {
			return ri < rj
		}
		return strings.ToLower(gs[i].Name) < strings.ToLower(gs[j].Name)
	})
	return gs
}

// Group returns one group.
func (s *Store) Group(ctx context.Context, id int64) (Group, error) {
	gs, err := s.Groups(ctx)
	if err != nil {
		return Group{}, err
	}
	for _, g := range gs {
		if g.ID == id {
			return g, nil
		}
	}
	return Group{}, ErrNotFound
}

// CreateGroup inserts a group with its permissions. ErrExists if the name is taken.
func (s *Store) CreateGroup(ctx context.Context, name string, perms map[string]int) (int64, error) {
	var id int64
	err := s.Write(ctx, func(tx *sql.Tx) error {
		res, err := tx.ExecContext(ctx, `INSERT INTO groups (name, created_at) VALUES (?, ?)`, name, s.nowMs())
		if isUnique(err) {
			return ErrExists
		} else if err != nil {
			return err
		}
		if id, err = res.LastInsertId(); err != nil {
			return err
		}
		return setPerms(ctx, tx, id, perms)
	})
	return id, err
}

func setPerms(ctx context.Context, tx *sql.Tx, id int64, perms map[string]int) error {
	for k, v := range perms {
		if _, err := tx.ExecContext(ctx, `INSERT INTO group_permissions (group_id, permission, value) VALUES (?, ?, ?)
			ON CONFLICT (group_id, permission) DO UPDATE SET value = excluded.value`, id, k, v); err != nil {
			return err
		}
	}
	return nil
}

// UpdateGroup renames a group (name != nil) and sets the given permission values.
func (s *Store) UpdateGroup(ctx context.Context, id int64, name *string, perms map[string]int) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		if name != nil {
			err := exec1Tx(ctx, tx, `UPDATE groups SET name = ? WHERE id = ?`, *name, id)
			if isUnique(err) {
				return ErrExists
			} else if err != nil {
				return err
			}
		} else {
			var n int
			if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM groups WHERE id = ?`, id).Scan(&n); err != nil {
				return err
			} else if n == 0 {
				return ErrNotFound
			}
		}
		return setPerms(ctx, tx, id, perms)
	})
}

// ErrBuiltinGroup is returned for changes the built-in groups don't allow.
var ErrBuiltinGroup = errors.New("built-in groups can't be deleted")

// DeleteGroup deletes an own group after moving its accounts to moveTo.
func (s *Store) DeleteGroup(ctx context.Context, id, moveTo int64) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		var builtin sql.NullString
		if err := tx.QueryRowContext(ctx, `SELECT builtin FROM groups WHERE id = ?`, id).Scan(&builtin); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		if builtin.Valid {
			return ErrBuiltinGroup
		}
		var target sql.NullString
		if id == moveTo {
			return ErrNotFound
		} else if err := tx.QueryRowContext(ctx, `SELECT builtin FROM groups WHERE id = ?`, moveTo).Scan(&target); errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		} else if err != nil {
			return err
		}
		// The Admin group can't be the source (it is built in); the triggers refuse Anybody.
		if _, err := tx.ExecContext(ctx, `UPDATE accounts SET group_id = ? WHERE group_id = ?`, moveTo, id); err != nil {
			return err
		}
		return exec1Tx(ctx, tx, `DELETE FROM groups WHERE id = ?`, id)
	})
}

// EnsurePermissions inserts missing permission rows for a group (defaults for new permissions).
func (s *Store) EnsurePermissions(ctx context.Context, id int64, defaults map[string]int) error {
	return s.Write(ctx, func(tx *sql.Tx) error {
		for k, v := range defaults {
			if _, err := tx.ExecContext(ctx, `INSERT INTO group_permissions (group_id, permission, value) VALUES (?, ?, ?)
				ON CONFLICT (group_id, permission) DO NOTHING`, id, k, v); err != nil {
				return err
			}
		}
		return nil
	})
}
