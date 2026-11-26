// Package update keeps the server up to date (spec 10 §3, D15): it checks GitHub for a newer
// release, verifies release.json with the key built into this binary, installs the server binary
// into <data>/bin, restarts into it and rolls back – binary and database – if the new version
// doesn't come up healthy.
//
// Files in the data folder:
//
//	bin/kicker-X.Y.Z   installed versions (the current and the previous one are kept)
//	bin/current        symlink to the running version; systemd runs it
//	bin/previous       symlink to the version before
//	update.json        an update in progress (State); gone once the new version is healthy
package update

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/backup"
	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/release"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/version"
)

// Topic is the SSE topic for update checks, progress and results (admins).
const Topic = "update"

// Settings keys.
const (
	SettingConfig = "update_config"
	SettingStatus = "update_status"
)

// Config are the update settings (Settings → Updates, setup step 8).
type Config struct {
	Check bool `json:"check"` // check daily
	Auto  bool `json:"auto"`  // install automatically at the backup time when the table is idle
}

// DefaultConfig: check daily, install by hand.
func DefaultConfig() Config { return Config{Check: true} }

// Available is a newer release found by the last check.
type Available struct {
	Version    string    `json:"version"`
	Tag        string    `json:"tag"`
	Date       time.Time `json:"date"`
	Notes      string    `json:"notes"`
	MinVersion string    `json:"min_version,omitempty"`
	URL        string    `json:"url"` // the release page
}

// Result is the outcome of the last installation.
type Result struct {
	At    time.Time `json:"at"`
	From  string    `json:"from"`
	To    string    `json:"to"`
	OK    bool      `json:"ok"`
	Error string    `json:"error,omitempty"`
}

// Status is what the settings page shows.
type Status struct {
	LastCheck  time.Time  `json:"last_check,omitzero"`
	CheckError string     `json:"check_error,omitempty"`
	Available  *Available `json:"available"`
	Last       *Result    `json:"last"`
	// Step of an installation in progress: backup, download, check, restart, then verify in the
	// new version until it counts as healthy ("" = none).
	Step string `json:"step,omitempty"`
}

// Errors for the API.
var (
	ErrDisabled     = errors.New("updates are disabled in this build")
	ErrNotInstalled = errors.New("this kicker doesn't run from <data>/bin/current, so it can't update itself – install it with the install script or the SD card image")
	ErrBusy         = errors.New("a game is being played – the update waits until the table is free")
	ErrNothing      = errors.New("no newer version is available")
	ErrTooOld       = errors.New("this version is too old to update directly – install the version named in the notes first")
	ErrRunning      = errors.New("an update is already being installed")
	ErrGitHubBusy   = errors.New("GitHub is busy, try again later")
)

// Options for New.
type Options struct {
	Store   *store.Store
	DataDir string
	Bus     *bus.Bus
	Logger  *slog.Logger
	// Busy reports a game being played: installing waits.
	Busy func() bool
	// Ready: setup is complete (nothing is checked before).
	Ready func() bool
	// Time is the daily backup time (HH:MM): automatic installs start then.
	Time func() string
	// Restart re-executes the given binary (the same process id, spec 10 §3).
	Restart func(binary string)
	// API is the GitHub API base URL (tests use a fake one).
	API string
	// Repo and PublicKey default to the values built into the binary (version.UpdateRepo, …).
	Repo, PublicKey, Version string
	HTTP                     *http.Client
	Now                      func() time.Time
	// HealthDelay: how long a new version must stay up to count as healthy (default 60 s).
	HealthDelay time.Duration
}

// Service checks for and installs updates.
type Service struct {
	o       Options
	mu      sync.Mutex // one check or installation at a time
	started time.Time
	// fromCurrent reports whether this process runs <data>/bin/current (tests replace it).
	fromCurrent func() bool

	stepMu sync.Mutex
	step   string
}

// New makes the update service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Busy == nil {
		o.Busy = func() bool { return false }
	}
	if o.Ready == nil {
		o.Ready = func() bool { return true }
	}
	if o.Time == nil {
		o.Time = func() string { return "04:00" }
	}
	if o.API == "" {
		o.API = "https://api.github.com"
	}
	if o.Repo == "" {
		o.Repo = version.UpdateRepo
	}
	if o.PublicKey == "" {
		o.PublicKey = version.UpdatePubKey
	}
	if o.Version == "" {
		o.Version = version.Version
	}
	if o.HTTP == nil {
		o.HTTP = &http.Client{Timeout: 10 * time.Minute}
	}
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.HealthDelay == 0 {
		o.HealthDelay = 60 * time.Second
	}
	s := &Service{o: o, started: o.Now()}
	s.fromCurrent = func() bool { return RunningFromCurrent(o.DataDir) }
	return s
}

