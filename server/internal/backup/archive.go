// Package backup makes, checks and restores the kicker's encrypted backups (D14, spec 03 §8,
// spec 10 §2): a tar.gz of a database snapshot, the pictures, the identity and the backup
// passphrase, encrypted with age using the backup passphrase.
package backup

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"filippo.io/age"

	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/tlsca"
	"github.com/Humbarrt/kicker/server/internal/version"
)

// DefaultName is a kicker's name until one is set (as the API shows it).
const DefaultName = "Kicker"

// Format is the backup format version, in the manifest.
const Format = 1

// Files in the data folder that a backup carries, besides the database and the pictures.
var identityFiles = []string{tlsca.CAKeyFile, tlsca.CACertFile}

// Limits while reading a backup: it is an upload.
const (
	maxFiles     = 100_000
	maxTotalSize = 8 << 30 // unpacked
)

// ErrPassphrase: the passphrase doesn't open the backup (or it isn't a kicker backup).
var ErrPassphrase = errors.New("wrong passphrase, or not a kicker backup")

// Manifest describes a backup; it is the archive's first file.
type Manifest struct {
	Format   int       `json:"format"`
	Name     string    `json:"name"`
	ServerID string    `json:"server_id"`
	Version  string    `json:"version"` // the server's
	Schema   int       `json:"schema"`
	Created  time.Time `json:"created"`
	Files    []File    `json:"files"`
}

// File is one file in a backup.
type File struct {
	Path   string `json:"path"` // slash-separated, relative: kicker.db, media/…, identity/ca.key, backup.passphrase
	Size   int64  `json:"size"`
	SHA256 string `json:"sha256"`
}

// Source is what a backup is made from.
type Source struct {
	Store    *store.Store
	DataDir  string
	Name     string
	ServerID string
}

// Kinds of backup, the last part of its file name.
const (
	KindAuto         = "auto"          // the schedule
	KindManual       = "manual"        // "Back up now", kicker backup
	KindPreUpdate    = "pre-update"    // before a server update
	KindPreMigration = "pre-migration" // before a database migration
	KindUploaded     = "uploaded"      // uploaded from elsewhere (the name it came with is kept if it has one)
)

// Filename is a backup's name: kicker-backup-<name>-<YYYY-MM-DD-HHMMSS>-<kind>.tar.gz.age.
func Filename(name string, at time.Time, kind string) string {
	return Prefix(name) + at.Format("2006-01-02-150405") + "-" + kind + ".tar.gz.age"
}

var unsafe = regexp.MustCompile(`[^a-z0-9]+`)

// Prefix is the start of every backup file name of a kicker with this name.
func Prefix(name string) string {
	n := strings.Trim(unsafe.ReplaceAllString(strings.ToLower(name), "-"), "-")
	if n == "" {
		n = "kicker" // a name without letters or digits
	}
	return "kicker-backup-" + n + "-"
}

var fileRe = regexp.MustCompile(`^kicker-backup-([a-z0-9-]+?)-(\d{4}-\d{2}-\d{2}-\d{4,6})(?:-(auto|manual|pre-update|pre-migration|uploaded))?\.tar\.gz\.age$`)

// Parsed is what a backup's file name tells without opening it.
type Parsed struct {
	Kicker  string    // the name part (lower case, dashes)
	Created time.Time // local time
	Kind    string
}

// ParseFilename reads a backup file name; ok is false for anything else (also path tricks).
func ParseFilename(f string) (Parsed, bool) {
	m := fileRe.FindStringSubmatch(f)
	if m == nil {
		return Parsed{}, false
	}
	layout := "2006-01-02-150405"
	if len(m[2]) == len("2006-01-02-1504") {
		layout = "2006-01-02-1504"
	}
	at, err := time.ParseInLocation(layout, m[2], time.Local)
	if err != nil {
		return Parsed{}, false
	}
	kind := m[3]
	if kind == "" {
		kind = KindManual
	}
	return Parsed{Kicker: m[1], Created: at, Kind: kind}, true
}

