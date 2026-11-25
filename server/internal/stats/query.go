package stats

import (
	"cmp"
	"context"
	"fmt"
	"math"
	"slices"
	"time"

	"github.com/Humbarrt/kicker/server/internal/store"
)

// Stats views (M3 task 8): player profiles, the leaderboard and the overview below the
// scoreboard. Everything counts rated games only (D9: finished, not voided, a player on each side);
// the guest and unknown coins have no stats of their own.

const (
	// MinRanked is how many games in a key a player needs to get a rank on the leaderboard.
	MinRanked = 5
	// MinTogether is how many games two players need together before they can be someone's best
	// teammate or worst enemy.
	MinTogether = 3
	// MaxHistoryPoints caps the points of a rating chart; longer histories are thinned out.
	MaxHistoryPoints = 300
	// FormLength is how many recent results the profile shows; the leaderboard shows LeaderForm.
	FormLength = 10
	LeaderForm = 5
	// HotStreak is the shortest current win streak the overview mentions.
	HotStreak = 3
	// OverviewWindow is the period of the overview's numbers and highlights and of the rating
	// change on the leaderboard.
	OverviewWindow = 30 * 24 * time.Hour
)

// Reader answers the stats queries.
type Reader struct {
	St    *store.Store
	Now   func() time.Time // default time.Now
	Cache *Cache           // optional: serve from it (NewCache)
}

// Profile computes a player's stats.
func (r Reader) Profile(ctx context.Context, playerID int64) (Profile, error) {
	return cached(r, fmt.Sprintf("profile/%d", playerID), func(ctx context.Context) (Profile, error) { return r.profile(ctx, playerID) })(ctx)
}

// Leaderboard ranks the players of one key (archived players are left out).
func (r Reader) Leaderboard(ctx context.Context, key string) (Leaderboard, error) {
	return cached(r, "leaderboard/"+key, func(ctx context.Context) (Leaderboard, error) { return r.leaderboard(ctx, key) })(ctx)
}

// Overview computes the overview.
func (r Reader) Overview(ctx context.Context) (Overview, error) {
	return cached(r, "overview", func(ctx context.Context) (Overview, error) { return r.overview(ctx) })(ctx)
}

func (r Reader) now() time.Time {
	if r.Now != nil {
		return r.Now()
	}
	return time.Now()
}

// RecordView is a win/loss record.
type RecordView struct {
	Games        int `json:"games"`
	Wins         int `json:"wins"`
	Losses       int `json:"losses"`
	Draws        int `json:"draws"`
	GoalsFor     int `json:"goals_for"`
	GoalsAgainst int `json:"goals_against"`
}

func recordView(r store.Record) RecordView {
	return RecordView{Games: r.Games(), Wins: r.Wins, Losses: r.Losses, Draws: r.Draws, GoalsFor: r.GoalsFor, GoalsAgainst: r.GoalsAgainst}
}

// HistoryPoint is a player's rating after a game.
type HistoryPoint struct {
	GameID int64   `json:"game_id"`
	At     int64   `json:"at"` // Unix ms
	Rating float64 `json:"rating"`
}

// KeyStats is a player's rating and record in one key.
type KeyStats struct {
	Key     string         `json:"key"` // solo | team
	Rating  float64        `json:"rating"`
	Peak    float64        `json:"peak"`
	Start   float64        `json:"start"` // the rating before the first game
	Record  RecordView     `json:"record"`
	History []HistoryPoint `json:"history"` // oldest first, at most MaxHistoryPoints
	Rank    int            `json:"rank"`    // place on the leaderboard; 0 = not ranked (yet)
	Ranked  int            `json:"ranked"`  // how many players have a rank in this key
}

// RelationView is a player's record with or against another player.
type RelationView struct {
	PlayerID int64      `json:"player_id"`
	Record   RecordView `json:"record"`
}

// Profile is everything a player profile shows besides the player itself.
type Profile struct {
	PlayerID int64      `json:"player_id"`
	Record   RecordView `json:"record"` // both keys
	Keys     []KeyStats `json:"keys"`
	// Streak is the current run: n > 0 wins, n < 0 losses, 0 after a draw or without games.
	Streak     int      `json:"streak"`
	BestStreak int      `json:"best_streak"` // the longest run of wins
	Form       []string `json:"form"`        // the last results, newest first: win | loss | draw
	LastPlayed *int64   `json:"last_played"` // Unix ms

	BestTeammate   *RelationView  `json:"best_teammate"`   // the highest win rate together (≥ MinTogether games)
	WorstEnemy     *RelationView  `json:"worst_enemy"`     // the opponent who beats them most often (≥ MinTogether games)
	FavoriteVictim *RelationView  `json:"favorite_victim"` // the opponent they beat most often (≥ MinTogether games)
	Teammates      []RelationView `json:"teammates"`       // most games first
	Opponents      []RelationView `json:"opponents"`
	MinTogether    int            `json:"min_together"`
	MinRanked      int            `json:"min_ranked"`
}

