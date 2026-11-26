// Package firmware updates module firmware over USB (spec 10 §4, spec 01 §9): the running
// server's release lists a .uf2 per module and board; a module with an older version is offered
// the update. Updating sends `update` (the module reboots into its bootloader), waits for the
// RP2 boot drive on the same USB port, writes the file and waits for the module to announce the
// new version. Always started by an admin, one module at a time, only while nobody plays.
package firmware

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/bus"
	"github.com/Humbarrt/kicker/server/internal/modules"
	"github.com/Humbarrt/kicker/server/internal/modules/virtual"
	"github.com/Humbarrt/kicker/server/internal/release"
)

// Topic is the SSE topic for offers, progress and results (admins).
const Topic = "firmware"

// Host is the part of the module host the updater uses.
type Host interface {
	Modules() []modules.ModuleInfo
	EnterBootloader(ctx context.Context, uid string) error
	VirtualModule(uid string) *virtual.Module
}

// Releases gives the running version's release and downloads its files (update.Service).
type Releases interface {
	CurrentRelease(ctx context.Context) (*release.Manifest, error)
	Download(ctx context.Context, a release.Asset, path string) error
}

// Options for New.
type Options struct {
	Host     Host
	Drives   Drives
	Releases Releases
	DataDir  string // firmware files are kept in <data>/firmware
	Bus      *bus.Bus
	Logger   *slog.Logger
	// Busy: a game is being played or about to start – no updates (spec 10 §4).
	Busy func() bool
	// BootWait: how long to wait for the boot drive; BackWait: for the module with its new firmware.
	BootWait, BackWait time.Duration
	// SimStep: how long each step of a simulated update (virtual modules) takes.
	SimStep time.Duration
}

// Offer is a firmware update available for a module.
type Offer struct {
	UID     string `json:"uid"`
	Name    string `json:"name"`
	Current string `json:"current"`
	Version string `json:"version"`
	File    string `json:"file"`
}

// File is a firmware file of the release.
type File struct {
	Name    string `json:"name"`
	MF      string `json:"mf"`
	HW      string `json:"hw"`
	Version string `json:"version"`
}

// Job is one module update: queued, running or finished.
type Job struct {
	UID     string    `json:"uid"`
	Name    string    `json:"name"`
	From    string    `json:"from"`
	To      string    `json:"to"` // "" = an uploaded file
	File    string    `json:"file"`
	Step    string    `json:"step"` // queued, download, bootloader, write, restart, done, failed
	Error   string    `json:"error,omitempty"`
	Log     []string  `json:"log"`
	Started time.Time `json:"started"`
	Ended   time.Time `json:"ended,omitzero"`

	asset *release.Firmware
	data  []byte // an uploaded file
	drive *Drive // flashing a module found in its bootloader
	usb   string // the module's USB port, kept while it is in the bootloader
}

// Status is what the modules page shows.
type Status struct {
	Release string  `json:"release"` // the running version's release ("" = not loaded / dev build)
	Files   []File  `json:"files"`   // its firmware files (for a module waiting in its bootloader)
	Offers  []Offer `json:"offers"`
	Jobs    []Job   `json:"jobs"`
	// Drives: modules waiting in their bootloader that no update is working on.
	Drives []Drive `json:"drives"`
	Busy   bool    `json:"busy"`
}

// Errors.
var (
	ErrBusy     = errors.New("a game is being played – update the firmware while the table is idle or asleep")
	ErrNoOffer  = errors.New("there is no firmware update for this module")
	ErrOffline  = errors.New("the module is not connected")
	ErrNoUF2    = errors.New("this module doesn't update with a .uf2 file")
	ErrNoDrive  = errors.New("no module is waiting in its bootloader there")
	ErrQueued   = errors.New("this module is already being updated")
	ErrNoModule = errors.New("no such module")
)

// Service runs firmware updates.
type Service struct {
	o Options

	mu      sync.Mutex
	jobs    map[string]*Job // by uid (or "drive:<name>"); the latest for each
	queue   []*Job
	running bool
}

