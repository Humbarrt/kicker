package auth

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/Humbarrt/kicker/server/internal/store"
)

// groupCache holds every group with its effective permissions. Guarded by Service.mu.
type groupCache struct {
	byID      map[int64]store.Group
	effective map[int64]map[string]int // group's own values combined with Anybody's
	builtin   map[string]int64         // admin | member | anybody → id
}

// loadGroups (re)reads the groups. With seed, missing permission rows of the built-in groups
// get their defaults first (new permissions in a release).
func (s *Service) loadGroups(ctx context.Context, seed bool) error {
	gs, err := s.st.Groups(ctx)
	if err != nil {
		return err
	}
	if seed {
		for _, g := range gs {
			if g.Builtin == GroupMember || g.Builtin == GroupAnybody {
				if err := s.st.EnsurePermissions(ctx, g.ID, Defaults(g.Builtin)); err != nil {
					return err
				}
			}
		}
		if gs, err = s.st.Groups(ctx); err != nil {
			return err
		}
	}
	c := groupCache{byID: map[int64]store.Group{}, effective: map[int64]map[string]int{}, builtin: map[string]int64{}}
	for _, g := range gs {
		c.byID[g.ID] = g
		if g.Builtin != "" {
			c.builtin[g.Builtin] = g.ID
		}
	}
	for _, k := range []string{GroupAdmin, GroupMember, GroupAnybody} {
		if _, ok := c.builtin[k]; !ok {
			return fmt.Errorf("built-in group %q is missing", k)
		}
	}
	anybody := c.byID[c.builtin[GroupAnybody]].Permissions
	for _, g := range gs {
		c.effective[g.ID] = effective(g.Permissions, anybody)
	}
	s.mu.Lock()
	s.groups = c
	s.mu.Unlock()
	return nil
}

func (s *Service) group(id int64) (store.Group, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	g, ok := s.groups.byID[id]
	return g, ok
}

// BuiltinGroup returns the id of a built-in group (admin | member | anybody).
func (s *Service) BuiltinGroup(kind string) int64 {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.groups.builtin[kind]
}

func (s *Service) accountOf(a store.Account) Account {
	g, _ := s.group(a.GroupID)
	out := Account{ID: a.ID, Username: a.Username, Status: a.Status, MustChangePassword: a.MustChangePassword, PlayerID: a.PlayerID,
		Group: GroupRef{ID: g.ID, Name: g.Name, Builtin: g.Builtin}, CreatedAt: a.CreatedAt.UnixMilli(),
		RequestedPlayerID: a.RequestedPlayerID, RequestedPlayerName: a.RequestedPlayerName}
	if a.LastLoginAt != nil {
		ms := a.LastLoginAt.UnixMilli()
		out.LastLoginAt = &ms
	}
	return out
}

// PermsOf returns the effective permissions of an account; nil (not logged in) and accounts that
// aren't approved get Anybody's (spec 03 §3, §4).
func (s *Service) PermsOf(a *Account) Perms {
	s.mu.Lock()
	defer s.mu.Unlock()
	if a.IsAdmin() {
		return Perms{Admin: true}
	}
	id := s.groups.builtin[GroupAnybody]
	if a.IsApproved() {
		id = a.Group.ID
	}
	return Perms{Values: s.groups.effective[id]}
}

// --- administration (Admin group only; the API checks that, these functions log it) ---------

// GroupView is a group as the admin UI sees it.
type GroupView struct {
	ID          int64          `json:"id"`
	Name        string         `json:"name"`
	Builtin     string         `json:"builtin,omitempty"`
	Members     int            `json:"members"`
	Permissions map[string]int `json:"permissions"` // the group's own values (Admin: all granted)
	Effective   map[string]int `json:"effective"`   // combined with Anybody's
}

// Groups lists the groups.
func (s *Service) Groups(ctx context.Context) ([]GroupView, error) {
	gs, err := s.st.Groups(ctx)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	anybody := s.groups.byID[s.groups.builtin[GroupAnybody]].Permissions
	s.mu.Unlock()
	out := make([]GroupView, len(gs))
	for i, g := range gs {
		v := GroupView{ID: g.ID, Name: g.Name, Builtin: g.Builtin, Members: g.Members}
		if g.Builtin == GroupAdmin {
			all := Perms{Admin: true}.Map()
			v.Permissions, v.Effective = all, all
		} else {
			v.Permissions = map[string]int{}
			for _, c := range Catalog {
				v.Permissions[c.Key] = g.Permissions[c.Key]
			}
			v.Effective = effective(g.Permissions, anybody)
		}
		out[i] = v
	}
	return out, nil
}

