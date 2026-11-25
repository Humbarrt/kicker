# Spec 09: Tournaments

Status: **Draft** (2026-11-25, M6). Builds on D11 and D16 (`00-architecture-decisions.md`), the game engine (spec 02), ratings (D9) and the permissions of spec 03 §4.
Proposals marked *(proposal)* fill gaps D11 leaves open; the owner's decisions are collected in §15.

## 1. Scope
- Creating a tournament: participants, team building, format, rules – chosen by **estimated duration** (D11).
- Running it on the table: the next pairing goes to the table, its games are normal games, results flow back by themselves.
- Brackets, group tables, standings, placements; pause, resume, dropouts, replacements.
- Not in scope: several tables in one tournament (one kicker = one table; the duration estimate keeps a "tables" factor of 1 for later), online sign-up by phone (M4 adds joining via tag), prize money, seeding by hand beyond swapping teams before the start.

## 2. Terms
| Term | Meaning |
|---|---|
| **Tournament** | name, owner (account), state, settings, its stages |
| **Participant** | a player in the tournament (guests allowed, D11) |
| **Team** | 1 or 2 participants. Fixed for the whole tournament, except in Monster DYP |
| **Stage** | one format applied to some teams: e.g. "groups" then "knockout". Each stage has its own rules and games per pairing |
| **Round** | the pairings of a stage that can be played without waiting for each other |
| **Pairing** | team A vs team B in a stage: one row in a bracket or table. Made of 1..n games (D11) |
| **Game** | a normal game (spec 02) with a `pairing_id`: rated and counted in stats like any other |

## 3. Lifecycle
`setup → running ⇄ paused → finished | abandoned` (D11)
- **setup:** everything can be changed; nothing is on the table yet.
- **running:** the tournament owns the table (§8). Pairings are played in order.
- **paused:** by hand, or when the table goes to sleep during a tournament game (the game is paused with it, D16). Paused tournaments are listed and can be picked up later; on wake the table offers to continue it.
- **finished:** all placements are known. Read-only, except reverting an edit (§11).
- **abandoned:** stopped for good by `tournaments.manage`. Played games stay valid and rated; the standings at that moment are kept, no placements. Can be reinstated to `paused`.

## 4. Setup
1. **Name** (default "Tournament <date>"), **participants**: pick players, add new ones or the guest. Players can also be added by inserting their coin or tapping a corner tag while the setup page is open *(proposal)*.
2. **Team building** (D11):
   - **Fixed teams:** made by hand (drag players together), or 1-player teams (1 against 1).
   - **DYP:** teams drawn at random once, at the start. Can be redrawn while in setup.
   - **Monster DYP:** new random partner every round, individual ranking (§6.6).
   - Odd number of players for 2v2: one solo team (fixed, DYP), or one player sits out each round in rotation (Monster DYP) (D11).
3. **Format** (§6): every option that fits the number of teams, **sorted by estimated duration**, each showing: number of games, estimated time, minimum games per team (D11). The first one is preselected.
4. **Rules per stage:** the preset (default: the "Tournament" preset – time limit + golden goal, no draws), games per pairing (1 | best of 3 | best of 5), e.g. "final best of 3". Who gets the first ball (§7.3).
5. **Placements:** 3rd place game or shared 3rd (knockout formats); tie-breaks (§9).
6. **Start** → `running`. The teams and the bracket are fixed now; DYP draws happen here.

### Duration estimate (D11)
`games × (game length + changeover) ÷ tables`
- **game length:** the stage's time limit if it has one, otherwise the average length of this kicker's finished games with the same preset (≥ 10 games), otherwise 6 min *(proposal)*. Best-of-n counts the expected number of games (best of 3 ≈ 2.5).
- **changeover:** 1 min *(proposal; setting)*.
- **games:** exact for knockout, round robin, groups, Swiss; for double knockout the maximum (with bracket reset) is shown as "up to".

## 5. Seeding
- Teams are seeded by rating (D11): a 1-player team by the player's `solo` rating, a 2-player team by the average of both players' `team` ratings. Guests have the fixed guest rating (D9). Ties by random draw.
- Seeding decides byes (knockout), group draws (snake order: seed 1 → A, 2 → B, 3 → C, 4 → C, 5 → B, 6 → A, …), the first Swiss round.
- In setup the owner can swap two teams' seeds.

## 6. Formats
### 6.1 Round robin
Every team plays every other team once (twice: setting *(proposal)*). Rounds by the circle method, so no team plays twice in a row where avoidable. Ranking §9.

