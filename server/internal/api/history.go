package api

import (
	"context"
	"errors"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/table"
)

// History is the game history store, implemented by *store.Store.
type History interface {
	ListGames(ctx context.Context, f store.GameFilter) ([]store.Game, error)
	RecentEdits(ctx context.Context, limit int) ([]store.EditEntry, error)
	AccountNames(ctx context.Context, ids []int64) (map[int64]string, error)
}

// GameSummaryView is a row of the game history.
type GameSummaryView struct {
	ID        int64                       `json:"id"`
	State     string                      `json:"state"` // finished | abandoned
	Home      int                         `json:"home"`
	Away      int                         `json:"away"`
	Winner    *string                     `json:"winner"`
	Voided    bool                        `json:"voided"`
	Edits     int                         `json:"edits"`
	PresetID  *int64                      `json:"preset_id"`
	StartedAt int64                       `json:"started_at"`
	EndedAt   *int64                      `json:"ended_at"`
	Roster    map[string][]table.SeatView `json:"roster"`
}

// GameDetailView is a game page: the game, its event log, and what the viewer may do.
type GameDetailView struct {
	table.GameDetail
	CanEdit   bool `json:"can_edit"`
	CanVoid   bool `json:"can_void"`
	CanRevert bool `json:"can_revert"`
}

// EditInput is the body of POST /api/games/:id/edits.
type EditInput struct {
	Kind       string `json:"kind"`
	Side       string `json:"side"`
	Slot       int    `json:"slot"`
	PlayerID   int64  `json:"player_id"`
	TargetSeq  int    `json:"target_seq"`
	PlayTimeMs *int64 `json:"play_time_ms"`
}

// EditFeedView is an edit in the feed of recent edits.
type EditFeedView struct {
	GameID    int64   `json:"game_id"`
	Seq       int     `json:"seq"`
	Type      string  `json:"type"`
	Side      *string `json:"side"`
	Slot      *int    `json:"slot"`
	PlayerID  *int64  `json:"player_id"`
	TargetSeq *int    `json:"target_seq"`
	Author    *string `json:"author"`
	At        int64   `json:"at"`
	Reverted  bool    `json:"reverted"`
}