// Enabled reports whether this build can update itself (a release build with repo and key).
func (s *Service) Enabled() bool { return s.o.Repo != "" && s.o.PublicKey != "" }

// Version is the running version.
func (s *Service) Version() string { return s.o.Version }

// Repo is the repo releases come from.
func (s *Service) Repo() string { return s.o.Repo }

func (s *Service) publish() {
	if s.o.Bus != nil {
		s.o.Bus.Publish(Topic, nil)
	}
}

// Config returns the settings.
func (s *Service) Config(ctx context.Context) (Config, error) {
	c := DefaultConfig()
	_, err := s.o.Store.GetSetting(ctx, SettingConfig, &c)
	return c, err
}

// SetConfig saves the settings.
func (s *Service) SetConfig(ctx context.Context, c Config) error {
	if err := s.o.Store.SetSetting(ctx, SettingConfig, c); err != nil {
		return err
	}
	s.publish()
	return nil
}

// Status returns the last check, the available release and the last result. A release that is
// not newer than the running version any more (installed by hand) is not offered.
func (s *Service) Status(ctx context.Context) (Status, error) {
	var st Status
	_, err := s.o.Store.GetSetting(ctx, SettingStatus, &st)
	if st.Available != nil && !release.Newer(st.Available.Version, s.o.Version) {
		st.Available = nil
	}
	s.stepMu.Lock()
	st.Step = s.step
	s.stepMu.Unlock()
	if st.Step == "" {
		if p, _ := ReadState(s.o.DataDir); p != nil && p.To == s.o.Version && !p.RolledBack {
			st.Step = "verify" // this is the new version, not yet running long enough
		}
	}
	return st, err
}

func (s *Service) saveStatus(ctx context.Context, change func(*Status)) error {
	var st Status
	if _, err := s.o.Store.GetSetting(ctx, SettingStatus, &st); err != nil {
		return err
	}
	change(&st)
	st.Step = ""
	if err := s.o.Store.SetSetting(ctx, SettingStatus, st); err != nil {
		return err
	}
	s.publish()
	return nil
}

func (s *Service) setStep(step string) {
	s.stepMu.Lock()
	s.step = step
	s.stepMu.Unlock()
	s.publish()
}

// UpdateAvailable reports a newer release, for the admin badge.
func (s *Service) UpdateAvailable(ctx context.Context) bool {
	st, err := s.Status(ctx)
	return err == nil && st.Available != nil
}

// Blocked returns why the available update can't be installed right now (nil = it can).
func (s *Service) Blocked(ctx context.Context) error {
	if !s.Enabled() {
		return ErrDisabled
	}
	st, err := s.Status(ctx)
	if err != nil {
		return err
	}
	if st.Available == nil {
		return ErrNothing
	}
	if st.Step != "" {
		return ErrRunning
	}
	if st.Available.MinVersion != "" && release.Newer(st.Available.MinVersion, s.o.Version) {
		return ErrTooOld
	}
	if err := s.installed(); err != nil {
		return err
	}
	if s.o.Busy() {
		return ErrBusy
	}
	return nil
}

// --- the data folder layout ----------------------------------------------------------------

func (s *Service) binDir() string { return filepath.Join(s.o.DataDir, "bin") }

// Current is the path systemd runs: <data>/bin/current.
func Current(dataDir string) string { return filepath.Join(dataDir, "bin", "current") }

// RunningFromCurrent reports whether this process runs <data>/bin/current (then a restart
// re-executes that path, which may point to a new version).
func RunningFromCurrent(dataDir string) bool {
	exe, err := os.Executable()
	if err != nil {
		return false
	}
	return samePath(exe, Current(dataDir))
}

func samePath(a, b string) bool {
	ra, err1 := filepath.EvalSymlinks(a)
	rb, err2 := filepath.EvalSymlinks(b)
	return err1 == nil && err2 == nil && ra == rb
}

// installed checks the layout: this process runs from <data>/bin/current.
func (s *Service) installed() error {
	if !s.fromCurrent() {
		return ErrNotInstalled
	}
	return nil
}

