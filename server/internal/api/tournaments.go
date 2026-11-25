package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"

	"github.com/Humbarrt/kicker/server/internal/auth"
	"github.com/Humbarrt/kicker/server/internal/engine"
	"github.com/Humbarrt/kicker/server/internal/store"
	"github.com/Humbarrt/kicker/server/internal/tournament"
	"github.com/Humbarrt/kicker/server/internal/tournaments"
)

// --- DTOs ---------------------------------------------------------------------------------------

// TournamentSummary is a row of the tournament list.
type TournamentSummary struct {
	ID        int64    `json:"id"`
	Name      string   `json:"name"`
	State     string   `json:"state"`
	TeamMode  string   `json:"team_mode"`
	Formats   []string `json:"formats"` // the stages' formats
	OwnerID   *int64   `json:"owner_account_id"`
	CreatedAt int64    `json:"created_at"`
	StartedAt *int64   `json:"started_at"`
	EndedAt   *int64   `json:"ended_at"`
}

// TournamentDetail is everything the tournament page shows (spec 09 §12).
type TournamentDetail struct {
	TournamentSummary
	Settings     tournaments.Settings  `json:"settings"`
	Participants []ParticipantView     `json:"participants"`
	Teams        []EntrantView         `json:"teams"` // the first stage's entrants in seed order (once known)
	Stages       []StageView           `json:"stages"`
	Placements   []PlacementView       `json:"placements"`
	Ready        []string              `json:"ready"`     // ready pairing keys, in the suggested order of play
	Suggested    string                `json:"suggested"` // "" = none
	Staged       string                `json:"staged"`    // the pairing on the table ("" = none)
	CheckedIn    []int64               `json:"checked_in"`
	Finished     bool                  `json:"finished"`
	Estimate     *EstimateView         `json:"estimate"`
	Events       []TournamentEventView `json:"events"`
	Can          TournamentCan         `json:"can"`
	Preview      bool                  `json:"preview"` // setup: the bracket as it would start (fixed teams)
}

type ParticipantView struct {
	PlayerID int64 `json:"player_id"`
	Team     *int  `json:"team"`
	Seed     int   `json:"seed"`
	Dropped  bool  `json:"dropped"`
}

type EntrantView struct {
	Team    int     `json:"team"` // 0 for Monster DYP pairs
	Players []int64 `json:"players"`
	Bye     bool    `json:"bye,omitempty"`
}

type PairingView struct {
	Key      string            `json:"key"`
	Stage    int               `json:"stage"`
	Round    int               `json:"round"`
	Pos      int               `json:"pos"`
	Bracket  string            `json:"bracket"`
	State    string            `json:"state"`
	A        *EntrantView      `json:"a"`
	B        *EntrantView      `json:"b"`
	WinsA    int               `json:"wins_a"`
	WinsB    int               `json:"wins_b"`
	GoalsA   int               `json:"goals_a"`
	GoalsB   int               `json:"goals_b"`
	Winner   string            `json:"winner"` // a | b | ""
	Walkover bool              `json:"walkover"`
	BestOf   int               `json:"best_of"`
	Games    []PairingGameView `json:"games"`
}

type PairingGameView struct {
	GameID int64  `json:"game_id"`
	ASide  string `json:"a_side"` // where team A played
	Winner string `json:"winner"` // a | b
	GoalsA int    `json:"goals_a"`
	GoalsB int    `json:"goals_b"`
}

type StageView struct {
	Index     int            `json:"index"`
	Format    string         `json:"format"`
	BestOf    int            `json:"best_of"`
	Done      bool           `json:"done"`
	Groups    []GroupView    `json:"groups"`
	Pairings  []PairingView  `json:"pairings"`
	Standings []StandingView `json:"standings"` // round robin, Swiss, Monster DYP
	SatOut    [][]int64      `json:"sat_out,omitempty"`
}

type GroupView struct {
	Name      string         `json:"name"`
	Teams     []EntrantView  `json:"teams"`
	Standings []StandingView `json:"standings"`
}

