package emergency

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestPassphraseAndKit(t *testing.T) {
	dir := t.TempDir()
	p1, err := Passphrase(dir)
	if err != nil || len(strings.Fields(p1)) != PassphraseWords {
		t.Fatalf("passphrase %q: %v", p1, err)
	}
	if p2, _ := Passphrase(dir); p2 != p1 {
		t.Errorf("the passphrase is kept: %q vs %q", p2, p1)
	}
	if fi, err := os.Stat(filepath.Join(dir, PassphraseFile)); err != nil || fi.Mode().Perm() != 0o600 {
		t.Errorf("file mode: %v %v", fi, err)
	}
	k := Kit{Name: "Büro Kicker!", ServerID: "abc", RecoveryKey: "a b c d e f g h i j k l", BackupPassphrase: p1,
		CreatedAt: time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)}
	if k.Filename() != "kicker-emergency-kit-b-ro-kicker.txt" {
		t.Errorf("filename: %s", k.Filename())
	}
	txt := k.Text("en")
	for _, want := range []string{"Büro Kicker!", " 1. a ", " 12. l", " 7. g", p1, "not yet"} {
		if !strings.Contains(txt, want) {
			t.Errorf("kit text lacks %q:\n%s", want, txt)
		}
	}
	if !strings.Contains(k.Text("de"), "NOTFALLKIT") {
		t.Error("German kit")
	}
}