// switchLink points link at target (a file name in bin/), atomically.
func switchLink(dir, link, target string) error {
	tmp := filepath.Join(dir, "."+link+".new")
	_ = os.Remove(tmp)
	if err := os.Symlink(target, tmp); err != nil {
		return err
	}
	return os.Rename(tmp, filepath.Join(dir, link))
}

// --- check ---------------------------------------------------------------------------------

type ghRelease struct {
	Tag        string `json:"tag_name"`
	Draft      bool   `json:"draft"`
	Prerelease bool   `json:"prerelease"`
	HTMLURL    string `json:"html_url"`
	Assets     []struct {
		Name string `json:"name"`
		URL  string `json:"browser_download_url"`
	} `json:"assets"`
}

// Check asks GitHub for the newest release (pre-releases are skipped), verifies its
// release.json and remembers it if it is newer than the running version.
func (s *Service) Check(ctx context.Context) (Status, error) {
	if !s.Enabled() {
		return Status{}, ErrDisabled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	avail, err := s.check(ctx)
	now := s.o.Now()
	if serr := s.saveStatus(ctx, func(st *Status) {
		st.LastCheck = now
		st.CheckError = ""
		if err != nil {
			st.CheckError = err.Error()
			return
		}
		st.Available = avail
	}); serr != nil {
		return Status{}, serr
	}
	st, serr := s.Status(ctx)
	if err == nil {
		err = serr
	}
	if err != nil {
		s.o.Logger.Warn("update check", "err", err)
	} else if st.Available != nil {
		s.o.Logger.Info("update available", "version", st.Available.Version)
	}
	return st, err
}

func (s *Service) check(ctx context.Context) (*Available, error) {
	rel, err := s.latest(ctx)
	if err != nil {
		return nil, err
	}
	m, err := s.manifest(ctx, rel)
	if err != nil {
		return nil, err
	}
	if !release.Newer(m.Version, s.o.Version) {
		return nil, nil
	}
	return &Available{Version: m.Version, Tag: rel.Tag, Date: m.Date, Notes: m.Notes, MinVersion: m.MinVersion, URL: rel.HTMLURL}, nil
}

func (s *Service) get(ctx context.Context, url string, limit int64, accept string) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "kicker/"+s.o.Version)
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return nil, fmt.Errorf("couldn't reach GitHub: %w", err)
	}
	defer resp.Body.Close()
	switch {
	case resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests:
		return nil, ErrGitHubBusy
	case resp.StatusCode == http.StatusNotFound:
		return nil, fmt.Errorf("%s: not found", url)
	case resp.StatusCode != http.StatusOK:
		return nil, fmt.Errorf("%s: %s", url, resp.Status)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, err
	}
	if int64(len(data)) > limit {
		return nil, fmt.Errorf("%s: larger than %d bytes", url, limit)
	}
	return data, nil
}

func (s *Service) latest(ctx context.Context) (ghRelease, error) {
	// /releases/latest is the newest release that is neither a draft nor a pre-release.
	data, err := s.get(ctx, strings.TrimRight(s.o.API, "/")+"/repos/"+s.o.Repo+"/releases/latest", 1<<20, "application/vnd.github+json")
	if err != nil {
		return ghRelease{}, err
	}
	var rel ghRelease
	if err := json.Unmarshal(data, &rel); err != nil {
		return ghRelease{}, fmt.Errorf("GitHub's answer: %w", err)
	}
	return rel, nil
}

func (s *Service) manifest(ctx context.Context, rel ghRelease) (release.Manifest, error) {
	urls := map[string]string{}
	for _, a := range rel.Assets {
		urls[a.Name] = a.URL
	}
	if urls[release.ManifestFile] == "" || urls[release.SignatureFile] == "" {
		return release.Manifest{}, fmt.Errorf("release %s has no signed release.json", rel.Tag)
	}
	data, err := s.get(ctx, urls[release.ManifestFile], 1<<20, "")
	if err != nil {
		return release.Manifest{}, err
	}
	sig, err := s.get(ctx, urls[release.SignatureFile], 4<<10, "")
	if err != nil {
		return release.Manifest{}, err
	}
	m, err := release.Verify(data, sig, s.o.PublicKey)
	if err != nil {
		return release.Manifest{}, err
	}
	if m.Version != strings.TrimPrefix(rel.Tag, "v") {
		return release.Manifest{}, fmt.Errorf("release %s: release.json is for version %s", rel.Tag, m.Version)
	}
	return m, nil
}

// --- install -------------------------------------------------------------------------------

