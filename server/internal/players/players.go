// Package players manages players (D2): names, colors from a curated palette, profile pictures
// and the link to accounts. Every change checks the caller's permissions here as well as in the
// router (spec 03 §4).
package players

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"math/rand/v2"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"unicode/utf8"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/store"
)

// Palette is the curated set of player colors: readable with white text, distinct from each other.
// New players get one of the least-used colors (D2).
var Palette = []string{
	"#dc2626", "#ea580c", "#d97706", "#65a30d", "#16a34a", "#0d9488", "#0891b2", "#0284c7",
	"#2563eb", "#4f46e5", "#7c3aed", "#1e3a8a", "#c026d3", "#db2777", "#e11d48", "#57534e",
}

// Player is a player as the API shows it.
type Player struct {
	ID         int64  `json:"id"`
	Name       string `json:"name"`
	Color      string `json:"color"`
	Avatar     string `json:"avatar,omitempty"` // picture hash; GET /api/media/avatars/<hash>-<size>.jpg
	Guest      bool   `json:"guest"`
	Archived   bool   `json:"archived"`
	HasAccount bool   `json:"has_account"`
	CreatedAt  int64  `json:"created_at"`
}

func view(p store.Player) Player {
	return Player{ID: p.ID, Name: p.Name, Color: p.Color, Avatar: p.AvatarHash, Guest: p.IsGuest,
		Archived: p.ArchivedAt != nil, HasAccount: p.AccountID != nil, CreatedAt: p.CreatedAt.UnixMilli()}
}

// Errors.
var (
	ErrForbidden    = errors.New("your group isn't allowed to do this")
	ErrInvalidName  = errors.New("player names are 1–40 characters")
	ErrInvalidColor = errors.New("colors are written #rrggbb")
	ErrNameTaken    = errors.New("a player with this name exists")
	ErrHasPlayer    = errors.New("this account already has a player")
	ErrGuest        = errors.New("the guest player can't be archived, deleted or linked to an account")
	ErrInUse        = errors.New("this player has played or has an account – archive it instead")
	ErrNotFound     = store.ErrNotFound
)

// Actor is who makes a change.
type Actor struct {
	AccountID int64 // 0 = not logged in
	PlayerID  int64 // the account's player, 0 = none
	Perms     auth.Perms
	IP        string
}

// Options configure the service.
type Options struct {
	Store    *store.Store
	Auth     *auth.Service // for the audit log
	MediaDir string        // pictures go to MediaDir/avatars
	Logger   *slog.Logger
}

// Service is the players service.
type Service struct {
	st   *store.Store
	auth *auth.Service
	dir  string
	log  *slog.Logger
}

// New returns the service and creates the picture folder.
func New(opts Options) (*Service, error) {
	if opts.Logger == nil {
		opts.Logger = slog.Default()
	}
	dir := filepath.Join(opts.MediaDir, "avatars")
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	return &Service{st: opts.Store, auth: opts.Auth, dir: dir, log: opts.Logger.With("component", "players")}, nil
}

func (s *Service) audit(ctx context.Context, by Actor, action, target, details string) {
	if s.auth == nil {
		return
	}
	var id *int64
	if by.AccountID != 0 {
		id = &by.AccountID
	}
	s.auth.Audit(ctx, id, by.IP, action, target, details)
}

// List returns the players; archived ones only with all.
func (s *Service) List(ctx context.Context, all bool) ([]Player, error) {
	ps, err := s.st.Players(ctx, all)
	if err != nil {
		return nil, err
	}
	out := make([]Player, len(ps))
	for i, p := range ps {
		out[i] = view(p)
	}
	return out, nil
}

// Get returns one player.
func (s *Service) Get(ctx context.Context, id int64) (Player, error) {
	p, err := s.st.Player(ctx, id)
	return view(p), err
}

func cleanName(name string) (string, error) {
	name = strings.Join(strings.Fields(name), " ")
	if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
		return "", ErrInvalidName
	}
	return name, nil
}

var colorRE = regexp.MustCompile(`^#[0-9a-f]{6}$`)

func cleanColor(c string) (string, error) {
	c = strings.ToLower(strings.TrimSpace(c))
	if !colorRE.MatchString(c) {
		return "", ErrInvalidColor
	}
	return c, nil
}