func (r Reader) profile(ctx context.Context, playerID int64) (Profile, error) {
	p := Profile{PlayerID: playerID, Form: []string{}, Keys: []KeyStats{}, Teammates: []RelationView{}, Opponents: []RelationView{},
		MinTogether: MinTogether, MinRanked: MinRanked}
	rows, err := r.St.PlayerResults(ctx, playerID)
	if err != nil {
		return p, err
	}
	var total store.Record
	keys := map[string]*KeyStats{}
	records := map[string]*store.Record{}
	run := 0
	for _, x := range rows {
		total.Add(x.Result, x.GoalsFor, x.GoalsAgainst)
		k := keys[x.Key]
		if k == nil {
			k = &KeyStats{Key: x.Key, Start: x.Before, Peak: x.Before}
			keys[x.Key] = k
			records[x.Key] = &store.Record{}
		}
		records[x.Key].Add(x.Result, x.GoalsFor, x.GoalsAgainst)
		k.Rating = x.After
		k.Peak = max(k.Peak, x.After)
		k.History = append(k.History, HistoryPoint{GameID: x.GameID, At: x.EndedAt.UnixMilli(), Rating: round1(x.After)})
		switch x.Result {
		case "win":
			run = max(run, 0) + 1
			p.BestStreak = max(p.BestStreak, run)
		case "loss":
			run = min(run, 0) - 1
		default:
			run = 0
		}
	}
	p.Streak = run
	p.Record = recordView(total)
	for i := len(rows) - 1; i >= 0 && len(p.Form) < FormLength; i-- {
		p.Form = append(p.Form, rows[i].Result)
	}
	if n := len(rows); n > 0 {
		at := rows[n-1].EndedAt.UnixMilli()
		p.LastPlayed = &at
	}
	for _, key := range []string{"solo", "team"} {
		if k := keys[key]; k != nil {
			k.Record = recordView(*records[key])
			k.History = thin(k.History, MaxHistoryPoints)
			if k.Rank, k.Ranked, err = r.rank(ctx, key, playerID); err != nil {
				return p, err
			}
			p.Keys = append(p.Keys, *k)
		}
	}

	guest, err := r.St.GuestPlayer(ctx)
	if err != nil {
		return p, err
	}
	rels, err := r.St.PlayerRelations(ctx, playerID, guest.ID, rows)
	if err != nil {
		return p, err
	}
	for _, x := range rels {
		v := RelationView{PlayerID: x.PlayerID, Record: recordView(x.Record)}
		if x.Teammate {
			p.Teammates = append(p.Teammates, v)
		} else {
			p.Opponents = append(p.Opponents, v)
		}
	}
	byGames := func(a, b RelationView) int {
		return cmp.Or(cmp.Compare(b.Record.Games, a.Record.Games), cmp.Compare(a.PlayerID, b.PlayerID))
	}
	slices.SortFunc(p.Teammates, byGames)
	slices.SortFunc(p.Opponents, byGames)
	winRate := func(v RelationView) float64 { return float64(v.Record.Wins) / float64(v.Record.Games) }
	lossRate := func(v RelationView) float64 { return float64(v.Record.Losses) / float64(v.Record.Games) }
	p.BestTeammate = best(p.Teammates, winRate)
	p.WorstEnemy = best(p.Opponents, lossRate)
	p.FavoriteVictim = best(p.Opponents, winRate)
	return p, nil
}

// rank returns a player's place on a key's leaderboard (0 = not ranked) and the number of
// ranked players – the same order as Leaderboard.
func (r Reader) rank(ctx context.Context, key string, playerID int64) (rank, ranked int, err error) {
	list, err := r.St.Leaderboard(ctx, key, 100000)
	if err != nil {
		return 0, 0, err
	}
	for _, x := range list {
		if x.Games < MinRanked {
			continue
		}
		ranked++
		if x.PlayerID == playerID {
			rank = ranked
		}
	}
	return rank, ranked, nil
}