// Install starts installing the available release in the background: backup, download, check,
// switch, restart. Errors before the restart are reported in the status (and returned when they
// happen before anything was started).
func (s *Service) Install(ctx context.Context) error {
	if err := s.Blocked(ctx); err != nil {
		return err
	}
	if !s.mu.TryLock() {
		return ErrRunning
	}
	s.setStep("backup")
	go func() {
		defer s.mu.Unlock()
		// not the request's context: the installation goes on when the browser leaves
		bctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		binary, err := s.install(bctx)
		if err != nil {
			s.o.Logger.Error("update failed", "err", err)
			st, _ := s.Status(bctx)
			to := ""
			if st.Available != nil {
				to = st.Available.Version
			}
			_ = s.saveStatus(bctx, func(st *Status) {
				st.Last = &Result{At: s.o.Now(), From: s.o.Version, To: to, Error: err.Error()}
			})
			s.setStep("")
			return
		}
		s.setStep("restart")
		s.o.Logger.Info("update installed, restarting", "binary", binary)
		if s.o.Restart != nil {
			s.o.Restart(binary)
		}
	}()
	return nil
}

func (s *Service) install(ctx context.Context) (string, error) {
	rel, err := s.latest(ctx)
	if err != nil {
		return "", err
	}
	m, err := s.manifest(ctx, rel)
	if err != nil {
		return "", err
	}
	if !release.Newer(m.Version, s.o.Version) {
		return "", ErrNothing
	}
	if m.MinVersion != "" && release.Newer(m.MinVersion, s.o.Version) {
		return "", ErrTooOld
	}
	bin, ok := m.ServerFor(runtime.GOOS, runtime.GOARCH)
	if !ok {
		return "", fmt.Errorf("release %s has no server for %s/%s", m.Version, runtime.GOOS, runtime.GOARCH)
	}
	if s.o.Busy() {
		return "", ErrBusy
	}

	// 1. the backup the rollback restores
	backupFile, err := backup.Before(ctx, s.o.Store, s.o.DataDir, backup.KindPreUpdate)
	if err != nil {
		return "", fmt.Errorf("backup before the update: %w", err)
	}
	if backupFile == "" {
		return "", errors.New("no backup passphrase yet – finish the setup first")
	}

	// 2. download and check
	s.setStep("download")
	dir := s.binDir()
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	name := "kicker-" + m.Version
	path := filepath.Join(dir, name)
	if err := s.download(ctx, bin.Asset, path); err != nil {
		return "", err
	}
	s.setStep("check")
	if err := preflight(ctx, path, m.Version); err != nil {
		return "", err
	}

	// 3. switch: previous → what runs now, current → the new version
	running, err := os.Readlink(Current(s.o.DataDir))
	if err != nil {
		return "", err
	}
	state := State{From: s.o.Version, To: m.Version, Backup: backupFile, Started: s.o.Now(), Previous: running}
	if err := WriteState(s.o.DataDir, state); err != nil {
		return "", err
	}
	if err := switchLink(dir, "previous", running); err != nil {
		_ = removeState(s.o.DataDir)
		return "", err
	}
	if err := switchLink(dir, "current", name); err != nil {
		_ = removeState(s.o.DataDir)
		return "", err
	}
	return Current(s.o.DataDir), nil
}

func (s *Service) download(ctx context.Context, a release.Asset, path string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, a.URL, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", "kicker/"+s.o.Version)
	resp, err := s.o.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("downloading %s: %w", a.Name, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("downloading %s: %s", a.Name, resp.Status)
	}
	part := path + ".part"
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o755)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(resp.Body, a.Size+1))
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(part)
		return err
	}
	if err := a.CheckFile(part); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("%s: %w", a.Name, err)
	}
	return os.Rename(part, path)
}

// preflight runs `<binary> version`: a file that can't run here (wrong architecture, damaged)
// is never switched to.
func preflight(ctx context.Context, binary, want string) error {
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	out, err := exec.CommandContext(cctx, binary, "version").CombinedOutput()
	if err != nil {
		return fmt.Errorf("the new version doesn't run on this kicker: %w", err)
	}
	if !strings.HasPrefix(string(out), "kicker "+want+" ") {
		return fmt.Errorf("the new binary reports %q, not %s", strings.TrimSpace(string(out)), want)
	}
	return nil
}

// --- after the restart ---------------------------------------------------------------------

