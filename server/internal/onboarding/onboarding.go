// Package onboarding is the first-boot wizard (spec 03 §12): step 1 (language), step 3 (admin
// account) and step 4 (emergency kit); the other steps follow with their milestones.
package onboarding

import (
	"context"
	"crypto/rand"
	"crypto/subtle"
	"encoding/base64"
	"errors"
	"slices"
	"sync"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Languages the UI and displays can use.
var Languages = []string{"en", "de"}

// claimIdle is how long a browser's claim on the wizard survives without activity. After that
// another browser may take over, so a closed tab can't block the setup forever.
const claimIdle = 15 * time.Minute

// Errors.
var (
	ErrDone         = errors.New("setup is already complete")
	ErrInProgress   = errors.New("setup is in progress in another browser")
	ErrNotClaimed   = errors.New("start the setup first")
	ErrBadLanguage  = errors.New("unsupported language")
	ErrNeedLanguage = errors.New("choose a language first")
	ErrNeedAdmin    = errors.New("create the admin account first")
)

// settingAdminCreated marks that step 3 is done: the wizard goes on with the emergency kit.
const settingAdminCreated = "onboarding_admin_created"

// Options configure the wizard.
type Options struct {
	Store *store.Store
	Auth  *auth.Service
	Now   func() time.Time
	// Calibrate picks the argon2 parameters in step 3; default auth.Calibrate(250 ms).
	Calibrate func() auth.Params
}

// Onboarding tracks the wizard.
type Onboarding struct {
	opts Options

	mu        sync.Mutex
	done      bool
	admin     bool // step 3 done, the kit (step 4) is next
	language  string
	claim     string // the claiming browser's token; "" = unclaimed
	claimSeen time.Time
}

// New loads the wizard state.
func New(ctx context.Context, opts Options) (*Onboarding, error) {
	if opts.Now == nil {
		opts.Now = time.Now
	}
	if opts.Calibrate == nil {
		opts.Calibrate = func() auth.Params { return auth.Calibrate(250 * time.Millisecond) }
	}
	o := &Onboarding{opts: opts}
	if _, err := opts.Store.GetSetting(ctx, store.SettingOnboarded, &o.done); err != nil {
		return nil, err
	}
	if _, err := opts.Store.GetSetting(ctx, store.SettingLanguage, &o.language); err != nil {
		return nil, err
	}
	if _, err := opts.Store.GetSetting(ctx, settingAdminCreated, &o.admin); err != nil {
		return nil, err
	}
	return o, nil
}

// Done reports whether the wizard is complete. While it isn't, the setting is re-read, so
// `kicker admin create` on the console takes effect without a restart.
func (o *Onboarding) Done() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	if !o.done {
		var done bool
		if ok, err := o.opts.Store.GetSetting(context.Background(), store.SettingOnboarded, &done); err == nil && ok && done {
			o.done, o.claim = true, ""
		}
	}
	return o.done
}

// Status is GET /api/onboarding.
type Status struct {
	Done      bool     `json:"done"`
	Mine      bool     `json:"mine"` // this browser runs the wizard
	Busy      bool     `json:"busy"` // another browser runs it
	Step      string   `json:"step"` // language | admin | kit | done
	Language  string   `json:"language"`
	Languages []string `json:"languages"`
}

// Status returns the wizard state as seen by the browser holding token ("" = none).
func (o *Onboarding) Status(token string) Status {
	o.mu.Lock()
	defer o.mu.Unlock()
	st := Status{Done: o.done, Language: o.language, Languages: Languages}
	switch {
	case o.done:
		st.Step = "done"
	case o.language == "":
		st.Step = "language"
	case o.admin:
		st.Step = "kit" // only the new admin, logged in, can see the kit
	default:
		st.Step = "admin"
	}
	if !o.done && !o.admin {
		st.Mine = o.owns(token)
		st.Busy = !st.Mine && o.claimActive()
	}
	return st
}

func (o *Onboarding) claimActive() bool {
	return o.claim != "" && o.opts.Now().Sub(o.claimSeen) < claimIdle
}

func (o *Onboarding) owns(token string) bool {
	return o.claim != "" && subtle.ConstantTimeCompare([]byte(token), []byte(o.claim)) == 1
}