// SuggestColor returns one of the least-used palette colors, picked at random among them.
func (s *Service) SuggestColor(ctx context.Context) (string, error) {
	use, err := s.st.ColorUse(ctx)
	if err != nil {
		return "", err
	}
	least := -1
	var pick []string
	for _, c := range Palette {
		switch n := use[c]; {
		case least < 0 || n < least:
			least, pick = n, []string{c}
		case n == least:
			pick = append(pick, c)
		}
	}
	return pick[rand.IntN(len(pick))], nil
}

// NewPlayer describes a player to create; an empty color picks one from the palette.
type NewPlayer struct {
	Name  string
	Color string
}

// Create creates a player (players.create).
func (s *Service) Create(ctx context.Context, by Actor, in NewPlayer) (Player, error) {
	if by.AccountID == 0 || !by.Perms.Has(auth.PlayersCreate) {
		return Player{}, ErrForbidden
	}
	return s.create(ctx, by, in, 0)
}

// CreateMine creates the player of the caller's account and links it (profile.own): for accounts
// that don't have a player yet, e.g. the first admin.
func (s *Service) CreateMine(ctx context.Context, by Actor, in NewPlayer) (Player, error) {
	if by.AccountID == 0 || !by.Perms.Has(auth.ProfileOwn) {
		return Player{}, ErrForbidden
	}
	if by.PlayerID != 0 {
		return Player{}, ErrHasPlayer
	}
	p, err := s.create(ctx, by, in, by.AccountID)
	if err == nil && s.auth != nil {
		s.auth.ForgetAccount(by.AccountID)
	}
	return p, err
}

// CheckNewName returns the cleaned name if a new player could get it (sign-up, spec 03 §3).
func (s *Service) CheckNewName(ctx context.Context, name string) (string, error) {
	name, err := cleanName(name)
	if err != nil {
		return "", err
	}
	if p, err := s.byName(ctx, name); err != nil {
		return "", err
	} else if p.ID != 0 {
		return "", ErrNameTaken
	}
	return name, nil
}

// CreateLinked creates a player for an account without a permission check: for open sign-ups
// and for approvals that create the requested new player (spec 03 §3).
func (s *Service) CreateLinked(ctx context.Context, by Actor, name string, accountID int64) (Player, error) {
	p, err := s.create(ctx, by, NewPlayer{Name: name}, accountID)
	if err == nil && s.auth != nil {
		s.auth.ForgetAccount(accountID)
	}
	return p, err
}

func (s *Service) create(ctx context.Context, by Actor, in NewPlayer, link int64) (Player, error) {
	name, err := cleanName(in.Name)
	if err != nil {
		return Player{}, err
	}
	color := in.Color
	if color == "" {
		if color, err = s.SuggestColor(ctx); err != nil {
			return Player{}, err
		}
	} else if color, err = cleanColor(color); err != nil {
		return Player{}, err
	}
	if p, err := s.byName(ctx, name); err != nil {
		return Player{}, err
	} else if p.ID != 0 {
		return Player{}, ErrNameTaken
	}
	id, err := s.st.CreatePlayer(ctx, store.Player{Name: name, Color: color}, link)
	if errors.Is(err, store.ErrExists) {
		if link != 0 {
			return Player{}, ErrHasPlayer
		}
		return Player{}, ErrNameTaken
	} else if err != nil {
		return Player{}, err
	}
	s.audit(ctx, by, "player_created", name, "")
	return s.Get(ctx, id)
}

func (s *Service) byName(ctx context.Context, name string) (store.Player, error) {
	ps, err := s.st.Players(ctx, true)
	if err != nil {
		return store.Player{}, err
	}
	for _, p := range ps {
		if strings.EqualFold(p.Name, name) {
			return p, nil
		}
	}
	return store.Player{}, nil
}

// Change is a partial update of a player.
type Change struct {
	Name     *string
	Color    *string
	Archived *bool
}

// canEdit: profile.any, or profile.own for one's own player.
func canEdit(by Actor, id int64) bool {
	if by.AccountID == 0 {
		return false
	}
	return by.Perms.Has(auth.ProfileAny) || (by.Perms.Has(auth.ProfileOwn) && by.PlayerID == id)
}