// State is update.json: an update in progress.
type State struct {
	From     string    `json:"from"`
	To       string    `json:"to"`
	Previous string    `json:"previous"` // file name in bin/ the rollback returns to
	Backup   string    `json:"backup"`   // the pre-update backup the rollback restores
	Started  time.Time `json:"started"`
	// Attempt counts starts of the new version; a second start before it was healthy means the
	// first one crashed or was stopped: roll back.
	Attempt    int    `json:"attempt"`
	RolledBack bool   `json:"rolled_back,omitempty"`
	Error      string `json:"error,omitempty"`
}

const stateFile = "update.json"

// ReadState reads update.json (nil if there is none).
func ReadState(dataDir string) (*State, error) {
	data, err := os.ReadFile(filepath.Join(dataDir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	var st State
	if err := json.Unmarshal(data, &st); err != nil {
		return nil, fmt.Errorf("%s: %w", stateFile, err)
	}
	return &st, nil
}

// WriteState writes update.json.
func WriteState(dataDir string, st State) error {
	data, _ := json.MarshalIndent(st, "", "  ")
	path := filepath.Join(dataDir, stateFile)
	if err := os.WriteFile(path+".tmp", data, 0o600); err != nil {
		return err
	}
	return os.Rename(path+".tmp", path)
}

func removeState(dataDir string) error {
	err := os.Remove(filepath.Join(dataDir, stateFile))
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	return err
}

// Startup runs first thing in `kicker serve`, before the database is opened:
//   - the new version starting for the first time: count the attempt and go on (pending);
//   - the new version starting again before it was healthy: roll back – stage the pre-update
//     backup, point current at the previous version, and return it to re-execute;
//   - the old version after a rollback: nothing to do here; the Service reports it.
func Startup(ctx context.Context, dataDir, running string, log *slog.Logger) (reexec string, pending bool, err error) {
	st, err := ReadState(dataDir)
	if err != nil || st == nil || st.RolledBack || running != st.To {
		return "", false, err
	}
	if st.Attempt == 0 {
		st.Attempt = 1
		return "", true, WriteState(dataDir, *st)
	}
	log.Error("the new version stopped before it was running properly – rolling back", "from", st.From, "to", st.To)
	if err := Rollback(ctx, dataDir, st); err != nil {
		return "", false, fmt.Errorf("rolling back to %s: %w", st.From, err)
	}
	st.RolledBack = true
	st.Error = fmt.Sprintf("%s stopped before it was running properly; %s and the data from before the update were restored", st.To, st.From)
	if err := WriteState(dataDir, *st); err != nil {
		return "", false, err
	}
	return Current(dataDir), false, nil
}

// Rollback stages the pre-update backup (swapped in at the next start) and points current at the
// previous version.
func Rollback(ctx context.Context, dataDir string, st *State) error {
	if st.Backup != "" {
		f, err := os.Open(st.Backup)
		if err != nil {
			return err
		}
		defer f.Close()
		pass, err := os.ReadFile(filepath.Join(dataDir, "backup.passphrase"))
		if err != nil {
			return err
		}
		if _, err := backup.Stage(ctx, dataDir, f, strings.TrimSpace(string(pass))); err != nil {
			return fmt.Errorf("the backup before the update: %w", err)
		}
	}
	prev := st.Previous
	if prev == "" {
		var err error
		if prev, err = os.Readlink(filepath.Join(dataDir, "bin", "previous")); err != nil {
			return err
		}
	}
	return switchLink(filepath.Join(dataDir, "bin"), "current", prev)
}

// Started is called once the server runs (database open, table restored, listening). It reports
// a rollback that just happened, and for a new version it waits HealthDelay and then marks the
// update as done.
func (s *Service) Started(ctx context.Context) {
	st, err := ReadState(s.o.DataDir)
	if err != nil || st == nil {
		return
	}
	switch {
	case st.RolledBack && s.o.Version != st.To:
		_ = s.saveStatus(ctx, func(x *Status) {
			x.Last = &Result{At: s.o.Now(), From: st.From, To: st.To, Error: st.Error}
		})
		_ = removeState(s.o.DataDir)
	case st.To == s.o.Version && !st.RolledBack:
		go func() {
			select {
			case <-ctx.Done():
				return
			case <-time.After(s.o.HealthDelay):
			}
			s.healthy(context.WithoutCancel(ctx), st)
		}()
	default:
		// left over from an update that never got to its new version
		s.o.Logger.Warn("dropping an unfinished update", "from", st.From, "to", st.To)
		_ = s.saveStatus(ctx, func(x *Status) {
			x.Last = &Result{At: s.o.Now(), From: st.From, To: st.To, Error: fmt.Sprintf("%s didn't start", st.To)}
		})
		_ = removeState(s.o.DataDir)
	}
}

// Stopping is called before a planned restart or shutdown: a new version that has been running
// for a while is healthy, even if the 60 s are not over.
func (s *Service) Stopping(ctx context.Context) {
	st, err := ReadState(s.o.DataDir)
	if err == nil && st != nil && st.To == s.o.Version && !st.RolledBack && s.o.Now().Sub(s.started) >= 10*time.Second {
		s.healthy(ctx, st)
	}
}

func (s *Service) healthy(ctx context.Context, st *State) {
	if cur, err := ReadState(s.o.DataDir); err != nil || cur == nil || cur.To != st.To {
		return
	}
	_ = s.saveStatus(ctx, func(x *Status) {
		x.Last = &Result{At: s.o.Now(), From: st.From, To: st.To, OK: true}
		x.Available = nil
	})
	s.prune()
	// last: without update.json the next start is an ordinary one
	if err := removeState(s.o.DataDir); err != nil {
		s.o.Logger.Error("finishing the update", "err", err)
		return
	}
	s.o.Logger.Info("update finished", "from", st.From, "to", st.To)
}

// prune keeps the versions current and previous point at.
func (s *Service) prune() {
	dir := s.binDir()
	keep := map[string]bool{"current": true, "previous": true}
	for _, l := range []string{"current", "previous"} {
		if t, err := os.Readlink(filepath.Join(dir, l)); err == nil {
			keep[filepath.Base(t)] = true
		}
	}
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		if strings.HasPrefix(e.Name(), "kicker-") && !keep[e.Name()] {
			_ = os.Remove(filepath.Join(dir, e.Name()))
		}
	}
}

// --- schedule ------------------------------------------------------------------------------

// Run checks once a day (if enabled) and installs automatically at the backup time when the
// table is idle (if enabled).
func (s *Service) Run(ctx context.Context) {
	if !s.Enabled() {
		return
	}
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			s.tick(ctx)
		}
	}
}

