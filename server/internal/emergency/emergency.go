// Package emergency holds the backup passphrase and writes the emergency kit (spec 03 §8): the
// recovery key and the backup passphrase, handed out together.
package emergency

import (
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
)

// PassphraseFile is the backup passphrase in the data folder (mode 0600). Backups (M7) are
// encrypted with it; the kit hands it out now, so it is known before the first backup.
const PassphraseFile = "backup.passphrase"

// PassphraseWords is the length of a generated passphrase.
const PassphraseWords = 8

// Passphrase returns the backup passphrase, creating it on first use.
func Passphrase(dataDir string) (string, error) {
	path := filepath.Join(dataDir, PassphraseFile)
	b, err := os.ReadFile(path)
	if err == nil {
		if p := strings.TrimSpace(string(b)); p != "" {
			return p, nil
		}
	} else if !errors.Is(err, fs.ErrNotExist) {
		return "", err
	}
	p, err := auth.RandomWords(PassphraseWords)
	if err != nil {
		return "", err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, []byte(p+"\n"), 0o600); err != nil {
		return "", err
	}
	if err := os.Rename(tmp, path); err != nil {
		return "", err
	}
	return p, nil
}

// Kit is the emergency kit.
type Kit struct {
	Name             string    `json:"name"`
	ServerID         string    `json:"server_id"`
	CAFingerprint    string    `json:"ca_fingerprint"` // "" until HTTPS (M3 task 10)
	RecoveryKey      string    `json:"recovery_key"`
	BackupPassphrase string    `json:"backup_passphrase"`
	CreatedAt        time.Time `json:"created_at"`
}

// Filename is the kit's download name: kicker-emergency-kit-<name>.txt.
func (k Kit) Filename() string {
	name := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= '0' && r <= '9', r == '-':
			return r
		case r >= 'A' && r <= 'Z':
			return r + 'a' - 'A'
		}
		return '-'
	}, k.Name)
	name = strings.Trim(name, "-")
	if name == "" {
		name = "kicker"
	}
	return "kicker-emergency-kit-" + name + ".txt"
}

// numbered writes words as "1. word" lines, two columns.
func numbered(words string) string {
	ws := strings.Fields(words)
	half := (len(ws) + 1) / 2
	var b strings.Builder
	for i := 0; i < half; i++ {
		fmt.Fprintf(&b, "  %2d. %-12s", i+1, ws[i])
		if j := i + half; j < len(ws) {
			fmt.Fprintf(&b, "  %2d. %s", j+1, ws[j])
		}
		b.WriteString("\n")
	}
	return b.String()
}

// Text is the kit as a text file, in English or German.
func (k Kit) Text(lang string) string {
	t := texts["en"]
	if x, ok := texts[lang]; ok {
		t = x
	}
	fp := k.CAFingerprint
	if fp == "" {
		fp = t.noCA
	}
	return fmt.Sprintf(t.body, k.Name, k.CreatedAt.Format("2006-01-02 15:04"), k.ServerID, fp,
		numbered(k.RecoveryKey), k.BackupPassphrase)
}

var texts = map[string]struct{ body, noCA string }{
	"en": {noCA: "(not yet – the kicker has no HTTPS certificate)", body: `KICKER EMERGENCY KIT
====================

Kicker:          %s
Created:         %s
Server ID:       %s
CA fingerprint:  %s

Keep this file safe and offline – print it, or put it in a password manager.
Anyone who has it can become admin of this kicker and read its backups.


RECOVERY KEY – for a forgotten admin password
---------------------------------------------
%s
1. Open the kicker's login page and choose "Forgot password?" → "Recovery key".
2. Enter your admin username, these 12 words and a new password.
3. A NEW recovery key and kit are made right away – save them; this one stops working.


BACKUP PASSPHRASE – for restoring a backup
------------------------------------------
  %s

Every backup is encrypted with this passphrase. To move the kicker to a new
Raspberry Pi: install it, choose "Restore a backup" in the setup and enter the
backup file and this passphrase. Old backups keep the passphrase they were made with.


Without both: on the Pi itself, "kicker admin create <name>" makes a new admin.
`},
	"de": {noCA: "(noch keiner – der Kicker hat noch kein HTTPS-Zertifikat)", body: `KICKER-NOTFALLKIT
=================

Kicker:          %s
Erstellt:        %s
Server-ID:       %s
CA-Fingerabdruck: %s

Bewahre diese Datei sicher und offline auf – ausgedruckt oder im Passwortmanager.
Wer sie hat, kann Admin dieses Kickers werden und seine Backups lesen.


WIEDERHERSTELLUNGSSCHLÜSSEL – bei vergessenem Admin-Passwort
------------------------------------------------------------
%s
1. Auf der Anmeldeseite des Kickers „Passwort vergessen?“ → „Wiederherstellungsschlüssel“ wählen.
2. Admin-Benutzernamen, diese 12 Wörter und ein neues Passwort eingeben.
3. Sofort entstehen ein NEUER Schlüssel und ein neues Kit – speichere sie; dieser hier gilt dann nicht mehr.


BACKUP-PASSPHRASE – zum Wiederherstellen eines Backups
-----------------------------------------------------
  %s

Jedes Backup ist mit dieser Passphrase verschlüsselt. Für den Umzug auf einen neuen
Raspberry Pi: installieren, im Setup „Backup wiederherstellen“ wählen und Backup-Datei
und diese Passphrase angeben. Alte Backups behalten die Passphrase, mit der sie erstellt wurden.


Ohne beides: auf dem Pi selbst legt „kicker admin create <name>“ einen neuen Admin an.
`},
}