func (o Options) historyRoutes(api *gin.RouterGroup) {
	t, h := o.Table, o.History
	view := o.Require(auth.ViewStats)
	idParam := func(c *gin.Context) (int64, bool) {
		id, err := strconv.ParseInt(c.Param("id"), 10, 64)
		if err != nil || id <= 0 {
			fail(c, http.StatusBadRequest, "invalid_input", "game ids are positive numbers")
			return 0, false
		}
		return id, true
	}
	optInt := func(c *gin.Context, key string) (int64, bool) {
		v := c.Query(key)
		if v == "" {
			return 0, true
		}
		n, err := strconv.ParseInt(v, 10, 64)
		if err != nil || n < 0 {
			fail(c, http.StatusBadRequest, "invalid_input", key+" is a number")
			return 0, false
		}
		return n, true
	}

	// GET /api/games: finished and abandoned games, newest first. Filters (all optional):
	// before=<id> (paging), limit, player=<id>[,<id>…] (all of them played, at most 4),
	// relation=together|against (of the first two players), result=win|loss|draw (for the first
	// player), kind=solo|team, status=finished|abandoned, flags=voided,edited,unassigned,guest,
	// shutout,close (all of them), from/to=<Unix ms> (started in [from, to)).
	api.GET("/games", view, func(c *gin.Context) {
		before, ok1 := optInt(c, "before")
		limit, ok2 := optInt(c, "limit")
		from, ok3 := optInt(c, "from")
		to, ok4 := optInt(c, "to")
		if !ok1 || !ok2 || !ok3 || !ok4 {
			return
		}
		f := store.GameFilter{BeforeID: before, Limit: int(limit), Relation: c.Query("relation"), Result: c.Query("result"),
			Kind: c.Query("kind"), Status: c.Query("status")}
		for _, v := range splitList(c.QueryArray("player")) {
			id, err := strconv.ParseInt(v, 10, 64)
			if err != nil || id <= 0 {
				fail(c, http.StatusBadRequest, "invalid_input", "player is a list of ids")
				return
			}
			if !slices.Contains(f.PlayerIDs, id) {
				f.PlayerIDs = append(f.PlayerIDs, id)
			}
		}
		f.Flags = splitList(c.QueryArray("flags"))
		bad := len(f.PlayerIDs) > 4 ||
			!slices.Contains([]string{"", "together", "against"}, f.Relation) ||
			!slices.Contains([]string{"", "win", "loss", "draw"}, f.Result) ||
			!slices.Contains([]string{"", "solo", "team"}, f.Kind) ||
			!slices.Contains([]string{"", "finished", "abandoned"}, f.Status)
		for _, flag := range f.Flags {
			bad = bad || !slices.Contains(store.GameFlags, flag)
		}
		if bad {
			fail(c, http.StatusBadRequest, "invalid_input", "unknown filter value")
			return
		}
		if from > 0 {
			f.From = time.UnixMilli(from)
		}
		if to > 0 {
			f.To = time.UnixMilli(to)
		}
		list, err := h.ListGames(c.Request.Context(), f)
		if err != nil {
			internalError(c, err)
			return
		}
		out := make([]GameSummaryView, len(list))
		for i, g := range list {
			v := GameSummaryView{ID: g.ID, State: g.State, Home: g.HomeGoals, Away: g.AwayGoals, Winner: g.Winner,
				Voided: g.Voided, Edits: g.Edits, PresetID: g.PresetID, StartedAt: g.StartedAt.UnixMilli(),
				Roster: map[string][]table.SeatView{"home": {}, "away": {}}}
			if g.EndedAt != nil {
				ms := g.EndedAt.UnixMilli()
				v.EndedAt = &ms
			}
			for _, p := range g.Participants {
				v.Roster[p.Side] = append(v.Roster[p.Side], table.SeatView{Slot: p.Slot, PlayerID: p.PlayerID, CoinUID: p.CoinUID})
			}
			out[i] = v
		}
		c.JSON(http.StatusOK, out)
	})

	detail := func(c *gin.Context, id int64) {
		d, err := t.GameDetail(c.Request.Context(), id)
		if err != nil {
			engineError(c, err)
			return
		}
		g, err := t.Game(c.Request.Context(), id)
		if err != nil {
			engineError(c, err)
			return
		}
		p, a := o.perms(c), account(c)
		finished := g.Status == engine.StatusFinished
		c.JSON(http.StatusOK, GameDetailView{GameDetail: d,
			CanEdit:   finished && a != nil && mayEdit(p, a, g, time.Now()),
			CanVoid:   finished && a != nil && p.Has(auth.GamesVoid),
			CanRevert: finished && a != nil && p.Has(auth.GamesRevert)})
	}
	api.GET("/games/:id", view, func(c *gin.Context) {
		if id, ok := idParam(c); ok {
			detail(c, id)
		}
	})

	// POST /api/games/:id/edits: correct a finished game (D2). Goals, players and sides need the
	// group's games.edit scope and window; voiding needs games.void, reverting games.revert.
	api.POST("/games/:id/edits", RequireLogin, func(c *gin.Context) {
		id, ok := idParam(c)
		if !ok {
			return
		}
		var in EditInput
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		ctx, cancel := context.WithTimeout(c.Request.Context(), 10*time.Second)
		defer cancel()
		g, err := t.Game(ctx, id)
		if err != nil {
			engineError(c, err)
			return
		}
		p, a := o.perms(c), account(c)
		var allowed bool
		switch engine.EditKind(in.Kind) {
		case engine.EditVoidGame:
			allowed = p.Has(auth.GamesVoid)
		case engine.EditRevert:
			allowed = p.Has(auth.GamesRevert)
		default:
			allowed = mayEdit(p, a, g, time.Now())
		}
		if !allowed {
			deny(c)
			return
		}
		ed := engine.Edit{Kind: engine.EditKind(in.Kind), Side: engine.Side(in.Side), Slot: in.Slot, PlayerID: in.PlayerID,
			TargetSeq: in.TargetSeq, Author: authorOf(c)}
		if in.PlayTimeMs != nil {
			d := time.Duration(*in.PlayTimeMs) * time.Millisecond
			ed.PlayTime = &d
		}
		if _, err := t.EditGame(ctx, id, ed); err != nil {
			switch {
			case errors.Is(err, engine.ErrNotFinished):
				fail(c, http.StatusConflict, "not_finished", err.Error())
			case errors.Is(err, engine.ErrBadEdit):
				fail(c, http.StatusBadRequest, "invalid_edit", err.Error())
			default:
				engineError(c, err)
			}
			return
		}
		o.audit(c, "game_edited", strconv.FormatInt(id, 10), in.Kind)
		detail(c, id)
	})

	// GET /api/edits?limit=50: the newest edits of all games, for those who may revert them.
	api.GET("/edits", o.Require(auth.GamesRevert), func(c *gin.Context) {
		limit, ok := optInt(c, "limit")
		if !ok {
			return
		}
		if limit == 0 || limit > 200 {
			limit = 50
		}
		ctx := c.Request.Context()
		entries, err := h.RecentEdits(ctx, int(limit))
		if err != nil {
			internalError(c, err)
			return
		}
		var ids []int64
		reverted := map[int64]map[int]bool{}
		for _, e := range entries {
			if e.AuthorAccountID != nil {
				ids = append(ids, *e.AuthorAccountID)
			}
			if _, ok := reverted[e.GameID]; !ok {
				d, err := t.GameDetail(ctx, e.GameID)
				if err != nil {
					internalError(c, err)
					return
				}
				m := map[int]bool{}
				for _, ev := range d.Events {
					m[ev.Seq] = ev.Reverted
				}
				reverted[e.GameID] = m
			}
		}
		names, err := h.AccountNames(ctx, ids)
		if err != nil {
			internalError(c, err)
			return
		}
		out := make([]EditFeedView, len(entries))
		for i, e := range entries {
			v := EditFeedView{GameID: e.GameID, Seq: e.Seq, Type: e.Type, Side: e.Side, Slot: e.Slot, PlayerID: e.PlayerID,
				TargetSeq: e.TargetSeq, At: e.CreatedAt.UnixMilli(), Reverted: reverted[e.GameID][e.Seq]}
			if e.AuthorAccountID != nil {
				if n, ok := names[*e.AuthorAccountID]; ok {
					v.Author = &n
				}
			}
			out[i] = v
		}
		c.JSON(http.StatusOK, out)
	})
}

// splitList splits repeated and comma-separated query values ("a,b&x=c" → a, b, c).
func splitList(vals []string) []string {
	var out []string
	for _, v := range vals {
		for _, x := range strings.Split(v, ",") {
			if x = strings.TrimSpace(x); x != "" {
				out = append(out, x)
			}
		}
	}
	return out
}