func (s *Service) tick(ctx context.Context) {
	if !s.o.Ready() {
		return
	}
	cfg, err := s.Config(ctx)
	if err != nil {
		return
	}
	st, err := s.Status(ctx)
	if err != nil || st.Step != "" {
		return
	}
	now := s.o.Now()
	if cfg.Check && now.Sub(st.LastCheck) >= 24*time.Hour {
		st, _ = s.Check(ctx)
	}
	// a version that failed and was rolled back is not tried again automatically
	failed := st.Available != nil && st.Last != nil && !st.Last.OK && st.Last.To == st.Available.Version
	if cfg.Auto && st.Available != nil && !failed && s.inWindow(now) && s.Blocked(ctx) == nil {
		s.o.Logger.Info("installing the update automatically", "version", st.Available.Version)
		if err := s.Install(ctx); err != nil {
			s.o.Logger.Warn("automatic update", "err", err)
		}
	}
}

// inWindow: within three hours after the daily backup time (local time).
func (s *Service) inWindow(now time.Time) bool {
	at, err := time.ParseInLocation("15:04", s.o.Time(), now.Location())
	if err != nil {
		return false
	}
	start := time.Date(now.Year(), now.Month(), now.Day(), at.Hour(), at.Minute(), 0, 0, now.Location())
	if now.Before(start) {
		start = start.AddDate(0, 0, -1)
	}
	return now.Sub(start) < 3*time.Hour
}

// InstallNow installs the newest release right away (`kicker update` in a shell): it doesn't
// check the table or restart anything – the caller restarts the service afterwards.
func (s *Service) InstallNow(ctx context.Context) (string, error) {
	if !s.Enabled() {
		return "", ErrDisabled
	}
	if _, err := os.Readlink(Current(s.o.DataDir)); err != nil {
		return "", ErrNotInstalled
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	st, err := s.install(ctx)
	if err != nil {
		return "", err
	}
	return st, nil
}

// NewestBackup returns the newest pre-update backup ("" if there is none).
func NewestBackup(dataDir string) string {
	entries, _ := os.ReadDir(filepath.Join(dataDir, "backups", backup.KindPreUpdate))
	newest := ""
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".age") && e.Name() > newest {
			newest = e.Name()
		}
	}
	if newest == "" {
		return ""
	}
	return filepath.Join(dataDir, "backups", backup.KindPreUpdate, newest)
}
