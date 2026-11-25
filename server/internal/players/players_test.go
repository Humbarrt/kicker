package players

import (
	"bytes"
	"context"
	"errors"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"os"
	"path/filepath"
	"testing"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

func newService(t *testing.T) (*Service, *store.Store, *auth.Service) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	st, err := store.Open(ctx, filepath.Join(dir, "kicker.db"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { st.Close() })
	as, err := auth.New(ctx, auth.Options{Store: st})
	if err != nil {
		t.Fatal(err)
	}
	_ = as.SetParams(ctx, auth.Params{Memory: 8 * 1024, Time: 1, Threads: 1})
	s, err := New(Options{Store: st, Auth: as, MediaDir: filepath.Join(dir, "media")})
	if err != nil {
		t.Fatal(err)
	}
	return s, st, as
}

// actors with the default Member permissions, Admin, and Anybody.
func member(as *auth.Service, accountID, playerID int64) Actor {
	acc := auth.Account{ID: accountID, Status: auth.StatusApproved, Group: auth.GroupRef{ID: as.BuiltinGroup(auth.GroupMember), Builtin: auth.GroupMember}}
	return Actor{AccountID: accountID, PlayerID: playerID, Perms: as.PermsOf(&acc)}
}

var admin = Actor{AccountID: 1, Perms: auth.Perms{Admin: true}}

func testPicture(t *testing.T, w, h int, format string) []byte {
	t.Helper()
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			// left half red, right half blue, so the crop and the averaging are visible
			if x < w/2 {
				img.Set(x, y, color.RGBA{220, 20, 20, 255})
			} else {
				img.Set(x, y, color.RGBA{20, 20, 220, 255})
			}
		}
	}
	var b bytes.Buffer
	if format == "png" {
		_ = png.Encode(&b, img)
	} else {
		_ = jpeg.Encode(&b, img, nil)
	}
	return b.Bytes()
}

func TestProcessAvatar(t *testing.T) {
	a, err := ProcessAvatar(testPicture(t, 900, 600, "png"))
	if err != nil {
		t.Fatal(err)
	}
	large, err := jpeg.Decode(bytes.NewReader(a.Large))
	if err != nil || large.Bounds().Dx() != 512 || large.Bounds().Dy() != 512 {
		t.Fatalf("large: %v %v", large.Bounds(), err)
	}
	small, _ := jpeg.Decode(bytes.NewReader(a.Small))
	if small.Bounds().Dx() != 128 {
		t.Errorf("small: %v", small.Bounds())
	}
	// centred square crop of a 900×600 half red / half blue picture: still half and half
	r, _, b, _ := large.At(100, 256).RGBA()
	r2, _, b2, _ := large.At(400, 256).RGBA()
	if r>>8 < 180 || b>>8 > 60 || b2>>8 < 180 || r2>>8 > 60 {
		t.Errorf("colors after crop and resize: left %d/%d right %d/%d", r>>8, b>>8, r2>>8, b2>>8)
	}
	if len(a.Hash) != 64 {
		t.Errorf("hash %q", a.Hash)
	}
	// a small picture stays at its size, JPEG input works
	a2, err := ProcessAvatar(testPicture(t, 200, 300, "jpeg"))
	if l, _ := jpeg.Decode(bytes.NewReader(a2.Large)); err != nil || l.Bounds().Dx() != 200 {
		t.Errorf("200px: %v", err)
	}
	for name, data := range map[string][]byte{
		"too small":    testPicture(t, 100, 300, "png"),
		"not an image": []byte("<svg xmlns='http://www.w3.org/2000/svg'></svg>"),
		"empty":        nil,
		"gif":          []byte("GIF89a\x01\x00\x01\x00\x00\x00\x00;"),
	} {
		if _, err := ProcessAvatar(data); !errors.Is(err, ErrBadImage) {
			t.Errorf("%s: %v", name, err)
		}
	}
}