// New makes the service.
func New(o Options) *Service {
	if o.Logger == nil {
		o.Logger = slog.Default()
	}
	if o.Busy == nil {
		o.Busy = func() bool { return false }
	}
	if o.BootWait == 0 {
		o.BootWait = 20 * time.Second
	}
	if o.BackWait == 0 {
		o.BackWait = 30 * time.Second
	}
	if o.SimStep == 0 {
		o.SimStep = 1500 * time.Millisecond
	}
	if o.Drives == nil {
		o.Drives = SysDrives{}
	}
	return &Service{o: o, jobs: map[string]*Job{}}
}

func (s *Service) publish() {
	if s.o.Bus != nil {
		s.o.Bus.Publish(Topic, nil)
	}
}

func (s *Service) manifest(ctx context.Context) *release.Manifest {
	if s.o.Releases == nil {
		return nil
	}
	m, err := s.o.Releases.CurrentRelease(ctx)
	if err != nil {
		s.o.Logger.Warn("the release's firmware list", "err", err)
		return nil
	}
	return m
}

// updatable: online, updated with UF2 files.
func updatable(m modules.ModuleInfo) bool { return m.Online && m.Update == "uf2" }

// Offers lists the modules with a newer firmware in the running version's release.
func (s *Service) Offers(ctx context.Context) []Offer {
	man := s.manifest(ctx)
	if man == nil {
		return []Offer{}
	}
	out := []Offer{}
	for _, m := range s.o.Host.Modules() {
		if !updatable(m) {
			continue
		}
		if f, ok := man.FirmwareFor(m.Manifest, m.Hardware); ok && release.Newer(f.FWVer, m.FWVersion) {
			out = append(out, Offer{UID: m.UID, Name: m.Name, Current: m.FWVersion, Version: f.FWVer, File: f.Name})
		}
	}
	return out
}

// Count is the number of offers, for the admin badge.
func (s *Service) Count(ctx context.Context) int { return len(s.Offers(ctx)) }

// Status returns the offers, the jobs and the stray boot drives.
func (s *Service) Status(ctx context.Context) Status {
	st := Status{Offers: s.Offers(ctx), Files: []File{}, Jobs: []Job{}, Drives: []Drive{}, Busy: s.o.Busy()}
	if m := s.manifest(ctx); m != nil {
		st.Release = m.Version
		for _, f := range m.Firmware {
			st.Files = append(st.Files, File{Name: f.Name, MF: f.MF, HW: f.HW, Version: f.FWVer})
		}
	}
	s.mu.Lock()
	used := map[string]bool{}
	for _, j := range s.jobs {
		c := *j
		c.Log = append([]string(nil), j.Log...)
		st.Jobs = append(st.Jobs, c)
		if j.usb != "" && (j.Step == "bootloader" || j.Step == "write") {
			used[j.usb] = true
		}
		if j.drive != nil && j.Step != "done" && j.Step != "failed" {
			used[j.drive.USBPath] = true
		}
	}
	s.mu.Unlock()
	for _, d := range s.o.Drives.List() {
		if !used[d.USBPath] {
			st.Drives = append(st.Drives, d)
		}
	}
	sort.Slice(st.Jobs, func(i, j int) bool { return st.Jobs[i].Started.Before(st.Jobs[j].Started) })
	return st
}

func (s *Service) module(uid string) (modules.ModuleInfo, bool) {
	for _, m := range s.o.Host.Modules() {
		if m.UID == uid {
			return m, true
		}
	}
	return modules.ModuleInfo{}, false
}

// Update queues the release's firmware for each module (one after the other).
func (s *Service) Update(ctx context.Context, uids []string) error {
	if s.o.Busy() {
		return ErrBusy
	}
	offers := map[string]Offer{}
	for _, o := range s.Offers(ctx) {
		offers[o.UID] = o
	}
	man := s.manifest(ctx)
	var jobs []*Job
	for _, uid := range uids {
		o, ok := offers[uid]
		if !ok {
			return fmt.Errorf("%s: %w", uid, ErrNoOffer)
		}
		m, _ := s.module(uid)
		f, _ := man.FirmwareFor(m.Manifest, m.Hardware)
		jobs = append(jobs, &Job{UID: uid, Name: o.Name, From: o.Current, To: o.Version, File: f.Name, asset: &f})
	}
	return s.enqueue(jobs...)
}

