package backup

import (
	"context"
	"errors"
	"fmt"
	"hash/fnv"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/emergency"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Settings keys (values JSON).
const (
	SettingConfig = "backup.config"
	SettingStatus = "backup.status"
)

// Topic is the SSE topic for backup runs and changes to the list (admins).
const Topic = "backup"

// Location kinds (spec 10 §2): every backup goes to each enabled location.
const (
	LocLocal  = "local"  // <data>/backups
	LocUSB    = "usb"    // a drive under USBRoot
	LocFolder = "folder" // any folder, e.g. a network share mounted by the OS
)

// Schedules.
const (
	ScheduleDaily  = "daily"
	ScheduleDays   = "days" // on the weekdays in Days
	ScheduleManual = "manual"
)

// Config is the backup cycle and the locations (spec 10 §2, like Home Assistant's).
type Config struct {
	Schedule string `json:"schedule"`
	Days     []int  `json:"days,omitempty"` // 0 = Sunday … 6 = Saturday
	// Time is HH:MM, or "" for automatic: a fixed time between 03:00 and 05:00 picked from the
	// kicker's id, so kickers on one network don't all back up at once.
	Time string `json:"time"`
	// Retention of automatic backups (manual, uploaded and the ones before updates are kept until
	// deleted): the newest Copies, or – with Days > 0 – the ones younger than Days.
	Copies    int        `json:"copies"`
	KeepDays  int        `json:"keep_days,omitempty"`
	Locations []Location `json:"locations"`
}

// Location is a folder backups are written to.
type Location struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Kind    string `json:"kind"`
	Path    string `json:"path"` // usb: the drive's folder under USBRoot; folder: an absolute path; local: ""
	Enabled bool   `json:"enabled"`
}

// DefaultConfig: daily at the automatic time, keeping 7, on this kicker.
func DefaultConfig() Config {
	return Config{Schedule: ScheduleDaily, Copies: 7, Locations: []Location{{ID: "local", Kind: LocLocal, Enabled: true}}}
}

// Status is the last result per location and of the schedule.
type Status struct {
	Locations     map[string]Result `json:"locations"`
	LastScheduled string            `json:"last_scheduled,omitempty"` // the day (YYYY-MM-DD) of the last scheduled run
	LastRun       *time.Time        `json:"last_run,omitempty"`
}

// Result of writing a backup to one location.
type Result struct {
	At     time.Time  `json:"at"`
	File   string     `json:"file,omitempty"`
	Error  string     `json:"error,omitempty"`
	LastOK *time.Time `json:"last_ok,omitempty"` // the last successful backup there
}

// Entry is a backup in the list: one file name, found in one or more locations.
type Entry struct {
	File      string    `json:"file"`
	Kicker    string    `json:"kicker"` // the name part of the file name; another kicker's backups can be restored too
	Own       bool      `json:"own"`    // made by this kicker (by name)
	Created   time.Time `json:"created"`
	Kind      string    `json:"kind"`
	Size      int64     `json:"size"`
	Locations []string  `json:"locations"` // location ids
}

// Options for New.
type Options struct {
	Store    *store.Store
	DataDir  string
	ServerID string
	Name     func() string // the kicker's name (backup file names)
	Busy     func() bool   // a game is being played: the scheduled backup waits
	Ready    func() bool   // setup is complete; before that there is nothing to back up
	Bus      *bus.Bus
	Logger   *slog.Logger
	Now      func() time.Time
	USBRoot  string // where USB drives are mounted (default /media/kicker)
	// Mounted reports whether a folder under USBRoot is a mounted drive (default: a mount point;
	// tests pass any folder).
	Mounted func(dir string) bool
}

// Service runs scheduled and on-demand backups and keeps the list.
type Service struct {
	o  Options
	mu sync.Mutex // one backup at a time
}