type StandingView struct {
	ID           int64   `json:"id"` // team id, or player id (Monster DYP)
	Players      []int64 `json:"players"`
	Seed         int     `json:"seed"`
	Played       int     `json:"played"`
	Won          int     `json:"won"`
	Lost         int     `json:"lost"`
	Points       int     `json:"points"`
	PerGame      float64 `json:"per_game"`
	GamesWon     int     `json:"games_won"`
	GamesLost    int     `json:"games_lost"`
	GoalsFor     int     `json:"goals_for"`
	GoalsAgainst int     `json:"goals_against"`
	Buchholz     int     `json:"buchholz"`
}

type PlacementView struct {
	Place   int     `json:"place"`
	Team    int     `json:"team"`
	Players []int64 `json:"players"`
}

type EstimateView struct {
	Pairings        int     `json:"pairings"`
	UpTo            bool    `json:"up_to"`
	Games           float64 `json:"games"`
	Minutes         float64 `json:"minutes"`
	MinGamesPerTeam int     `json:"min_games_per_team"`
}

type TournamentEventView struct {
	At        int64           `json:"at"`
	AccountID *int64          `json:"account_id"`
	Kind      string          `json:"kind"`
	Details   json.RawMessage `json:"details"`
}

// TournamentCan tells the UI what the account may do with this tournament.
type TournamentCan struct {
	Edit   bool `json:"edit"`   // change the setup (tournaments.edit)
	Run    bool `json:"run"`    // start, pause, continue (tournaments.run)
	Play   bool `json:"play"`   // put a ready pairing on the table (table.control)
	Manage bool `json:"manage"` // abandon, reinstate, delete (tournaments.manage)
}

// OptionView is one format choice of the setup page.
type OptionView struct {
	Name     string             `json:"name"`
	Stages   []tournament.Stage `json:"stages"`
	Estimate EstimateView       `json:"estimate"`
}

func estimateView(e tournament.Estimate) EstimateView {
	return EstimateView{Pairings: e.Pairings, UpTo: e.UpTo, Games: e.Games, Minutes: e.Duration.Minutes(),
		MinGamesPerTeam: e.MinGamesPerTeam}
}

func ms(t *time.Time) *int64 {
	if t == nil {
		return nil
	}
	v := t.UnixMilli()
	return &v
}

func summaryOf(t store.Tournament) TournamentSummary {
	var set tournaments.Settings
	_ = json.Unmarshal(t.Settings, &set)
	s := TournamentSummary{ID: t.ID, Name: t.Name, State: t.State, TeamMode: set.TeamMode, Formats: []string{},
		OwnerID: t.OwnerAccountID, CreatedAt: t.CreatedAt.UnixMilli(), StartedAt: ms(t.StartedAt), EndedAt: ms(t.EndedAt)}
	for _, st := range set.Stages {
		s.Formats = append(s.Formats, string(st.Format))
	}
	return s
}

func entrantView(e *tournament.Entrant) *EntrantView {
	if e == nil {
		return nil
	}
	return &EntrantView{Team: e.Team, Players: append([]int64{}, e.Players...), Bye: e.Bye}
}

func standingViews(rows []tournament.Row) []StandingView {
	out := make([]StandingView, 0, len(rows))
	for _, r := range rows {
		out = append(out, StandingView{ID: r.ID, Players: append([]int64{}, r.Entrant.Players...), Seed: r.Seed,
			Played: r.Played, Won: r.Won, Lost: r.Lost, Points: r.Points, PerGame: r.PerGame(),
			GamesWon: r.GamesWon, GamesLost: r.GamesLost, GoalsFor: r.GoalsFor, GoalsAgainst: r.GoalsAgainst, Buchholz: r.Buchholz})
	}
	return out
}

func sideName(s tournament.Side) string {
	switch s {
	case tournament.A:
		return "a"
	case tournament.B:
		return "b"
	}
	return ""
}

// --- routes ---------------------------------------------------------------------------------------

