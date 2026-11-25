package store

import (
	"context"
	"crypto/rand"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
)

// Setting keys used so far. Values are stored as JSON.
const (
	SettingCurrentPreset = "current_preset_id" // int64
	SettingBallsOut      = "balls_out"         // int (spec 02 §6.1)
	SettingServerID      = "server_id"         // string, random UUID (spec 03 §9)
	SettingName          = "name"              // string, the kicker's name
	SettingLanguage      = "language"          // string, onboarding step 1
	SettingOnboarded     = "onboarding_done"   // bool
	SettingArgon2        = "argon2_params"     // string "m=…,t=…,p=…" (spec 03 §2)
)

// ServerID returns the server's id, creating it on first use.
func (s *Store) ServerID(ctx context.Context) (string, error) {
	var id string
	if ok, err := s.GetSetting(ctx, SettingServerID, &id); err != nil || ok {
		return id, err
	}
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = b[6]&0x0f | 0x40 // version 4
	b[8] = b[8]&0x3f | 0x80 // variant 10
	id = fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
	// Insert only if still missing, so two concurrent callers agree on one id.
	err := s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `INSERT INTO settings (key, value) VALUES (?, json_quote(?)) ON CONFLICT (key) DO NOTHING`, SettingServerID, id)
		return err
	})
	if err != nil {
		return "", err
	}
	_, err = s.GetSetting(ctx, SettingServerID, &id)
	return id, err
}

// GetSetting decodes a setting into v. It reports false (and leaves v alone) if it isn't set.
func (s *Store) GetSetting(ctx context.Context, key string, v any) (bool, error) {
	var raw string
	err := s.r.QueryRowContext(ctx, `SELECT value FROM settings WHERE key = ?`, key).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return true, json.Unmarshal([]byte(raw), v)
}

// SetSetting stores a setting as JSON.
func (s *Store) SetSetting(ctx context.Context, key string, v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	return s.Write(ctx, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(ctx, `
			INSERT INTO settings (key, value) VALUES (?, ?)
			ON CONFLICT (key) DO UPDATE SET value = excluded.value`, key, string(raw))
		return err
	})
}