// Upload queues an uploaded .uf2 for a module (own builds; admins are warned in the UI).
func (s *Service) Upload(ctx context.Context, uid, filename string, data []byte) error {
	if s.o.Busy() {
		return ErrBusy
	}
	if _, err := ParseUF2(data); err != nil {
		return err
	}
	m, ok := s.module(uid)
	switch {
	case !ok:
		return ErrNoModule
	case !m.Online:
		return ErrOffline
	case m.Update != "uf2":
		return ErrNoUF2
	}
	return s.enqueue(&Job{UID: uid, Name: m.Name, From: m.FWVersion, File: filepath.Base(filename), data: data})
}

// FlashDrive writes a file to a module found in its bootloader: a file of the release (by name)
// or an uploaded one.
func (s *Service) FlashDrive(ctx context.Context, name, file string, data []byte) error {
	if s.o.Busy() {
		return ErrBusy
	}
	var d *Drive
	for _, x := range s.o.Drives.List() {
		if x.Name == name {
			d = &x
			break
		}
	}
	if d == nil {
		return ErrNoDrive
	}
	j := &Job{UID: "drive:" + d.Name, Name: d.Chip + " " + d.USBPath, File: file, drive: d, usb: d.USBPath}
	if data != nil {
		if _, err := ParseUF2(data); err != nil {
			return err
		}
		j.data = data
	} else {
		man := s.manifest(ctx)
		if man == nil {
			return ErrNoOffer
		}
		for _, f := range man.Firmware {
			if f.Name == file {
				j.asset, j.To = &f, f.FWVer
			}
		}
		if j.asset == nil {
			return ErrNoOffer
		}
	}
	return s.enqueue(j)
}

func (s *Service) enqueue(jobs ...*Job) error {
	s.mu.Lock()
	for _, j := range jobs {
		if old := s.jobs[j.UID]; old != nil && old.Step != "done" && old.Step != "failed" {
			s.mu.Unlock()
			return ErrQueued
		}
	}
	now := time.Now()
	for _, j := range jobs {
		j.Step, j.Started, j.Log = "queued", now, []string{}
		s.jobs[j.UID] = j
		s.queue = append(s.queue, j)
	}
	start := !s.running
	s.running = true
	s.mu.Unlock()
	s.publish()
	if start {
		go s.worker()
	}
	return nil
}

func (s *Service) worker() {
	for {
		s.mu.Lock()
		if len(s.queue) == 0 {
			s.running = false
			s.mu.Unlock()
			return
		}
		j := s.queue[0]
		s.queue = s.queue[1:]
		s.mu.Unlock()

		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		err := s.run(ctx, j)
		cancel()
		s.mu.Lock()
		j.Ended = time.Now()
		if err != nil {
			j.Step, j.Error = "failed", err.Error()
			s.o.Logger.Warn("firmware update failed", "module", j.UID, "err", err)
		} else {
			j.Step = "done"
			s.o.Logger.Info("firmware updated", "module", j.UID, "from", j.From, "to", j.To)
		}
		s.mu.Unlock()
		s.publish()
	}
}

func (s *Service) step(j *Job, step, msg string) {
	s.mu.Lock()
	if step != "" {
		j.Step = step
	}
	if msg != "" {
		j.Log = append(j.Log, time.Now().Format("15:04:05")+" "+msg)
	}
	s.mu.Unlock()
	s.publish()
}

// file returns the UF2 to write: uploaded, cached in <data>/firmware or downloaded.
func (s *Service) file(ctx context.Context, j *Job) (UF2, error) {
	if j.data != nil {
		return ParseUF2(j.data)
	}
	s.step(j, "download", "download "+j.asset.Name)
	dir := filepath.Join(s.o.DataDir, "firmware")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return UF2{}, err
	}
	path := filepath.Join(dir, j.asset.Name)
	if j.asset.CheckFile(path) != nil {
		if err := s.o.Releases.Download(ctx, j.asset.Asset, path); err != nil {
			return UF2{}, err
		}
	} else {
		s.step(j, "", "already downloaded")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return UF2{}, err
	}
	return ParseUF2(data)
}