func (o Options) tournamentRoutes(api *gin.RouterGroup) {
	svc := o.Tournaments
	view := api.Group("", o.Require(auth.ViewTournaments))

	view.GET("/tournaments", func(c *gin.Context) {
		ts, err := svc.Tournaments(c.Request.Context())
		if err != nil {
			internalError(c, err)
			return
		}
		out := make([]TournamentSummary, 0, len(ts))
		for _, t := range ts {
			out = append(out, summaryOf(t))
		}
		c.JSON(http.StatusOK, out)
	})

	api.GET("/tournaments/options", o.Require(auth.TournamentsCreate), func(c *gin.Context) {
		n, err := strconv.Atoi(c.Query("teams"))
		if err != nil || n < 0 || n > 256 {
			fail(c, http.StatusBadRequest, "invalid_input", "teams: 0–256")
			return
		}
		opts, err := svc.Options(c.Request.Context(), n, c.Query("monster") == "1")
		if err != nil {
			internalError(c, err)
			return
		}
		out := make([]OptionView, 0, len(opts))
		for _, op := range opts {
			out = append(out, OptionView{Name: op.Name, Stages: op.Stages, Estimate: estimateView(op.Estimate)})
		}
		c.JSON(http.StatusOK, out)
	})

	view.GET("/tournaments/:id", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		d, err := o.tournamentDetail(c, id)
		if err != nil {
			tournamentError(c, err)
			return
		}
		c.JSON(http.StatusOK, d)
	})

	api.POST("/tournaments", o.Require(auth.TournamentsCreate), func(c *gin.Context) {
		var in tournaments.Setup
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		id, err := svc.Create(c.Request.Context(), in, authorPtr(c))
		if err != nil {
			tournamentError(c, err)
			return
		}
		o.audit(c, "tournament_created", strconv.FormatInt(id, 10), in.Name)
		d, err := o.tournamentDetail(c, id)
		if err != nil {
			tournamentError(c, err)
			return
		}
		c.JSON(http.StatusCreated, d)
	})

	api.PUT("/tournaments/:id", func(c *gin.Context) {
		id, _, ok := o.tournamentFor(c, auth.TournamentsEdit)
		if !ok {
			return
		}
		var in tournaments.Setup
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		if err := svc.Update(c.Request.Context(), id, in, authorPtr(c)); err != nil {
			tournamentError(c, err)
			return
		}
		o.respondTournament(c, id)
	})

	// The lifecycle (spec 09 §3): tournaments.run for the account's own or all tournaments.
	for path, f := range map[string]func(context.Context, int64, *int64) error{
		"start": svc.Start, "pause": svc.Pause, "continue": svc.Continue,
	} {
		api.POST("/tournaments/:id/"+path, func(c *gin.Context) {
			id, _, ok := o.tournamentFor(c, auth.TournamentsRun)
			if !ok {
				return
			}
			if err := f(c.Request.Context(), id, authorPtr(c)); err != nil {
				tournamentError(c, err)
				return
			}
			o.audit(c, "tournament_"+path, strconv.FormatInt(id, 10), "")
			o.respondTournament(c, id)
		})
	}

	// "Play": a ready pairing on the table now (spec 09 §8, table.control).
	api.POST("/tournaments/:id/play", o.Require(auth.TableControl), func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		var in struct {
			Pairing string `json:"pairing" binding:"required"`
		}
		if err := c.ShouldBindJSON(&in); err != nil {
			fail(c, http.StatusBadRequest, "invalid_input", err.Error())
			return
		}
		if err := svc.Play(c.Request.Context(), id, in.Pairing); err != nil {
			tournamentError(c, err)
			return
		}
		o.respondTournament(c, id)
	})

	manage := api.Group("", o.Require(auth.TournamentsManage))
	manage.POST("/tournaments/:id/abandon", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		if err := svc.Abandon(c.Request.Context(), id, authorPtr(c)); err != nil {
			tournamentError(c, err)
			return
		}
		o.audit(c, "tournament_abandoned", strconv.FormatInt(id, 10), "")
		o.respondTournament(c, id)
	})
	manage.POST("/tournaments/:id/reinstate", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		if err := svc.Reinstate(c.Request.Context(), id, authorPtr(c)); err != nil {
			tournamentError(c, err)
			return
		}
		o.audit(c, "tournament_reinstated", strconv.FormatInt(id, 10), "")
		o.respondTournament(c, id)
	})
	manage.DELETE("/tournaments/:id", func(c *gin.Context) {
		id, ok := idParam(c, "id")
		if !ok {
			return
		}
		if err := svc.Delete(c.Request.Context(), id); err != nil {
			tournamentError(c, err)
			return
		}
		o.audit(c, "tournament_deleted", strconv.FormatInt(id, 10), "")
		c.Status(http.StatusNoContent)
	})
}