// Start claims the wizard for a browser and returns its token: the same one if it already holds
// the claim, a new one otherwise (spec 03 §12: the first browser wins).
func (o *Onboarding) Start(token string) (string, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return "", ErrDone
	}
	if o.owns(token) {
		o.claimSeen = o.opts.Now()
		return token, nil
	}
	if o.claimActive() {
		return "", ErrInProgress
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	o.claim, o.claimSeen = base64.RawURLEncoding.EncodeToString(b), o.opts.Now()
	return o.claim, nil
}

// check verifies the claim; call with mu held.
func (o *Onboarding) check(token string) error {
	if o.done {
		return ErrDone
	}
	if !o.owns(token) {
		if o.claimActive() {
			return ErrInProgress
		}
		return ErrNotClaimed
	}
	o.claimSeen = o.opts.Now()
	return nil
}

// SetLanguage is step 1.
func (o *Onboarding) SetLanguage(ctx context.Context, token, lang string) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.check(token); err != nil {
		return err
	}
	if !slices.Contains(Languages, lang) {
		return ErrBadLanguage
	}
	if err := o.opts.Store.SetSetting(ctx, store.SettingLanguage, lang); err != nil {
		return err
	}
	o.language = lang
	return nil
}

// CreateAdmin is step 3: it calibrates password hashing on this machine and creates the first
// admin. The caller logs the new admin in; step 4 (the emergency kit) needs that login, not the
// browser's claim, so nobody else on the network can pick the kit up.
func (o *Onboarding) CreateAdmin(ctx context.Context, token, username, password, ip string) (auth.Account, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := o.check(token); err != nil {
		return auth.Account{}, err
	}
	if o.language == "" {
		return auth.Account{}, ErrNeedLanguage
	}
	if o.admin {
		return auth.Account{}, ErrDone
	}
	// Check the input before the slow calibration.
	if _, err := auth.NormalizeUsername(username); err != nil {
		return auth.Account{}, err
	}
	if err := auth.CheckPassword(password); err != nil {
		return auth.Account{}, err
	}
	if err := o.opts.Auth.SetParams(ctx, o.opts.Calibrate()); err != nil {
		return auth.Account{}, err
	}
	a, err := o.opts.Auth.CreateAccount(ctx, auth.NewAccount{Username: username, Password: password, GroupID: o.opts.Auth.BuiltinGroup(auth.GroupAdmin)})
	if err != nil {
		return auth.Account{}, err
	}
	o.opts.Auth.Audit(ctx, &a.ID, ip, "onboarding_admin_created", a.Username, "")
	if err := o.opts.Store.SetSetting(ctx, settingAdminCreated, true); err != nil {
		return a, err
	}
	o.admin, o.claim = true, ""
	return a, nil
}

// AtKit reports whether the wizard waits for step 4, the emergency kit.
func (o *Onboarding) AtKit() bool {
	o.mu.Lock()
	defer o.mu.Unlock()
	return !o.done && o.admin
}

// Finish completes the wizard after step 4. The caller checks that the logged-in admin has
// made the emergency kit.
func (o *Onboarding) Finish(ctx context.Context) error {
	o.mu.Lock()
	defer o.mu.Unlock()
	if o.done {
		return ErrDone
	}
	if !o.admin {
		return ErrNeedAdmin
	}
	return o.finish(ctx)
}

// finish marks the wizard complete; call with mu held.
func (o *Onboarding) finish(ctx context.Context) error {
	if err := o.opts.Store.SetSetting(ctx, store.SettingOnboarded, true); err != nil {
		return err
	}
	o.done, o.claim = true, ""
	return nil
}

// CompleteFromConsole finishes the wizard after `kicker admin create` made an admin on the Pi
// (spec 03 §7, physical access). The language defaults to English.
func CompleteFromConsole(ctx context.Context, st *store.Store) error {
	var lang string
	if ok, err := st.GetSetting(ctx, store.SettingLanguage, &lang); err != nil {
		return err
	} else if !ok {
		if err := st.SetSetting(ctx, store.SettingLanguage, "en"); err != nil {
			return err
		}
	}
	return st.SetSetting(ctx, store.SettingOnboarded, true)
}