// New makes the backup service.
func New(o Options) *Service {
	if o.Now == nil {
		o.Now = time.Now
	}
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Mounted == nil {
		o.Mounted = isMountPoint
	}
	if o.USBRoot == "" {
		o.USBRoot = "/media/kicker"
	}
	if o.Busy == nil {
		o.Busy = func() bool { return false }
	}
	if o.Ready == nil {
		o.Ready = func() bool { return true }
	}
	return &Service{o: o}
}

func (s *Service) publish() {
	if s.o.Bus != nil {
		s.o.Bus.Publish(Topic, nil)
	}
}

// Config returns the configuration (the default until it is saved).
func (s *Service) Config(ctx context.Context) (Config, error) {
	c := DefaultConfig()
	_, err := s.o.Store.GetSetting(ctx, SettingConfig, &c)
	// The kicker's own location is always there (it can only be switched off).
	if !slices.ContainsFunc(c.Locations, func(l Location) bool { return l.Kind == LocLocal }) {
		c.Locations = append([]Location{{ID: "local", Kind: LocLocal, Enabled: true}}, c.Locations...)
	}
	if c.Schedule == "" {
		c.Schedule = ScheduleDaily
	}
	if c.Copies < 1 && c.KeepDays < 1 {
		c.Copies = 7
	}
	return c, err
}

var hhmm = regexp.MustCompile(`^([01]\d|2[0-3]):[0-5]\d$`)

// ErrConfig: an invalid configuration.
var ErrConfig = errors.New("invalid backup configuration")