// tournamentFor loads a tournament and checks a scoped permission (own = the account created it).
func (o Options) tournamentFor(c *gin.Context, key string) (int64, store.Tournament, bool) {
	id, ok := idParam(c, "id")
	if !ok {
		return 0, store.Tournament{}, false
	}
	t, _, _, err := o.Tournaments.Get(c.Request.Context(), id)
	if err != nil {
		tournamentError(c, err)
		return 0, t, false
	}
	if !o.mayTournament(c, key, t) {
		deny(c)
		return 0, t, false
	}
	return id, t, true
}

func (o Options) mayTournament(c *gin.Context, key string, t store.Tournament) bool {
	switch o.perms(c).Value(key) {
	case auth.ScopeAll:
		return true
	case auth.ScopeOwn:
		a := account(c)
		return a != nil && t.OwnerAccountID != nil && *t.OwnerAccountID == a.ID
	}
	return false
}

func (o Options) respondTournament(c *gin.Context, id int64) {
	d, err := o.tournamentDetail(c, id)
	if err != nil {
		tournamentError(c, err)
		return
	}
	c.JSON(http.StatusOK, d)
}

// tournamentDetail derives a tournament for the page. In setup, fixed teams get a preview of the
// bracket; drawn teams (DYP, Monster DYP) are only known at the start.
func (o Options) tournamentDetail(c *gin.Context, id int64) (TournamentDetail, error) {
	ctx := c.Request.Context()
	svc := o.Tournaments
	t, ps, evs, err := svc.Get(ctx, id)
	if err != nil {
		return TournamentDetail{}, err
	}
	d := TournamentDetail{TournamentSummary: summaryOf(t), Participants: []ParticipantView{}, Teams: []EntrantView{},
		Stages: []StageView{}, Placements: []PlacementView{}, Ready: []string{}, CheckedIn: []int64{}, Events: []TournamentEventView{}}
	_ = json.Unmarshal(t.Settings, &d.Settings)
	for _, p := range ps {
		d.Participants = append(d.Participants, ParticipantView{PlayerID: p.PlayerID, Team: p.TeamNo, Seed: p.Seed, Dropped: p.DroppedAt != nil})
	}
	for _, e := range evs {
		d.Events = append(d.Events, TournamentEventView{At: e.At.UnixMilli(), AccountID: e.AccountID, Kind: e.Kind, Details: e.Details})
	}
	d.Can = TournamentCan{Edit: t.State == "setup" && o.mayTournament(c, auth.TournamentsEdit, t),
		Run: o.mayTournament(c, auth.TournamentsRun, t), Play: o.perms(c).Has(auth.TableControl),
		Manage: o.perms(c).Has(auth.TournamentsManage)}

	if t.State == "setup" && d.Settings.TeamMode != tournaments.FixedTeams {
		// Drawn teams are known at the start: estimate with stand-ins.
		setup := tournament.Setup{}
		for _, s := range d.Settings.Stages {
			setup.Stages = append(setup.Stages, s.Stage)
		}
		if d.Settings.TeamMode == tournaments.Monster {
			setup.Players = make([]int64, len(ps))
		} else {
			setup.Teams = make([]tournament.Team, (len(ps)+1)/2)
		}
		if setup.Validate() == nil {
			if e, err := svc.Estimate(ctx, d.Settings, setup); err == nil {
				ev := estimateView(e)
				d.Estimate = &ev
			}
		}
		return d, nil
	}
	st, _, _, err := svc.State(ctx, id)
	if err != nil {
		if t.State == "setup" && errors.Is(err, tournaments.ErrBadSetup) {
			return d, nil // not complete yet: no preview
		}
		return d, err
	}
	d.Preview = t.State == "setup"
	if e, err := svc.Estimate(ctx, d.Settings, st.Setup); err == nil {
		ev := estimateView(e)
		d.Estimate = &ev
	}
	raw, err := svc.PairingGames(ctx, id)
	if err != nil {
		return d, err
	}
	for i, ss := range st.Stages {
		sv := StageView{Index: i, Format: string(ss.Stage.Format), BestOf: max(ss.Stage.BestOf, 1), Done: ss.Done,
			Groups: []GroupView{}, Pairings: []PairingView{}, Standings: []StandingView{}, SatOut: ss.SatOut}
		if i == 0 {
			for _, e := range ss.Entrants {
				d.Teams = append(d.Teams, *entrantView(e))
			}
		}
		for _, g := range ss.Groups {
			gv := GroupView{Name: g, Teams: []EntrantView{}, Standings: standingViews(st.Standings(i, g))}
			for _, e := range ss.GroupEntrants[g] {
				gv.Teams = append(gv.Teams, *entrantView(e))
			}
			sv.Groups = append(sv.Groups, gv)
		}
		if ss.Stage.Format != tournament.Groups {
			sv.Standings = standingViews(st.Standings(i, ""))
		}
		for _, p := range ss.Pairings {
			pv := PairingView{Key: p.Key, Stage: p.Stage, Round: p.Round, Pos: p.Pos, Bracket: p.Bracket, State: string(p.State),
				A: entrantView(p.A), B: entrantView(p.B), WinsA: p.WinsA, WinsB: p.WinsB, GoalsA: p.GoalsA, GoalsB: p.GoalsB,
				Winner: sideName(p.Winner), Walkover: p.Walkover, BestOf: max(p.BestOf, 1), Games: []PairingGameView{}}
			for _, g := range raw[p.Key].Games {
				gv := PairingGameView{GameID: g.GameID, ASide: g.ASide, GoalsA: g.HomeGoals, GoalsB: g.AwayGoals}
				if g.ASide == "away" {
					gv.GoalsA, gv.GoalsB = g.AwayGoals, g.HomeGoals
				}
				if g.Winner != nil {
					gv.Winner = "b"
					if *g.Winner == g.ASide {
						gv.Winner = "a"
					}
				}
				pv.Games = append(pv.Games, gv)
			}
			sv.Pairings = append(sv.Pairings, pv)
		}
		d.Stages = append(d.Stages, sv)
	}
	for _, p := range st.Placements {
		d.Placements = append(d.Placements, PlacementView{Place: p.Place, Team: p.Team, Players: append([]int64{}, p.Players...)})
	}
	d.Finished = st.Finished
	if t.State == "running" {
		for _, p := range st.ReadyPairings() {
			d.Ready = append(d.Ready, p.Key)
		}
		active, staged := svc.Staged()
		if active == id {
			d.Staged = staged
			d.CheckedIn = svc.CheckedIn()
		}
		if sug := st.Suggest(st.Pairing(svc.LastPairing())); sug != nil {
			d.Suggested = sug.Key
		}
	}
	return d, nil
}