func TestPlayersAndPermissions(t *testing.T) {
	ctx := context.Background()
	s, st, as := newService(t)

	list, _ := s.List(ctx, false)
	if len(list) != 1 || !list[0].Guest {
		t.Fatalf("built-in guest: %+v", list)
	}
	anybody := Actor{Perms: as.PermsOf(nil)}
	if _, err := s.Create(ctx, anybody, NewPlayer{Name: "Max"}); !errors.Is(err, ErrForbidden) {
		t.Errorf("anybody creates: %v", err)
	}
	mia := member(as, 10, 0)
	maxP, err := s.Create(ctx, mia, NewPlayer{Name: "  Max   Power "})
	if err != nil || maxP.Name != "Max Power" || maxP.Color == "" {
		t.Fatalf("create: %+v %v", maxP, err)
	}
	if _, err := s.Create(ctx, mia, NewPlayer{Name: "max power"}); !errors.Is(err, ErrNameTaken) {
		t.Errorf("duplicate: %v", err)
	}
	for name, in := range map[string]NewPlayer{"empty": {Name: " "}, "long": {Name: string(make([]rune, 41))}, "color": {Name: "X", Color: "red"}} {
		if _, err := s.Create(ctx, mia, in); err == nil {
			t.Errorf("%s accepted", name)
		}
	}

	// colors: each new player gets one of the least used
	seen := map[string]int{}
	for i := 0; i < len(Palette)-1; i++ {
		p, err := s.Create(ctx, mia, NewPlayer{Name: "P" + string(rune('a'+i))})
		if err != nil {
			t.Fatal(err)
		}
		seen[p.Color]++
	}
	seen[maxP.Color]++
	if len(seen) != len(Palette) {
		t.Errorf("16 players should use all 16 colors once, got %v", seen)
	}

	// editing: own player with profile.own, others only with profile.any
	if _, err := s.Update(ctx, mia, maxP.ID, Change{Name: ptr("Maxi")}); !errors.Is(err, ErrForbidden) {
		t.Errorf("member edits someone else: %v", err)
	}
	maxi := member(as, 11, maxP.ID)
	if p, err := s.Update(ctx, maxi, maxP.ID, Change{Name: ptr("Maxi"), Color: ptr("#ABCDEF")}); err != nil || p.Name != "Maxi" || p.Color != "#abcdef" {
		t.Errorf("own edit: %+v %v", p, err)
	}
	if _, err := s.Update(ctx, maxi, maxP.ID, Change{Archived: ptr(true)}); !errors.Is(err, ErrForbidden) {
		t.Errorf("archiving needs profile.any: %v", err)
	}
	if p, err := s.Update(ctx, admin, maxP.ID, Change{Archived: ptr(true)}); err != nil || !p.Archived {
		t.Errorf("admin archives: %+v %v", p, err)
	}
	if l, _ := s.List(ctx, false); len(l) != 16 {
		t.Errorf("archived players are hidden: %d", len(l))
	}
	if l, _ := s.List(ctx, true); len(l) != 17 {
		t.Errorf("with all: %d", len(l))
	}
	guest, _ := st.GuestPlayer(ctx)
	if _, err := s.Update(ctx, admin, guest.ID, Change{Archived: ptr(true)}); !errors.Is(err, ErrGuest) {
		t.Errorf("archive guest: %v", err)
	}
	if err := s.Delete(ctx, admin, guest.ID); !errors.Is(err, ErrGuest) {
		t.Errorf("delete guest: %v", err)
	}
	if err := s.Delete(ctx, mia, maxP.ID); !errors.Is(err, ErrForbidden) {
		t.Errorf("member deletes: %v", err)
	}
	if err := s.Delete(ctx, admin, maxP.ID); err != nil {
		t.Errorf("delete unused player: %v", err)
	}
	if _, err := s.Get(ctx, maxP.ID); !errors.Is(err, ErrNotFound) {
		t.Errorf("deleted: %v", err)
	}
}