// best picks the relation with the highest score among those with at least MinTogether games;
// more games break a tie (the list is sorted by games, so the first of equal scores wins). A score
// of 0 (never won together, never lost against) picks nobody.
func best(list []RelationView, score func(RelationView) float64) *RelationView {
	var out *RelationView
	top := 0.0
	for i := range list {
		if list[i].Record.Games < MinTogether {
			continue
		}
		if s := score(list[i]); s > top {
			top, out = s, &list[i]
		}
	}
	if out == nil {
		return nil
	}
	v := *out
	return &v
}

// thin keeps at most n points: evenly spread, always the first and the last.
func thin(pts []HistoryPoint, n int) []HistoryPoint {
	if len(pts) <= n || n < 2 {
		return pts
	}
	out := make([]HistoryPoint, 0, n)
	step := float64(len(pts)-1) / float64(n-1)
	for i := range n {
		out = append(out, pts[int(math.Round(float64(i)*step))])
	}
	return out
}

func round1(v float64) float64 { return math.Round(v*10) / 10 }

// LeaderRow is one player on the leaderboard.
type LeaderRow struct {
	Rank     int        `json:"rank"` // 0 = not ranked yet (fewer than MinRanked games)
	PlayerID int64      `json:"player_id"`
	Rating   float64    `json:"rating"`
	Record   RecordView `json:"record"`
	Form     []string   `json:"form"`   // the last results in this key, newest first
	Change   float64    `json:"change"` // rating change within OverviewWindow
}

// Leaderboard is the ranking of one key.
type Leaderboard struct {
	Key        string      `json:"key"`
	MinRanked  int         `json:"min_ranked"`
	WindowDays int         `json:"window_days"` // the period of LeaderRow.Change
	Rows       []LeaderRow `json:"rows"`        // ranked first (best first), then the unranked by games
}

func (r Reader) leaderboard(ctx context.Context, key string) (Leaderboard, error) {
	lb := Leaderboard{Key: key, MinRanked: MinRanked, WindowDays: int(OverviewWindow / (24 * time.Hour)), Rows: []LeaderRow{}}
	list, err := r.St.Leaderboard(ctx, key, 1000)
	if err != nil {
		return lb, err
	}
	change, err := r.changes(ctx)
	if err != nil {
		return lb, err
	}
	var unranked []LeaderRow
	for _, x := range list {
		row := LeaderRow{PlayerID: x.PlayerID, Rating: round1(x.Rating), Record: recordView(x.Record), Form: []string{},
			Change: round1(change[ratingKey{x.PlayerID, key}])}
		last, err := r.St.LastResults(ctx, x.PlayerID, key, LeaderForm)
		if err != nil {
			return lb, err
		}
		for _, l := range last {
			row.Form = append(row.Form, l.Result)
		}
		if x.Games >= MinRanked {
			row.Rank = len(lb.Rows) + 1
			lb.Rows = append(lb.Rows, row)
		} else {
			unranked = append(unranked, row)
		}
	}
	slices.SortStableFunc(unranked, func(a, b LeaderRow) int { return cmp.Compare(b.Record.Games, a.Record.Games) })
	lb.Rows = append(lb.Rows, unranked...)
	return lb, nil
}

type ratingKey struct {
	player int64
	key    string
}

// changes returns each player's rating change per key within OverviewWindow.
func (r Reader) changes(ctx context.Context) (map[ratingKey]float64, error) {
	rows, err := r.St.RatingChangesSince(ctx, r.now().Add(-OverviewWindow))
	if err != nil {
		return nil, err
	}
	out := map[ratingKey]float64{}
	for _, x := range rows {
		out[ratingKey{x.PlayerID, x.Key}] = x.Change
	}
	return out, nil
}

// Highlight is one remark of the overview.
type Highlight struct {
	Kind      string  `json:"kind"`       // streak | most_games | climber | biggest_win
	PlayerIDs []int64 `json:"player_ids"` // who it is about (the winners for biggest_win)
	Value     float64 `json:"value"`      // streak length, games, rating gain, goal difference
	GameID    *int64  `json:"game_id,omitempty"`
	Home      *int    `json:"home,omitempty"` // biggest_win: the score
	Away      *int    `json:"away,omitempty"`
}

// Overview is what the scoreboard page shows below the table.
type Overview struct {
	WindowDays int         `json:"window_days"`
	Games      int         `json:"games"` // finished games within the window
	Goals      int         `json:"goals"`
	Top        []LeaderTop `json:"top"` // the first three per key
	Highlights []Highlight `json:"highlights"`
}

