package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"time"

	"github.com/Humbarrt/kicker/server/internal/modules"
)

// Adoptions implements modules.Store on the database.
type Adoptions struct{ s *Store }

// Adoptions returns the module adoption store.
func (s *Store) Adoptions() *Adoptions { return &Adoptions{s: s} }

var _ modules.Store = (*Adoptions)(nil)

func (a *Adoptions) Adoptions() ([]modules.Adoption, error) {
	ctx := context.Background()
	rows, err := a.s.r.QueryContext(ctx, `SELECT uid, name, config, adopted_at FROM modules ORDER BY uid`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byUID := map[string]*modules.Adoption{}
	var out []*modules.Adoption
	for rows.Next() {
		var ad modules.Adoption
		var cfg string
		var at int64
		if err := rows.Scan(&ad.UID, &ad.Name, &cfg, &at); err != nil {
			return nil, err
		}
		ad.AdoptedAt = time.UnixMilli(at)
		ad.Roles, ad.Caps, ad.Config = map[string]string{}, map[string]string{}, map[string]json.RawMessage{}
		if cfg != "{}" {
			ad.Config[""] = json.RawMessage(cfg)
		}
		byUID[ad.UID] = &ad
		out = append(out, &ad)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	caps, err := a.s.r.QueryContext(ctx, `SELECT uid, instance, capability, role, config FROM module_capabilities`)
	if err != nil {
		return nil, err
	}
	defer caps.Close()
	for caps.Next() {
		var uid, inst, capID, cfg string
		var role sql.NullString
		if err := caps.Scan(&uid, &inst, &capID, &role, &cfg); err != nil {
			return nil, err
		}
		ad := byUID[uid]
		if ad == nil {
			continue
		}
		ad.Caps[inst] = capID
		if role.Valid {
			ad.Roles[inst] = role.String
		}
		if cfg != "{}" {
			ad.Config[inst] = json.RawMessage(cfg)
		}
	}
	if err := caps.Err(); err != nil {
		return nil, err
	}
	result := make([]modules.Adoption, 0, len(out))
	for _, ad := range out {
		result = append(result, *ad)
	}
	return result, nil
}

// SaveAdoption replaces the stored adoption of a module (name, roles, config).
func (a *Adoptions) SaveAdoption(ad modules.Adoption) error {
	ctx := context.Background()
	return a.s.Write(ctx, func(tx *sql.Tx) error {
		moduleCfg := "{}"
		if c, ok := ad.Config[""]; ok && len(c) > 0 {
			moduleCfg = string(c)
		}
		at := ad.AdoptedAt
		if at.IsZero() {
			at = a.s.now()
		}
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO modules (uid, name, config, adopted_at) VALUES (?, ?, ?, ?)
			ON CONFLICT (uid) DO UPDATE SET name = excluded.name, config = excluded.config`,
			ad.UID, ad.Name, moduleCfg, at.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `DELETE FROM module_capabilities WHERE uid = ?`, ad.UID); err != nil {
			return err
		}
		instances := map[string]bool{}
		for inst := range ad.Caps {
			instances[inst] = true
		}
		for inst := range ad.Roles {
			instances[inst] = true
		}
		for inst := range ad.Config {
			if inst != "" {
				instances[inst] = true
			}
		}
		for inst := range instances {
			var role any
			if r, ok := ad.Roles[inst]; ok && r != "" {
				role = r
			}
			cfg := "{}"
			if c, ok := ad.Config[inst]; ok && len(c) > 0 {
				cfg = string(c)
			}
			if _, err := tx.ExecContext(ctx, `
				INSERT INTO module_capabilities (uid, instance, capability, role, config) VALUES (?, ?, ?, ?, ?)`,
				ad.UID, inst, ad.Caps[inst], role, cfg); err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteAdoption forgets a module (its capabilities go with it).
func (a *Adoptions) DeleteAdoption(uid string) error {
	return a.s.Write(context.Background(), func(tx *sql.Tx) error {
		_, err := tx.Exec(`DELETE FROM modules WHERE uid = ?`, uid)
		return err
	})
}