// Write makes a backup and writes it, encrypted, to w.
func Write(ctx context.Context, w io.Writer, src Source) (Manifest, error) {
	if src.Name == "" {
		src.Name = DefaultName
	}
	pass, err := emergency.Passphrase(src.DataDir)
	if err != nil {
		return Manifest{}, err
	}
	tmp, err := os.MkdirTemp(src.DataDir, ".backup-")
	if err != nil {
		return Manifest{}, err
	}
	defer os.RemoveAll(tmp)
	db := filepath.Join(tmp, "kicker.db")
	if err := src.Store.Snapshot(ctx, db); err != nil {
		return Manifest{}, err
	}
	schema, err := store.CheckFile(ctx, db)
	if err != nil {
		return Manifest{}, err
	}

	// The files, then the manifest with their checksums (a first pass over them).
	type item struct{ path, src string }
	items := []item{{"kicker.db", db}}
	media := filepath.Join(src.DataDir, "media")
	err = filepath.WalkDir(media, func(p string, d fs.DirEntry, err error) error {
		if errors.Is(err, fs.ErrNotExist) {
			return fs.SkipDir
		}
		if err != nil || d.IsDir() || !d.Type().IsRegular() {
			return err
		}
		rel, _ := filepath.Rel(src.DataDir, p)
		items = append(items, item{filepath.ToSlash(rel), p})
		return nil
	})
	if err != nil {
		return Manifest{}, err
	}
	for _, f := range identityFiles {
		if p := filepath.Join(src.DataDir, f); exists(p) {
			items = append(items, item{"identity/" + f, p})
		}
	}
	items = append(items, item{emergency.PassphraseFile, filepath.Join(src.DataDir, emergency.PassphraseFile)})

	m := Manifest{Format: Format, Name: src.Name, ServerID: src.ServerID, Version: version.Version, Schema: schema, Created: time.Now().UTC()}
	for _, it := range items {
		size, sum, err := hashFile(it.src)
		if err != nil {
			return Manifest{}, err
		}
		m.Files = append(m.Files, File{Path: it.path, Size: size, SHA256: sum})
	}

	rcp, err := age.NewScryptRecipient(pass)
	if err != nil {
		return Manifest{}, err
	}
	enc, err := age.Encrypt(w, rcp)
	if err != nil {
		return Manifest{}, err
	}
	gz := gzip.NewWriter(enc)
	tw := tar.NewWriter(gz)
	raw, _ := json.MarshalIndent(m, "", "  ")
	if err := tw.WriteHeader(&tar.Header{Name: "manifest.json", Mode: 0o600, Size: int64(len(raw)), ModTime: m.Created}); err != nil {
		return Manifest{}, err
	}
	if _, err := tw.Write(raw); err != nil {
		return Manifest{}, err
	}
	for i, it := range items {
		if err := addFile(tw, it.path, it.src, m.Files[i].Size, m.Created); err != nil {
			return Manifest{}, err
		}
	}
	if err := errors.Join(tw.Close(), gz.Close(), enc.Close()); err != nil {
		return Manifest{}, err
	}
	return m, nil
}

func addFile(tw *tar.Writer, name, src string, size int64, at time.Time) error {
	f, err := os.Open(src)
	if err != nil {
		return err
	}
	defer f.Close()
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: size, ModTime: at}); err != nil {
		return err
	}
	n, err := io.Copy(tw, io.LimitReader(f, size))
	if err == nil && n != size {
		err = fmt.Errorf("%s changed while it was backed up", name)
	}
	return err
}

func hashFile(p string) (int64, string, error) {
	f, err := os.Open(p)
	if err != nil {
		return 0, "", err
	}
	defer f.Close()
	h := sha256.New()
	n, err := io.Copy(h, f)
	return n, hex.EncodeToString(h.Sum(nil)), err
}