// Errors for group administration.
var (
	ErrGroupName    = errors.New("group names are 1–40 characters")
	ErrGroupExists  = errors.New("a group with this name exists")
	ErrGroupBuiltin = errors.New("built-in groups can't be deleted")
	ErrLastAdmin    = store.ErrLastAdmin
	ErrNotFound     = store.ErrNotFound
)

func checkGroupName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if n := utf8.RuneCountInString(name); n < 1 || n > 40 {
		return "", ErrGroupName
	}
	return name, nil
}

// Actor is who makes an administrative change, for the audit log.
type Actor struct {
	AccountID int64
	IP        string
}

func (s *Service) auditAs(ctx context.Context, by Actor, action, target, details string) {
	var id *int64
	if by.AccountID != 0 {
		id = &by.AccountID
	}
	s.audit(ctx, id, by.IP, action, target, details)
}

// CreateGroup creates an own group as a copy of another group's permissions.
func (s *Service) CreateGroup(ctx context.Context, by Actor, name string, copyFrom int64) (GroupView, error) {
	name, err := checkGroupName(name)
	if err != nil {
		return GroupView{}, err
	}
	src, ok := s.group(copyFrom)
	if !ok {
		return GroupView{}, ErrNotFound
	}
	perms := Defaults("")
	if src.Builtin == GroupAdmin {
		// A copy of Admin gets every grantable permission at its maximum (not the system ones).
		for _, c := range Catalog {
			perms[c.Key] = Perms{Admin: true}.Value(c.Key)
		}
	} else {
		for k, v := range src.Permissions {
			if _, known := Lookup(k); known {
				perms[k] = v
			}
		}
	}
	id, err := s.st.CreateGroup(ctx, name, perms)
	if errors.Is(err, store.ErrExists) {
		return GroupView{}, ErrGroupExists
	} else if err != nil {
		return GroupView{}, err
	}
	if err := s.loadGroups(ctx, false); err != nil {
		return GroupView{}, err
	}
	s.auditAs(ctx, by, "group_created", name, "copy of "+src.Name)
	return s.groupView(ctx, id)
}

func (s *Service) groupView(ctx context.Context, id int64) (GroupView, error) {
	gs, err := s.Groups(ctx)
	if err != nil {
		return GroupView{}, err
	}
	for _, g := range gs {
		if g.ID == id {
			return g, nil
		}
	}
	return GroupView{}, ErrNotFound
}

// GroupChange is a partial update of a group.
type GroupChange struct {
	Name        *string
	Permissions map[string]int
}

// UpdateGroup renames a group and/or changes permission values. The Admin group can only be
// renamed; login-required permissions can't be granted to Anybody.
func (s *Service) UpdateGroup(ctx context.Context, by Actor, id int64, ch GroupChange) (GroupView, error) {
	g, ok := s.group(id)
	if !ok {
		return GroupView{}, ErrNotFound
	}
	if ch.Name != nil {
		n, err := checkGroupName(*ch.Name)
		if err != nil {
			return GroupView{}, err
		}
		ch.Name = &n
	}
	var changes []string
	for k, v := range ch.Permissions {
		if err := ValidateValue(g.Builtin, k, v); err != nil {
			return GroupView{}, err
		}
		if old := g.Permissions[k]; old != v {
			changes = append(changes, fmt.Sprintf("%s %d→%d", k, old, v))
		}
	}
	sort.Strings(changes)
	err := s.st.UpdateGroup(ctx, id, ch.Name, ch.Permissions)
	if errors.Is(err, store.ErrExists) {
		return GroupView{}, ErrGroupExists
	} else if err != nil {
		return GroupView{}, err
	}
	if err := s.loadGroups(ctx, false); err != nil {
		return GroupView{}, err
	}
	if ch.Name != nil && *ch.Name != g.Name {
		s.auditAs(ctx, by, "group_renamed", g.Name, "→ "+*ch.Name)
	}
	if len(changes) > 0 {
		s.auditAs(ctx, by, "group_permissions_changed", g.Name, strings.Join(changes, ", "))
	}
	return s.groupView(ctx, id)
}