func authorPtr(c *gin.Context) *int64 {
	if a := account(c); a != nil {
		return &a.ID
	}
	return nil
}

// tournamentError maps service and store errors to HTTP.
func tournamentError(c *gin.Context, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(c, http.StatusNotFound, "not_found", "no such tournament")
	case errors.Is(err, tournaments.ErrBadSetup):
		fail(c, http.StatusBadRequest, "invalid_setup", err.Error())
	case errors.Is(err, tournaments.ErrOtherRunning):
		fail(c, http.StatusConflict, "other_running", err.Error())
	case errors.Is(err, tournaments.ErrNotRunning):
		fail(c, http.StatusConflict, "not_running", err.Error())
	case errors.Is(err, tournaments.ErrNotReady):
		fail(c, http.StatusConflict, "not_ready", err.Error())
	case errors.Is(err, store.ErrTournamentState):
		fail(c, http.StatusConflict, "wrong_state", err.Error())
	case errors.Is(err, store.ErrInUse):
		fail(c, http.StatusConflict, "in_use", "games were played in this tournament: abandon it instead")
	case errors.Is(err, engine.ErrTournament):
		fail(c, http.StatusConflict, "tournament", err.Error())
	case errors.Is(err, engine.ErrInvalidInput):
		engineError(c, err)
	default:
		internalError(c, err)
	}
}