func (s *Service) run(ctx context.Context, j *Job) error {
	u, err := s.file(ctx, j)
	if err != nil {
		return err
	}
	s.step(j, "", fmt.Sprintf("%s: %d blocks for the %s", j.File, u.Blocks, u.Chip()))
	if s.o.Busy() {
		return ErrBusy
	}
	if j.drive != nil {
		return s.write(ctx, j, *j.drive, u)
	}
	m, ok := s.module(j.UID)
	if !ok || !m.Online {
		return ErrOffline
	}
	if m.Virtual {
		return s.simulate(ctx, j)
	}
	j.usb = m.USBPath
	before := map[string]bool{}
	for _, d := range s.o.Drives.List() {
		before[d.Name] = true
	}
	started := time.Now()

	s.step(j, "bootloader", "send update: the module restarts into its bootloader")
	cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	err = s.o.Host.EnterBootloader(cctx, j.UID)
	cancel()
	if err != nil {
		if mm, _ := s.module(j.UID); mm.Online {
			return fmt.Errorf("the module didn't go into its bootloader: %w", err)
		}
		s.step(j, "", "no answer, but the module went away")
	}
	d, err := s.waitDrive(ctx, j.usb, before)
	if err != nil {
		return err
	}
	s.step(j, "", fmt.Sprintf("boot drive %s (%s) on USB %s", d.Dev, d.Chip, d.USBPath))
	if err := s.write(ctx, j, d, u); err != nil {
		return err
	}
	return s.waitBack(ctx, j, started)
}

// write checks the chip and writes the file.
func (s *Service) write(ctx context.Context, j *Job, d Drive, u UF2) error {
	if u.Chip() != d.Chip {
		return fmt.Errorf("the file is for the %s, but the module is an %s – it waits in its bootloader for the right file", u.Chip(), d.Chip)
	}
	s.step(j, "write", "write "+j.File)
	err := s.o.Drives.Write(ctx, d, u)
	if err != nil && j.drive == nil {
		// the module may have rebooted before the last write was confirmed: see if it comes back
		s.step(j, "", "writing: "+err.Error())
		return err
	}
	if err != nil {
		return err
	}
	if j.drive != nil {
		s.step(j, "", "written – the module restarts with the new firmware")
	}
	return nil
}

// waitDrive waits for the module's boot drive: on its USB port, or the only new one.
func (s *Service) waitDrive(ctx context.Context, usb string, before map[string]bool) (Drive, error) {
	deadline := time.Now().Add(s.o.BootWait)
	for {
		var fresh []Drive
		for _, d := range s.o.Drives.List() {
			if usb != "" && d.USBPath == usb {
				return d, nil
			}
			if !before[d.Name] {
				fresh = append(fresh, d)
			}
		}
		if usb == "" && len(fresh) == 1 {
			return fresh[0], nil
		}
		if time.Now().After(deadline) {
			return Drive{}, fmt.Errorf("no boot drive appeared within %s (USB %s)", s.o.BootWait, usb)
		}
		select {
		case <-ctx.Done():
			return Drive{}, ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// waitBack waits for the module to announce itself with the new version.
func (s *Service) waitBack(ctx context.Context, j *Job, since time.Time) error {
	s.step(j, "restart", "waiting for the module")
	deadline := time.Now().Add(s.o.BackWait)
	for {
		if m, ok := s.module(j.UID); ok && m.Online && m.ConnectedAt.After(since) {
			if j.To == "" || m.FWVersion == j.To {
				s.step(j, "", "back with firmware "+m.FWVersion)
				return nil
			}
			return fmt.Errorf("the module came back with firmware %s, not %s", m.FWVersion, j.To)
		}
		if time.Now().After(deadline) {
			return fmt.Errorf("the module didn't come back within %s", s.o.BackWait)
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(250 * time.Millisecond):
		}
	}
}

// simulate runs the steps for a virtual module (the simulator).
func (s *Service) simulate(ctx context.Context, j *Job) error {
	vm := s.o.Host.VirtualModule(j.UID)
	if vm == nil {
		return ErrOffline
	}
	started := time.Now()
	for _, st := range []string{"bootloader", "write"} {
		s.step(j, st, "simulated "+st)
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(s.o.SimStep):
		}
	}
	vm.Flash(j.To)
	return s.waitBack(ctx, j, started)
}