// DeleteGroup deletes an own group; its accounts move to moveTo.
func (s *Service) DeleteGroup(ctx context.Context, by Actor, id, moveTo int64) error {
	g, ok := s.group(id)
	if !ok {
		return ErrNotFound
	}
	if g.Builtin != "" {
		return ErrGroupBuiltin
	}
	if t, ok := s.group(moveTo); !ok || t.Builtin == GroupAnybody || moveTo == id {
		return ErrInvalidGroup
	}
	if err := s.st.DeleteGroup(ctx, id, moveTo); err != nil {
		return err
	}
	if err := s.loadGroups(ctx, false); err != nil {
		return err
	}
	s.forgetAll()
	t, _ := s.group(moveTo)
	s.auditAs(ctx, by, "group_deleted", g.Name, fmt.Sprintf("%d accounts moved to %s", g.Members, t.Name))
	return nil
}

func (s *Service) forgetAll() {
	s.mu.Lock()
	s.cache = map[string]*cached{}
	s.mu.Unlock()
}

// Accounts lists all accounts.
func (s *Service) Accounts(ctx context.Context) ([]Account, error) {
	as, err := s.st.Accounts(ctx)
	if err != nil {
		return nil, err
	}
	out := make([]Account, len(as))
	for i, a := range as {
		out[i] = s.accountOf(a)
	}
	return out, nil
}

// SetAccountGroup moves an account to another group. The last admin can't leave Admin.
func (s *Service) SetAccountGroup(ctx context.Context, by Actor, accountID, groupID int64) error {
	g, ok := s.group(groupID)
	if !ok || g.Builtin == GroupAnybody {
		return ErrInvalidGroup
	}
	a, err := s.st.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.st.SetAccountGroup(ctx, accountID, groupID); err != nil {
		return err
	}
	s.forgetAccount(accountID)
	from, _ := s.group(a.GroupID)
	s.auditAs(ctx, by, "account_group_changed", a.Username, from.Name+" → "+g.Name)
	return nil
}

// SetStatus approves, disables or re-enables an account. Disabling revokes all its sessions
// (spec 03 §3). The last admin can't be disabled.
func (s *Service) SetStatus(ctx context.Context, by Actor, accountID int64, status string) error {
	switch status {
	case StatusPending, StatusApproved, StatusDisabled:
	default:
		return fmt.Errorf("invalid status %q", status)
	}
	a, err := s.st.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if err := s.st.SetAccountStatus(ctx, accountID, status); err != nil {
		return err
	}
	if status == StatusDisabled {
		if _, err := s.st.DeleteSessions(ctx, accountID, 0); err != nil {
			return err
		}
	}
	s.forgetAccount(accountID)
	if a.Status != status {
		s.auditAs(ctx, by, "account_status_changed", a.Username, a.Status+" → "+status)
	}
	return nil
}

// Account returns one account.
func (s *Service) Account(ctx context.Context, id int64) (Account, error) {
	a, err := s.st.Account(ctx, id)
	if err != nil {
		return Account{}, err
	}
	return s.accountOf(a), nil
}

// AuditLog returns the newest audit log entries.
func (s *Service) AuditLog(ctx context.Context, limit int) ([]store.AuditEntry, error) {
	return s.st.AuditLog(ctx, limit)
}

// AccountNames maps account ids to usernames (for the audit log).
func (s *Service) AccountNames(ctx context.Context, ids []int64) (map[int64]string, error) {
	return s.st.AccountNames(ctx, ids)
}

// ForgetAccount drops cached sessions of an account after a change made elsewhere (e.g. its
// player was created).
func (s *Service) ForgetAccount(id int64) { s.forgetAccount(id) }

// ErrPlayerTaken is returned when the player already has another account.
var ErrPlayerTaken = errors.New("this player already has an account")

// SetAccountPlayer links an account to a player (0 = unlink). Linking to an existing player is
// an admin decision, so nobody takes over someone else's history (spec 03 §3).
func (s *Service) SetAccountPlayer(ctx context.Context, by Actor, accountID, playerID int64) error {
	a, err := s.st.Account(ctx, accountID)
	if err != nil {
		return err
	}
	if playerID != 0 {
		p, err := s.st.Player(ctx, playerID)
		if err != nil {
			return err
		}
		if p.IsGuest {
			return ErrInvalidPlayer
		}
	}
	if err := s.st.SetAccountPlayer(ctx, accountID, playerID); errors.Is(err, store.ErrExists) {
		return ErrPlayerTaken
	} else if err != nil {
		return err
	}
	s.forgetAccount(accountID)
	s.auditAs(ctx, by, "account_player_changed", a.Username, fmt.Sprintf("player %d", playerID))
	return nil
}

// ErrInvalidPlayer is returned for linking an account to the guest player.
var ErrInvalidPlayer = errors.New("the guest player can't be linked to an account")
