package auth

import (
	"errors"
	"testing"
)

func TestCatalogDefaults(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range Catalog {
		if seen[p.Key] {
			t.Errorf("duplicate %s", p.Key)
		}
		seen[p.Key] = true
		if err := ValidateValue(GroupMember, p.Key, p.Member); err != nil {
			t.Errorf("member default of %s: %v", p.Key, err)
		}
		if err := ValidateValue(GroupAnybody, p.Key, p.Anybody); err != nil {
			t.Errorf("anybody default of %s: %v", p.Key, err)
		}
	}
}

func TestValidateValue(t *testing.T) {
	for _, tt := range []struct {
		group, key string
		v          int
		want       error
	}{
		{GroupMember, TableScore, 1, nil},
		{GroupMember, TableScore, 2, ErrInvalidValue},
		{GroupMember, GamesEdit, ScopeAll, nil},
		{GroupMember, GamesEdit, 3, ErrInvalidValue},
		{GroupMember, GamesEditDays, Unlimited, nil},
		{GroupMember, GamesEditDays, 365, nil},
		{GroupMember, GamesEditDays, 366, ErrInvalidValue},
		{GroupMember, GamesEditDays, -2, ErrInvalidValue},
		{GroupMember, "games.delete_all", 1, ErrUnknownPermission},
		{GroupAdmin, ViewLive, 1, ErrAdminFixed},
		{GroupAnybody, TableScore, 1, nil},
		{GroupAnybody, GamesEdit, ScopeAll, ErrLoginRequired},
		{GroupAnybody, GamesEdit, ScopeOff, nil},
		{"", PresetsManage, 1, nil},
	} {
		if err := ValidateValue(tt.group, tt.key, tt.v); !errors.Is(err, tt.want) {
			t.Errorf("%s %s=%d: %v, want %v", tt.group, tt.key, tt.v, err, tt.want)
		}
	}
}

func TestEffective(t *testing.T) {
	group := map[string]int{TableScore: 0, GamesEdit: ScopeOwn, GamesEditDays: 7, ViewLive: 0}
	anybody := map[string]int{TableScore: 1, ViewLive: 1}
	e := effective(group, anybody)
	if e[TableScore] != 1 || e[ViewLive] != 1 || e[GamesEdit] != ScopeOwn || e[GamesEditDays] != 7 || e[PresetsManage] != 0 {
		t.Errorf("%v", e)
	}
	if merge(KindDays, 30, Unlimited) != Unlimited || merge(KindDays, Unlimited, 0) != Unlimited || merge(KindDays, 3, 30) != 30 {
		t.Error("days: unlimited is the most generous")
	}
	admin := Perms{Admin: true}
	if admin.Value(GamesEditDays) != Unlimited || admin.Value(TournamentsRun) != ScopeAll || !admin.Has(PresetsManage) || admin.Has("nope") {
		t.Errorf("admin: %v", admin.Map())
	}
}