### 6.2 Single knockout
- Bracket of the next power of 2; **byes go to the top seeds** (D11). Standard bracket order, so 1 and 2 can only meet in the final.
- 3rd place: a 3rd place game between the semi-final losers, or both share 3rd (setting).

### 6.3 Double knockout
- Winners and losers bracket; a team is out after two lost pairings.
- **Grand final:** winner of the winners bracket vs winner of the losers bracket. If the losers-bracket team wins it, a second deciding pairing is played (**bracket reset**, D11).
- Placements: 1st/2nd from the grand final, 3rd = loser of the losers-bracket final (no 3rd place game).

### 6.4 Groups → knockout
- Groups of 3–6 teams, as equal as possible (D11: unequal sizes allowed). The setup offers the group counts that fit.
- Each group is a round robin; group rankings use **points per game played** (D11), then the tie-breaks.
- Top 1 or 2 of each group advance *(proposal: setting, default 2)*; the knockout crosses groups (A1 vs B2, B1 vs A2, …) so teams from one group meet again as late as possible.

### 6.5 Swiss
- Rounds: ⌈log₂ teams⌉ by default, adjustable.
- Round 1 by seeding (top half vs bottom half); then teams with equal points play each other, **no rematches**; one **bye** per round if the number is odd, rotating to the lowest-ranked team without a bye yet. A bye counts as a win in the standings but not for ratings (D11).
- Optional final stage: top 4 (or 8) into a knockout.

### 6.6 Monster DYP
- Rounds like Swiss, but on **players**: each round all players are paired into new random teams (avoid repeating partners, then avoid repeating opponents), then teams are matched.
- **Individual ranking:** points of the games each player won, then the tie-breaks per player.
- Odd players: one sits out per round in rotation (D11); sitting out counts as neither win nor loss *(proposal)*.
- Number of rounds: chosen in setup (default so that everyone plays ≈ 5 games).

## 7. Pairings and games
### 7.1 A pairing
- Holds team A, team B, the games played, its state: `waiting` (teams not known yet) → `ready` → `playing` → `done` | `walkover`.
- **Best of n:** the pairing is won by the first team with ⌈n/2⌉ game wins; no further games are played after that.
- The tournament shows a pairing as one row (e.g. "2 : 1") with its games below (D11).