// Update renames, recolors or (un)archives a player. Archiving needs profile.any.
func (s *Service) Update(ctx context.Context, by Actor, id int64, ch Change) (Player, error) {
	if !canEdit(by, id) || (ch.Archived != nil && !by.Perms.Has(auth.ProfileAny)) {
		return Player{}, ErrForbidden
	}
	old, err := s.st.Player(ctx, id)
	if err != nil {
		return Player{}, err
	}
	var sc store.PlayerChange
	var changes []string
	if ch.Name != nil {
		n, err := cleanName(*ch.Name)
		if err != nil {
			return Player{}, err
		}
		if n != old.Name {
			sc.Name = &n
			changes = append(changes, fmt.Sprintf("name %q → %q", old.Name, n))
		}
	}
	if ch.Color != nil {
		c, err := cleanColor(*ch.Color)
		if err != nil {
			return Player{}, err
		}
		if c != old.Color {
			sc.Color = &c
			changes = append(changes, "color "+old.Color+" → "+c)
		}
	}
	if ch.Archived != nil {
		if old.IsGuest {
			return Player{}, ErrGuest
		}
		if *ch.Archived != (old.ArchivedAt != nil) {
			sc.Archived = ch.Archived
			changes = append(changes, map[bool]string{true: "archived", false: "unarchived"}[*ch.Archived])
		}
	}
	if len(changes) > 0 {
		if err := s.st.UpdatePlayer(ctx, id, sc); errors.Is(err, store.ErrExists) {
			return Player{}, ErrNameTaken
		} else if err != nil {
			return Player{}, err
		}
		s.audit(ctx, by, "player_changed", old.Name, strings.Join(changes, ", "))
	}
	return s.Get(ctx, id)
}

// SetAvatar stores a new profile picture (nil data = remove it).
func (s *Service) SetAvatar(ctx context.Context, by Actor, id int64, data []byte) (Player, error) {
	if !canEdit(by, id) {
		return Player{}, ErrForbidden
	}
	old, err := s.st.Player(ctx, id)
	if err != nil {
		return Player{}, err
	}
	hash := ""
	if data != nil {
		a, err := ProcessAvatar(data)
		if err != nil {
			return Player{}, err
		}
		if err := s.writeFiles(a); err != nil {
			return Player{}, err
		}
		hash = a.Hash
	}
	if err := s.st.UpdatePlayer(ctx, id, store.PlayerChange{Avatar: &hash}); err != nil {
		return Player{}, err
	}
	s.gc(ctx, old.AvatarHash)
	s.audit(ctx, by, "player_picture_changed", old.Name, map[bool]string{true: "removed", false: "uploaded"}[data == nil])
	return s.Get(ctx, id)
}

// AvatarPath returns the file of a picture size, or "" for an invalid name.
func (s *Service) AvatarPath(hash string, size int) string {
	if (size != SizeLarge && size != SizeSmall) || len(hash) != 64 || strings.Trim(hash, "0123456789abcdef") != "" {
		return ""
	}
	return filepath.Join(s.dir, fmt.Sprintf("%s-%d.jpg", hash, size))
}

func (s *Service) writeFiles(a Avatar) error {
	for size, data := range map[int][]byte{SizeLarge: a.Large, SizeSmall: a.Small} {
		path := s.AvatarPath(a.Hash, size)
		if _, err := os.Stat(path); err == nil {
			continue // same picture already stored
		}
		tmp, err := os.CreateTemp(s.dir, ".upload-*")
		if err != nil {
			return err
		}
		_, werr := tmp.Write(data)
		cerr := tmp.Close()
		if werr != nil || cerr != nil {
			os.Remove(tmp.Name())
			return errors.Join(werr, cerr)
		}
		if err := os.Rename(tmp.Name(), path); err != nil {
			os.Remove(tmp.Name())
			return err
		}
	}
	return nil
}

// gc deletes a picture's files once no player uses it.
func (s *Service) gc(ctx context.Context, hash string) {
	if hash == "" {
		return
	}
	if used, err := s.st.AvatarInUse(ctx, hash); err != nil || used {
		return
	}
	for _, size := range []int{SizeLarge, SizeSmall} {
		if err := os.Remove(s.AvatarPath(hash, size)); err != nil && !os.IsNotExist(err) {
			s.log.Warn("removing an unused picture", "err", err)
		}
	}
}

// Delete removes a player that never played and has no account (Admin only); otherwise
// ErrInUse – archive it instead.
func (s *Service) Delete(ctx context.Context, by Actor, id int64) error {
	if !by.Perms.Admin {
		return ErrForbidden
	}
	p, err := s.st.Player(ctx, id)
	if err != nil {
		return err
	}
	if p.IsGuest {
		return ErrGuest
	}
	if err := s.st.DeletePlayer(ctx, id); errors.Is(err, store.ErrInUse) {
		return ErrInUse
	} else if err != nil {
		return err
	}
	s.gc(ctx, p.AvatarHash)
	s.audit(ctx, by, "player_deleted", p.Name, "")
	return nil
}