func TestMyPlayerAndAccountLink(t *testing.T) {
	ctx := context.Background()
	s, st, as := newService(t)
	acc, err := as.CreateAccount(ctx, auth.NewAccount{Username: "anna", Password: "blue tiger dances", GroupID: as.BuiltinGroup(auth.GroupAdmin)})
	if err != nil {
		t.Fatal(err)
	}
	me := Actor{AccountID: acc.ID, Perms: auth.Perms{Admin: true}}
	p, err := s.CreateMine(ctx, me, NewPlayer{Name: "Anna"})
	if err != nil || !p.HasAccount {
		t.Fatalf("create mine: %+v %v", p, err)
	}
	if a, _ := as.Account(ctx, acc.ID); a.PlayerID == nil || *a.PlayerID != p.ID {
		t.Errorf("linked: %+v", a)
	}
	me.PlayerID = p.ID
	if _, err := s.CreateMine(ctx, me, NewPlayer{Name: "Anna 2"}); !errors.Is(err, ErrHasPlayer) {
		t.Errorf("second own player: %v", err)
	}
	// a player with an account can't be deleted, only archived
	if err := s.Delete(ctx, admin, p.ID); !errors.Is(err, ErrInUse) {
		t.Errorf("delete linked player: %v", err)
	}
	// linking: one account per player, never the guest
	bob, _ := as.CreateAccount(ctx, auth.NewAccount{Username: "bob", Password: "blue tiger dances"})
	if err := as.SetAccountPlayer(ctx, auth.Actor{}, bob.ID, p.ID); !errors.Is(err, auth.ErrPlayerTaken) {
		t.Errorf("player taken: %v", err)
	}
	guest, _ := st.GuestPlayer(ctx)
	if err := as.SetAccountPlayer(ctx, auth.Actor{}, bob.ID, guest.ID); !errors.Is(err, auth.ErrInvalidPlayer) {
		t.Errorf("guest link: %v", err)
	}
	if err := as.SetAccountPlayer(ctx, auth.Actor{}, acc.ID, 0); err != nil {
		t.Errorf("unlink: %v", err)
	}
	if err := as.SetAccountPlayer(ctx, auth.Actor{}, bob.ID, p.ID); err != nil {
		t.Errorf("link to bob: %v", err)
	}
}

func TestAvatarFiles(t *testing.T) {
	ctx := context.Background()
	s, _, as := newService(t)
	mia := member(as, 10, 0)
	a, _ := s.Create(ctx, mia, NewPlayer{Name: "A"})
	b, _ := s.Create(ctx, mia, NewPlayer{Name: "B"})
	pic := testPicture(t, 300, 300, "png")

	if _, err := s.SetAvatar(ctx, mia, a.ID, pic); !errors.Is(err, ErrForbidden) {
		t.Errorf("member sets someone's picture: %v", err)
	}
	pa, err := s.SetAvatar(ctx, admin, a.ID, pic)
	if err != nil || pa.Avatar == "" {
		t.Fatalf("set: %+v %v", pa, err)
	}
	pb, _ := s.SetAvatar(ctx, admin, b.ID, pic) // same picture: same files
	if pb.Avatar != pa.Avatar {
		t.Error("same picture, same hash")
	}
	large := s.AvatarPath(pa.Avatar, SizeLarge)
	if _, err := os.Stat(large); err != nil {
		t.Fatal(err)
	}
	if _, err := s.SetAvatar(ctx, admin, a.ID, nil); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(large); err != nil {
		t.Error("still used by B: must stay")
	}
	_, _ = s.SetAvatar(ctx, admin, b.ID, nil)
	if _, err := os.Stat(large); !os.IsNotExist(err) {
		t.Error("unused picture must be deleted")
	}
	for _, bad := range []string{"../x", pa.Avatar[:63], "ZZ" + pa.Avatar[2:]} {
		if s.AvatarPath(bad, SizeLarge) != "" {
			t.Errorf("path for %q", bad)
		}
	}
	if s.AvatarPath(pa.Avatar, 64) != "" {
		t.Error("unknown size")
	}
}

func ptr[T any](v T) *T { return &v }