// LeaderTop is the top of one key's leaderboard.
type LeaderTop struct {
	Key  string      `json:"key"`
	Rows []LeaderRow `json:"rows"`
}

func (r Reader) overview(ctx context.Context) (Overview, error) {
	since := r.now().Add(-OverviewWindow)
	o := Overview{WindowDays: int(OverviewWindow / (24 * time.Hour)), Top: []LeaderTop{}, Highlights: []Highlight{}}
	tot, err := r.St.GamesSince(ctx, since)
	if err != nil {
		return o, err
	}
	o.Games, o.Goals = tot.Games, tot.Goals
	rows, err := r.St.RatingChangesSince(ctx, since)
	if err != nil {
		return o, err
	}
	change := map[ratingKey]float64{}
	for _, x := range rows {
		change[ratingKey{x.PlayerID, x.Key}] = x.Change
	}
	// The top three per key (without their form, which the overview doesn't show).
	for _, key := range []string{"team", "solo"} {
		list, err := r.St.Leaderboard(ctx, key, 1000)
		if err != nil {
			return o, err
		}
		top := LeaderTop{Key: key, Rows: []LeaderRow{}}
		for _, x := range list {
			if x.Games < MinRanked {
				continue
			}
			top.Rows = append(top.Rows, LeaderRow{Rank: len(top.Rows) + 1, PlayerID: x.PlayerID, Rating: round1(x.Rating),
				Record: recordView(x.Record), Form: []string{}, Change: round1(change[ratingKey{x.PlayerID, key}])})
			if len(top.Rows) == 3 {
				break
			}
		}
		if len(top.Rows) > 0 {
			o.Top = append(o.Top, top)
		}
	}
	games := map[int64]int{}
	gain := map[int64]float64{}
	var players []int64
	for _, x := range rows {
		if _, ok := games[x.PlayerID]; !ok {
			players = append(players, x.PlayerID)
		}
		games[x.PlayerID] += x.Games
		gain[x.PlayerID] += x.Change
	}
	// The longest current win streak among the window's players. Most lost one of their last
	// few games; only the others need a longer look.
	var hot Highlight
	for _, id := range players {
		n := 0
		for _, limit := range []int{8, 100} {
			last, err := r.St.LastResults(ctx, id, "", limit)
			if err != nil {
				return o, err
			}
			n = 0
			for n < len(last) && last[n].Result == "win" {
				n++
			}
			if n < len(last) {
				break
			}
		}
		if n >= HotStreak && float64(n) > hot.Value {
			hot = Highlight{Kind: "streak", PlayerIDs: []int64{id}, Value: float64(n)}
		}
	}
	if hot.Kind != "" {
		o.Highlights = append(o.Highlights, hot)
	}
	if id, n := top(players, func(id int64) float64 { return float64(games[id]) }); n >= 2 {
		o.Highlights = append(o.Highlights, Highlight{Kind: "most_games", PlayerIDs: []int64{id}, Value: n})
	}
	if id, g := top(players, func(id int64) float64 { return gain[id] }); g >= 1 {
		o.Highlights = append(o.Highlights, Highlight{Kind: "climber", PlayerIDs: []int64{id}, Value: math.Round(g)})
	}
	if id, ok, err := r.St.BiggestWin(ctx, since); err != nil {
		return o, err
	} else if ok {
		g, _, err := r.St.Game(ctx, id)
		if err != nil {
			return o, err
		}
		ps, err := r.St.Participants(ctx, id)
		if err != nil {
			return o, err
		}
		h := Highlight{Kind: "biggest_win", PlayerIDs: []int64{}, Value: math.Abs(float64(g.HomeGoals - g.AwayGoals)), GameID: &id,
			Home: &g.HomeGoals, Away: &g.AwayGoals}
		for _, p := range ps {
			if g.Winner != nil && p.Side == *g.Winner && p.PlayerID != nil {
				h.PlayerIDs = append(h.PlayerIDs, *p.PlayerID)
			}
		}
		o.Highlights = append(o.Highlights, h)
	}
	return o, nil
}

// top returns the id with the highest value (the first of equal ones, i.e. who played first).
func top(ids []int64, value func(int64) float64) (int64, float64) {
	var best int64
	v := math.Inf(-1)
	for _, id := range ids {
		if x := value(id); x > v {
			best, v = id, x
		}
	}
	if best == 0 {
		return 0, 0
	}
	return best, v
}