func exists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// Extract decrypts a backup into dir (which must be empty or not exist) and checks it: every file
// against the manifest, the database's integrity and schema. Nothing outside dir is touched, so
// a wrong passphrase or a damaged file changes nothing (spec 10 §2).
func Extract(ctx context.Context, r io.Reader, passphrase, dir string) (Manifest, error) {
	var m Manifest
	id, err := age.NewScryptIdentity(strings.TrimSpace(passphrase))
	if err != nil {
		return m, ErrPassphrase
	}
	dec, err := age.Decrypt(r, id)
	if err != nil {
		return m, ErrPassphrase
	}
	gz, err := gzip.NewReader(dec)
	if err != nil {
		return m, fmt.Errorf("damaged backup: %w", err)
	}
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return m, err
	}
	tr := tar.NewReader(gz)
	got := map[string]File{}
	var total int64
	for n := 0; ; n++ {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return m, fmt.Errorf("damaged backup: %w", err)
		}
		if n > maxFiles {
			return m, errors.New("too many files in the backup")
		}
		if n == 0 {
			if h.Name != "manifest.json" || h.Size > 32<<20 {
				return m, errors.New("not a kicker backup: no manifest")
			}
			if err := json.NewDecoder(io.LimitReader(tr, h.Size)).Decode(&m); err != nil {
				return m, fmt.Errorf("damaged backup: manifest: %w", err)
			}
			if m.Format < 1 || m.Format > Format {
				return m, fmt.Errorf("backup format %d is newer than this kicker: update first", m.Format)
			}
			continue
		}
		name, ok := cleanName(h.Name)
		if !ok || h.Typeflag != tar.TypeReg {
			return m, fmt.Errorf("damaged backup: unexpected entry %q", h.Name)
		}
		total += h.Size
		if total > maxTotalSize {
			return m, errors.New("the backup is too large")
		}
		dst := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
			return m, err
		}
		f, err := os.OpenFile(dst, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
		if err != nil {
			return m, err
		}
		hs := sha256.New()
		size, err := io.Copy(io.MultiWriter(f, hs), io.LimitReader(tr, h.Size))
		if err = errors.Join(err, f.Close()); err != nil {
			return m, fmt.Errorf("damaged backup: %w", err)
		}
		got[name] = File{Path: name, Size: size, SHA256: hex.EncodeToString(hs.Sum(nil))}
	}
	if m.Format == 0 {
		return m, errors.New("not a kicker backup: no manifest")
	}
	for _, f := range m.Files {
		g, ok := got[f.Path]
		if !ok || g.Size != f.Size || g.SHA256 != f.SHA256 {
			return m, fmt.Errorf("damaged backup: %s doesn't match its checksum", f.Path)
		}
		delete(got, f.Path)
	}
	if len(got) > 0 {
		return m, errors.New("damaged backup: files that aren't in its manifest")
	}
	if _, ok := find(m, "kicker.db"); !ok {
		return m, errors.New("damaged backup: no database")
	}
	if _, err := store.CheckFile(ctx, filepath.Join(dir, "kicker.db")); err != nil {
		return m, err
	}
	return m, nil
}

func find(m Manifest, p string) (File, bool) {
	for _, f := range m.Files {
		if f.Path == p {
			return f, true
		}
	}
	return File{}, false
}

// cleanName accepts only the paths a backup has: kicker.db, backup.passphrase, identity/<file>,
// media/… – relative, no "..".
func cleanName(n string) (string, bool) {
	c := path.Clean(n)
	if c != n || strings.HasPrefix(c, "/") || strings.Contains(c, "..") || strings.Contains(c, `\`) {
		return "", false
	}
	switch {
	case c == "kicker.db", c == emergency.PassphraseFile:
		return c, true
	case strings.HasPrefix(c, "identity/"):
		for _, f := range identityFiles {
			if c == "identity/"+f {
				return c, true
			}
		}
		return "", false
	case strings.HasPrefix(c, "media/"):
		return c, true
	}
	return "", false
}
