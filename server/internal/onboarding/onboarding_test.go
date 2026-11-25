package onboarding

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

func TestWizard(t *testing.T) {
	ctx := context.Background()
	st, err := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	clock := func() time.Time { return now }
	as, _ := auth.New(ctx, auth.Options{Store: st, Now: clock})
	fast := auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1}
	o, err := New(ctx, Options{Store: st, Auth: as, Now: clock, Calibrate: func() auth.Params { return fast }})
	if err != nil {
		t.Fatal(err)
	}
	if o.Done() || o.Status("").Step != "language" {
		t.Fatalf("fresh: %+v", o.Status(""))
	}
	if err := o.SetLanguage(ctx, "", "en"); !errors.Is(err, ErrNotClaimed) {
		t.Errorf("unclaimed: %v", err)
	}

	a, err := o.Start("")
	if err != nil || a == "" {
		t.Fatal(err)
	}
	if again, _ := o.Start(a); again != a {
		t.Error("the owner keeps its token")
	}
	if _, err := o.Start("other"); !errors.Is(err, ErrInProgress) {
		t.Errorf("second browser: %v", err)
	}
	if st := o.Status("other"); !st.Busy || st.Mine {
		t.Errorf("status for the other browser: %+v", st)
	}
	if err := o.SetLanguage(ctx, "other", "de"); !errors.Is(err, ErrInProgress) {
		t.Errorf("other browser changes: %v", err)
	}
	if _, err := o.CreateAdmin(ctx, a, "anna", "blue tiger dances", ""); !errors.Is(err, ErrNeedLanguage) {
		t.Errorf("admin before language: %v", err)
	}
	if err := o.SetLanguage(ctx, a, "fr"); !errors.Is(err, ErrBadLanguage) {
		t.Errorf("unsupported: %v", err)
	}
	if err := o.SetLanguage(ctx, a, "de"); err != nil {
		t.Fatal(err)
	}

	// an abandoned claim can be taken over after 15 minutes
	now = now.Add(16 * time.Minute)
	b, err := o.Start("")
	if err != nil || b == a {
		t.Fatalf("takeover: %v", err)
	}
	if _, err := o.CreateAdmin(ctx, a, "anna", "blue tiger dances", ""); !errors.Is(err, ErrInProgress) {
		t.Errorf("old owner: %v", err)
	}
	if _, err := o.CreateAdmin(ctx, b, "anna", "qwertyuiop", ""); !errors.Is(err, auth.ErrPasswordCommon) {
		t.Errorf("weak password: %v", err)
	}
	if st := o.Status(b); st.Step != "admin" || st.Language != "de" {
		t.Errorf("step 3: %+v", st)
	}
	acc, err := o.CreateAdmin(ctx, b, "Anna", "blue tiger dances", "1.2.3.4")
	if err != nil || !acc.IsAdmin() || acc.Username != "anna" {
		t.Fatalf("admin: %+v %v", acc, err)
	}
	// Step 4: the emergency kit, for the logged-in admin only (the caller checks the login).
	if o.Done() || o.Status("").Step != "kit" || !o.AtKit() || o.Status(b).Mine {
		t.Errorf("step 4: %+v", o.Status(b))
	}
	if _, err := o.CreateAdmin(ctx, b, "ben", "blue tiger dances", ""); err == nil {
		t.Error("a second admin through the wizard")
	}
	if again, _ := New(ctx, Options{Store: st, Auth: as, Now: clock}); !again.AtKit() {
		t.Error("step 4 survives a restart")
	}
	if err := o.Finish(ctx); err != nil {
		t.Fatal(err)
	}
	if !o.Done() || o.Status("").Step != "done" {
		t.Error("done")
	}
	if as.Params() != fast {
		t.Error("calibrated parameters must be in use")
	}
	if _, err := o.Start(""); !errors.Is(err, ErrDone) {
		t.Errorf("after done: %v", err)
	}

	// survives a restart
	o2, _ := New(ctx, Options{Store: st, Auth: as, Now: clock})
	if !o2.Done() {
		t.Error("done after restart")
	}
}

func TestConsoleCompletes(t *testing.T) {
	ctx := context.Background()
	st, _ := store.Open(ctx, filepath.Join(t.TempDir(), "kicker.db"))
	defer st.Close()
	as, _ := auth.New(ctx, auth.Options{Store: st})
	o, _ := New(ctx, Options{Store: st, Auth: as})
	if o.Done() {
		t.Fatal("fresh")
	}
	if err := CompleteFromConsole(ctx, st); err != nil {
		t.Fatal(err)
	}
	if !o.Done() {
		t.Error("a running server must notice the console without a restart")
	}
	var lang string
	_, _ = st.GetSetting(ctx, store.SettingLanguage, &lang)
	if lang != "en" {
		t.Errorf("language %q", lang)
	}
}