### 7.2 Order of play
- The next pairing is the **first `ready` pairing** in a fixed order: by round, then by bracket position / group *(proposal)*. Teams that just played get a pause where the order allows it (a team doesn't play twice in a row if another ready pairing exists).
- The tournament page shows "now", "next" and "after that", so players can get ready.
- `tournaments.run` can pick a different ready pairing by hand.

### 7.3 First ball and sides
- D11: the tournament decides the first ball. *(proposal)* Game 1: the higher seed chooses → by default team A (higher seed) is home and gets the first ball. Later games of a pairing: the loser of the previous game gets the first ball; sides swap every game.

### 7.4 Results
- A tournament game ends like any game (spec 02 §8); the tournament reads the winner and updates the pairing. Draws can't happen (golden goal).
- **Edited games** (spec 02 §11): an edit of a tournament game updates its pairing. If that changes who won a pairing whose follow-up pairings were already played, the tournament **doesn't rewrite history**: it shows a warning to `tournaments.manage`, who can rebuild the bracket from that point (§11).
- **Results without the table** *(proposal)*: `tournaments.run` can enter a game result by hand (e.g. the table broke). It becomes a finished game marked `manual`, rated like the others.

## 8. The table during a tournament
- While a tournament is **running**, the table is in **tournament mode**:
  - The scoreboard shows the tournament name, stage/round and the two team names above the score; the overview shows the bracket or standings instead of the free-play sections.
  - The current pairing's players are **registered by the tournament**, not by coins or tags. Coins and tags still wake the table; they don't register anyone. *(proposal: a coin of a player of the current pairing counts as "I'm here" for the smart wake.)*
  - Kickoff: as configured (`kickoff_requires`, spec 02 §6); "Start now" for `table.control`.
  - Free play is not possible; to play a free game, pause the tournament first.
- When a pairing is done, the next ready pairing is put on the table with the kickoff countdown – no button needed *(proposal)*.
- The table going to sleep pauses the tournament (§3). On wake: "Continue tournament X?" – continue / not now.

## 9. Rankings and tie-breaks
- **Points:** 1 per pairing won *(proposal; D11 has no draws)*. Groups: points per pairing played (D11).
- **Tie-breaks**, in this order (configurable: order and which ones):
  1. head-to-head (pairings between the tied teams)
  2. game difference (games won − lost, for best-of)
  3. goal difference
  4. goals scored
  5. Buchholz (Swiss/Monster DYP: sum of the opponents' points)
  6. seed
- **Placements:** knockout: final winner 1st, loser 2nd, 3rd by 3rd place game or shared; double knockout §6.3; round robin and Swiss: the ranking; groups → knockout: by the knockout.

## 10. Dropouts and replacements (D11)
- **Replacing a player** (`tournaments.edit`): applies from the next game on; played games keep their players.
- **A team drops out:**
  - Knockout: the opponent advances by **walkover**, now and for the pairings it would have reached.
  - Groups and round robin: if fewer than 50 % of the team's pairings were played, they are **annulled** (removed from the standings); otherwise the remaining ones are **forfeits** (won by the opponent).
  - Swiss / Monster DYP: the team (player) is not paired any more; played pairings stay.
- Walkovers, forfeits and byes never count for ratings or player stats (D11) – they are pairing results without games.

## 11. Changing a running tournament
- `tournaments.edit` (own/all): rename, replace players, change the rules of stages not started yet.
- `tournaments.manage`: abandon, reinstate, delete (only without played games; otherwise abandon), **rebuild from a pairing**: discards the later pairings that depend on it (their played games stay as normal free-play games, unlinked) and draws them again.
- Every change is a tournament event (§13) and visible in the tournament's history, like game edits.

## 12. Pages
- **Tournaments** (header, `view.tournaments`): running/paused first, then finished; "New tournament" (`tournaments.create`).
- **Tournament page:** header (state, current round, "now / next"), the stage views – **bracket** (knockout, double knockout with both brackets), **group tables**, **standings** (Swiss, round robin, Monster DYP) – the list of pairings with their games, the placements, the history. Live over SSE.
- **Setup page:** the steps of §4 on one page, the format list with the estimates.
- **Player profile:** tournaments played, best placement, medals (1st/2nd/3rd counts).
- **Scoreboard:** tournament mode (§8).

## 13. Data model and code
- Package `tournament/`: **pure functions**, like `engine/` – `Plan(setup) → stages and pairings`, `Advance(state, result) → new pairings`, `Standings(state)`, the duration estimate. No I/O; tested with scenario files like the engine (`tournament/testdata/*.txt`).
- The **state is derived** from the setup and the pairing results, so an edit or a rebuild just recomputes it.
- Tables *(proposal)*:
  - `tournaments` (id, name, owner_account_id, state, settings JSON, created_at, started_at, ended_at)
  - `tournament_participants` (tournament_id, player_id, team_no NULL, seed, dropped_at)
  - `tournament_stages` (tournament_id, no, format, settings JSON incl. rules + games per pairing)
  - `tournament_pairings` (id, tournament_id, stage_no, round, position, team_a, team_b, state, result, winner)
  - `games.pairing_id` (already in the projections, spec 04 M3)
  - `tournament_events` (id, tournament_id, at, account_id, kind, details JSON): created, started, paused, resumed, player replaced, dropout, rebuilt, finished, abandoned.
- The table actor gets the "current pairing" from the tournament service and starts its games with the stage's rules and the registered teams (spec 02 §6: "the rules … from the tournament stage").
- SSE topic `tournament` (state changes, results).

## 14. Milestone tasks (M6, proposed order)
1. `tournament/` core: formats, seeding, byes, pairing order, standings + tie-breaks, duration estimate (+ scenarios).
2. Store: tables, events; `games.pairing_id` wiring.
3. Table integration: tournament mode, registering pairings, results back, pause/sleep/continue.
4. API + permissions (`tournaments.*`), SSE.
5. Web: setup page with the format list, tournament page (bracket, groups, standings, now/next), scoreboard tournament mode, profile additions.
6. Dropouts, replacements, manual results, rebuild.
7. Load: a 64-team double knockout and a 40-player Monster DYP through `--sim`; e2e test of a small tournament.

## 15. Open questions for the owner
1. **Points and ranking:** 1 point per pairing won, tie-breaks as in §9 – or a different scheme (e.g. 3/0, or games instead of pairings)?
2. **Groups → knockout:** how many advance per group (default 2)? Cross-seeding as proposed?
3. **First ball:** higher seed in game 1, then the loser of the previous game (§7.3)?
4. **Tournament mode:** the tournament owns the table while running, free play only after pausing it (§8)? And the next pairing starts by itself with the countdown?
5. **Manual results** for games not played on the table (§7.4): wanted? Rated?
6. **Monster DYP:** sitting out = no points (§6.6)? Default number of rounds?
7. **Defaults for the estimate:** 6 min per game without data, 1 min changeover?
8. **Joining in setup with coins/tags** (§4.1): wanted now, or only by picking players?