// SetConfig checks and saves the configuration.
func (s *Service) SetConfig(ctx context.Context, c Config) error {
	switch c.Schedule {
	case ScheduleDaily, ScheduleManual:
		c.Days = nil
	case ScheduleDays:
		if len(c.Days) == 0 {
			return fmt.Errorf("%w: pick at least one day", ErrConfig)
		}
		for _, d := range c.Days {
			if d < 0 || d > 6 {
				return fmt.Errorf("%w: day %d", ErrConfig, d)
			}
		}
		slices.Sort(c.Days)
		c.Days = slices.Compact(c.Days)
	default:
		return fmt.Errorf("%w: schedule %q", ErrConfig, c.Schedule)
	}
	if c.Time != "" && !hhmm.MatchString(c.Time) {
		return fmt.Errorf("%w: time %q is not HH:MM", ErrConfig, c.Time)
	}
	if c.KeepDays < 0 || c.KeepDays > 3650 || c.KeepDays == 0 && (c.Copies < 1 || c.Copies > 1000) {
		return fmt.Errorf("%w: keep 1–1000 backups or 1–3650 days", ErrConfig)
	}
	seen := map[string]bool{}
	local := 0
	for i := range c.Locations {
		l := &c.Locations[i]
		l.Name = strings.TrimSpace(l.Name)
		if l.ID == "" || seen[l.ID] || len(l.ID) > 40 {
			return fmt.Errorf("%w: every location needs its own id", ErrConfig)
		}
		seen[l.ID] = true
		switch l.Kind {
		case LocLocal:
			l.Path = ""
			local++
		case LocUSB:
			if l.Path == "" || strings.ContainsAny(l.Path, `/\`) || l.Path == "." || l.Path == ".." {
				return fmt.Errorf("%w: a USB location names its drive", ErrConfig)
			}
		case LocFolder:
			if !filepath.IsAbs(l.Path) {
				return fmt.Errorf("%w: %q is not an absolute path", ErrConfig, l.Path)
			}
			l.Path = filepath.Clean(l.Path)
		default:
			return fmt.Errorf("%w: kind %q", ErrConfig, l.Kind)
		}
	}
	if local != 1 {
		return fmt.Errorf("%w: the kicker's own location can be switched off, not removed", ErrConfig)
	}
	if err := s.o.Store.SetSetting(ctx, SettingConfig, c); err != nil {
		return err
	}
	s.publish()
	return nil
}

// AutoTime is the backup time of the automatic setting: between 03:00 and 04:59, from the id.
func (s *Service) AutoTime() string {
	h := fnv.New32a()
	_, _ = h.Write([]byte(s.o.ServerID))
	m := int(h.Sum32() % 120)
	return fmt.Sprintf("%02d:%02d", 3+m/60, m%60)
}

// Time is the daily backup time (HH:MM, local time) as configured.
func (s *Service) Time(ctx context.Context) string {
	c, _ := s.Config(ctx)
	return s.timeOf(c)
}

func (s *Service) timeOf(c Config) string {
	if c.Time != "" {
		return c.Time
	}
	return s.AutoTime()
}

// Status returns the last results.
func (s *Service) Status(ctx context.Context) (Status, error) {
	st := Status{Locations: map[string]Result{}}
	_, err := s.o.Store.GetSetting(ctx, SettingStatus, &st)
	if st.Locations == nil {
		st.Locations = map[string]Result{}
	}
	return st, err
}

// Problem reports whether admins should be told: an enabled location failed last time, or – with a
// schedule – has no backup younger than 48 h (a weekly schedule: 8 days).
func (s *Service) Problem(ctx context.Context) bool {
	c, err := s.Config(ctx)
	if err != nil {
		return true
	}
	st, err := s.Status(ctx)
	if err != nil {
		return true
	}
	maxAge := 48 * time.Hour
	if c.Schedule == ScheduleDays {
		maxAge = 8 * 24 * time.Hour
	}
	for _, l := range c.Locations {
		if !l.Enabled {
			continue
		}
		r := st.Locations[l.ID]
		if r.Error != "" || c.Schedule != ScheduleManual && (r.LastOK == nil || s.o.Now().Sub(*r.LastOK) > maxAge) {
			return true
		}
	}
	return false
}

// Drives lists the USB drives that can be a location (folders under USBRoot).
func (s *Service) Drives() []string {
	es, err := os.ReadDir(s.o.USBRoot)
	if err != nil {
		return []string{}
	}
	out := []string{}
	for _, e := range es {
		if e.IsDir() && s.o.Mounted(filepath.Join(s.o.USBRoot, e.Name())) {
			out = append(out, e.Name())
		}
	}
	return out
}

// Dir is where a location writes.
func (s *Service) Dir(l Location) string {
	switch l.Kind {
	case LocLocal:
		return filepath.Join(s.o.DataDir, "backups")
	case LocUSB:
		return filepath.Join(s.o.USBRoot, l.Path)
	}
	return l.Path
}

// localDirs are the folders of the local location: the scheduled and manual backups, and the
// ones kept before updates and migrations and the uploaded ones.
func (s *Service) localDirs() []string {
	base := filepath.Join(s.o.DataDir, "backups")
	return []string{base, filepath.Join(base, KindPreUpdate), filepath.Join(base, KindPreMigration), filepath.Join(base, "uploads")}
}

func (s *Service) name() string {
	name := ""
	if s.o.Name != nil {
		name = s.o.Name()
	}
	if name == "" {
		name = DefaultName
	}
	return name
}

func (s *Service) source() Source {
	return Source{Store: s.o.Store, DataDir: s.o.DataDir, Name: s.name(), ServerID: s.o.ServerID}
}

// Download writes a fresh backup to w (encrypted like every backup).
func (s *Service) Download(ctx context.Context, w io.Writer) (Manifest, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return Write(ctx, w, s.source())
}

// Filename is the name of a manual backup made now.
func (s *Service) Filename() string { return Filename(s.name(), s.o.Now(), KindManual) }

// RunNow makes a manual backup and writes it to every enabled location.
func (s *Service) RunNow(ctx context.Context) (Status, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.run(ctx, KindManual)
}

func (s *Service) run(ctx context.Context, kind string) (Status, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return Status{}, err
	}
	st, err := s.Status(ctx)
	if err != nil {
		return st, err
	}
	now := s.o.Now()
	if kind == KindAuto {
		st.LastScheduled = now.Format(time.DateOnly)
	}
	st.LastRun = &now
	src := s.source()
	file := Filename(src.Name, now, kind)

	// One backup, copied to each location.
	tmp, err := os.CreateTemp(s.o.DataDir, ".backup-*.age")
	if err != nil {
		return st, err
	}
	defer os.Remove(tmp.Name())
	_, err = Write(ctx, tmp, src)
	err = errors.Join(err, tmp.Close())
	for _, l := range c.Locations {
		if !l.Enabled {
			continue
		}
		r := st.Locations[l.ID]
		r.At, r.File, r.Error = now, "", ""
		if err != nil {
			r.Error = err.Error()
		} else if cerr := s.copyTo(tmp.Name(), l, file); cerr != nil {
			r.Error = cerr.Error()
		} else {
			r.File, r.LastOK = file, &now
			s.prune(s.Dir(l), c, src.Name)
		}
		if r.Error != "" {
			s.o.Logger.Warn("backup failed", "location", l.ID, "err", r.Error)
		}
		st.Locations[l.ID] = r
	}
	if serr := s.o.Store.SetSetting(ctx, SettingStatus, st); serr != nil {
		return st, serr
	}
	s.publish()
	s.o.Logger.Info("backup done", "file", file)
	return st, err
}

// copyTo copies a backup into a location (atomically: a .part file renamed).
func (s *Service) copyTo(src string, l Location, file string) error {
	dir := s.Dir(l)
	switch l.Kind {
	case LocUSB:
		if _, err := os.Stat(dir); err != nil || !s.o.Mounted(dir) {
			return fmt.Errorf("drive %q is not plugged in", l.Path)
		}
	case LocFolder:
		if fi, err := os.Stat(dir); err != nil || !fi.IsDir() {
			return fmt.Errorf("the folder %s doesn't exist (is the share mounted?)", dir)
		}
	case LocLocal:
		if err := os.MkdirAll(dir, 0o750); err != nil {
			return err
		}
	}
	_, err := copyFile(src, filepath.Join(dir, file))
	return err
}

func copyFile(src, dst string) (int64, error) {
	in, err := os.Open(src)
	if err != nil {
		return 0, err
	}
	defer in.Close()
	part := dst + ".part"
	out, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return 0, err
	}
	n, err := io.Copy(out, in)
	if err = errors.Join(err, out.Sync(), out.Close()); err != nil {
		_ = os.Remove(part)
		return 0, err
	}
	return n, os.Rename(part, dst)
}

// prune deletes this kicker's automatic backups in dir beyond the retention.
func (s *Service) prune(dir string, c Config, name string) {
	var auto []string
	for _, f := range listDir(dir) {
		p, _ := ParseFilename(f)
		if p.Kind == KindAuto && strings.HasPrefix(f, Prefix(name)) {
			auto = append(auto, f)
		}
	}
	sort.Strings(auto) // oldest first: names sort by time
	now := s.o.Now()
	for i, f := range auto {
		p, _ := ParseFilename(f)
		old := c.KeepDays > 0 && now.Sub(p.Created) > time.Duration(c.KeepDays)*24*time.Hour
		if c.KeepDays == 0 && len(auto)-i > max(c.Copies, 1) || old {
			_ = os.Remove(filepath.Join(dir, f))
		}
	}
}

// listDir lists the backup files in a folder.
func listDir(dir string) []string {
	es, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range es {
		if _, ok := ParseFilename(e.Name()); ok && e.Type().IsRegular() {
			out = append(out, e.Name())
		}
	}
	return out
}

// List returns the backups in all enabled locations (plus the kicker's own folders for the
// ones before updates and migrations and the uploaded ones), newest first.
func (s *Service) List(ctx context.Context) ([]Entry, error) {
	c, err := s.Config(ctx)
	if err != nil {
		return nil, err
	}
	byFile := map[string]*Entry{}
	own := strings.TrimSuffix(strings.TrimPrefix(Prefix(s.name()), "kicker-backup-"), "-")
	add := func(dir, loc string) {
		for _, f := range listDir(dir) {
			e := byFile[f]
			if e == nil {
				p, _ := ParseFilename(f)
				e = &Entry{File: f, Kicker: p.Kicker, Own: p.Kicker == own, Created: p.Created, Kind: p.Kind, Locations: []string{}}
				if fi, err := os.Stat(filepath.Join(dir, f)); err == nil {
					e.Size = fi.Size()
				}
				byFile[f] = e
			}
			if !slices.Contains(e.Locations, loc) {
				e.Locations = append(e.Locations, loc)
			}
		}
	}
	for _, l := range c.Locations {
		if l.Kind == LocLocal {
			for _, d := range s.localDirs() {
				add(d, l.ID)
			}
		} else if l.Enabled {
			add(s.Dir(l), l.ID)
		}
	}
	out := make([]Entry, 0, len(byFile))
	for _, e := range byFile {
		out = append(out, *e)
	}
	sort.Slice(out, func(i, j int) bool {
		if !out[i].Created.Equal(out[j].Created) {
			return out[i].Created.After(out[j].Created)
		}
		return out[i].File < out[j].File
	})
	return out, nil
}

// ErrNotFound: no such backup in the locations.
var ErrNotFound = errors.New("no such backup")

// Path finds a backup file: in the location given, or the kicker's own folders first.
func (s *Service) Path(ctx context.Context, file, location string) (string, error) {
	if _, ok := ParseFilename(file); !ok {
		return "", ErrNotFound
	}
	c, err := s.Config(ctx)
	if err != nil {
		return "", err
	}
	for _, l := range c.Locations {
		if location != "" && l.ID != location {
			continue
		}
		dirs := []string{s.Dir(l)}
		if l.Kind == LocLocal {
			dirs = s.localDirs()
		}
		for _, d := range dirs {
			p := filepath.Join(d, file)
			if fi, err := os.Stat(p); err == nil && fi.Mode().IsRegular() {
				return p, nil
			}
		}
	}
	return "", ErrNotFound
}

// Delete removes a backup from one location, or from all ("").
func (s *Service) Delete(ctx context.Context, file, location string) error {
	deleted := false
	for {
		p, err := s.Path(ctx, file, location)
		if errors.Is(err, ErrNotFound) {
			break
		} else if err != nil {
			return err
		}
		if err := os.Remove(p); err != nil {
			return err
		}
		deleted = true
	}
	if !deleted {
		return ErrNotFound
	}
	s.publish()
	return nil
}

// Restore checks a stored backup and stages it (spec 10 §2). An empty passphrase tries the
// current one: a backup of this kicker opens with it unless the passphrase changed since.
func (s *Service) Restore(ctx context.Context, file, passphrase string) (Manifest, error) {
	p, err := s.Path(ctx, file, "")
	if err != nil {
		return Manifest{}, err
	}
	if strings.TrimSpace(passphrase) == "" {
		if passphrase, err = emergency.Passphrase(s.o.DataDir); err != nil {
			return Manifest{}, err
		}
	}
	f, err := os.Open(p)
	if err != nil {
		return Manifest{}, err
	}
	defer f.Close()
	return Stage(ctx, s.o.DataDir, f, passphrase)
}

// MaxUpload limits an uploaded backup.
const MaxUpload = 4 << 30

// Upload keeps an uploaded backup in <data>/backups/uploads, under the name it came with if that
// is a backup's name, otherwise as an uploaded backup of now. It returns the file name.
func (s *Service) Upload(r io.Reader, name string) (string, error) {
	name = filepath.Base(name)
	if _, ok := ParseFilename(name); !ok {
		name = Filename("uploaded", s.o.Now(), KindUploaded)
	}
	dir := filepath.Join(s.o.DataDir, "backups", "uploads")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	part := filepath.Join(dir, name+".part")
	f, err := os.OpenFile(part, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	n, err := io.Copy(f, io.LimitReader(r, MaxUpload+1))
	if err == nil && n > MaxUpload {
		err = errors.New("the backup is too large")
	}
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(part)
		return "", err
	}
	if err := os.Rename(part, filepath.Join(dir, name)); err != nil {
		return "", err
	}
	s.publish()
	return name, nil
}

// Run starts the scheduled backups; if a game is on at that time, it waits until the table is
// free. It returns when ctx ends.
func (s *Service) Run(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		s.tick(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// Due reports whether the schedule wants a backup now.
func (s *Service) due(c Config, st Status) bool {
	now := s.o.Now()
	switch c.Schedule {
	case ScheduleManual:
		return false
	case ScheduleDays:
		if !slices.Contains(c.Days, int(now.Weekday())) {
			return false
		}
	}
	return st.LastScheduled != now.Format(time.DateOnly) && now.Format("15:04") >= s.timeOf(c)
}

func (s *Service) tick(ctx context.Context) {
	if !s.o.Ready() {
		return
	}
	c, err := s.Config(ctx)
	if err != nil {
		return
	}
	st, err := s.Status(ctx)
	if err != nil || !s.due(c, st) || s.o.Busy() {
		return
	}
	if !s.mu.TryLock() {
		return
	}
	defer s.mu.Unlock()
	if _, err := s.run(ctx, KindAuto); err != nil {
		s.o.Logger.Warn("scheduled backup", "err", err)
	}
}

// NextRun is when the schedule backs up next (nil: manual only).
func (s *Service) NextRun(c Config) *time.Time {
	if c.Schedule == ScheduleManual {
		return nil
	}
	now := s.o.Now()
	var h, m int
	_, _ = fmt.Sscanf(s.timeOf(c), "%d:%d", &h, &m)
	for d := 0; d < 8; d++ {
		day := now.AddDate(0, 0, d)
		at := time.Date(day.Year(), day.Month(), day.Day(), h, m, 0, 0, now.Location())
		if at.Before(now) || c.Schedule == ScheduleDays && !slices.Contains(c.Days, int(at.Weekday())) {
			continue
		}
		return &at
	}
	return nil
}

// Before writes a backup to <data>/backups/<kind> (pre-migration, pre-update) and keeps the last
// three there. Without a backup passphrase (before onboarding) there is nothing worth keeping.
func Before(ctx context.Context, st *store.Store, dataDir, kind string) (string, error) {
	if _, err := os.Stat(filepath.Join(dataDir, emergency.PassphraseFile)); errors.Is(err, fs.ErrNotExist) {
		return "", nil
	}
	var name string
	if _, err := st.GetSetting(ctx, store.SettingName, &name); err != nil {
		return "", err
	}
	if name == "" {
		name = DefaultName
	}
	id, err := st.ServerID(ctx)
	if err != nil {
		return "", err
	}
	dir := filepath.Join(dataDir, "backups", kind)
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return "", err
	}
	path := filepath.Join(dir, Filename(name, time.Now(), kind))
	f, err := os.OpenFile(path+".part", os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return "", err
	}
	_, err = Write(ctx, f, Source{Store: st, DataDir: dataDir, Name: name, ServerID: id})
	if err = errors.Join(err, f.Close()); err != nil {
		_ = os.Remove(path + ".part")
		return "", err
	}
	if err := os.Rename(path+".part", path); err != nil {
		return "", err
	}
	files := listDir(dir)
	sort.Strings(files)
	for len(files) > 3 {
		_ = os.Remove(filepath.Join(dir, files[0]))
		files = files[1:]
	}
	return path, nil
}
