-- Accounts, full spec 03 (M3 task 9): sign-up requests, reset codes and the recovery key.

-- What a pending account asked for at sign-up: an existing player (linked only once an admin
-- confirms it) or the name of a new player (created on approval). Cleared on approval.
ALTER TABLE accounts ADD COLUMN requested_player_id INTEGER REFERENCES players (id) ON DELETE SET NULL;
ALTER TABLE accounts ADD COLUMN requested_player_name TEXT CHECK (length(requested_player_name) BETWEEN 1 AND 40);

-- One-time password reset codes, handed over in person (spec 03 §7). One per account.
CREATE TABLE reset_codes (
    account_id INTEGER PRIMARY KEY REFERENCES accounts (id) ON DELETE CASCADE,
    code_hash  TEXT    NOT NULL CHECK (length(code_hash) = 64), -- hex sha256 of the normalized code
    expires_at INTEGER NOT NULL,
    created_by INTEGER, -- the admin; NULL = the console
    created_at INTEGER NOT NULL
) STRICT;

-- The admin recovery key (D18): 12 words, stored only as an argon2id hash. One per kicker.
CREATE TABLE recovery_key (
    id         INTEGER PRIMARY KEY CHECK (id = 1),
    hash       TEXT    NOT NULL CHECK (hash LIKE '$argon2id$%'),
    created_at INTEGER NOT NULL
) STRICT;
