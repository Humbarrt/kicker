# Spec 09: Tournaments

Status: **Approved v1** (2026-11-25, M6; owner's decisions in §15). Builds on D11 and D16 (`00-architecture-decisions.md`), the game engine (spec 02), ratings (D9) and the permissions of spec 03 §4.
Where D11 left gaps, §15 records how they were decided.

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
`setup → running ⇄ paused → finished | abandoned` (D11); `finished → paused` when one of its games is voided (§11).
- **setup:** everything can be changed; nothing is on the table yet.
- **running:** the tournament owns the table (§8). Pairings are played in order.
- **paused:** by hand, or when the table goes to sleep during a tournament game (the game is paused with it, D16). Paused tournaments are listed and can be picked up later; on wake the table offers to continue it.
- **finished:** all placements are known. Its pairing results are **frozen** at the finish: later edits of its games change their stats and ratings, not the tournament (§11). Voiding one of its games reopens it (→ paused).
- **abandoned:** stopped for good by `tournaments.manage`. Played games stay valid and rated; the standings at that moment are kept, no placements. Can be reinstated to `paused`.

## 4. Setup
1. **Name** (default "Tournament <date>"), **participants**: pick players, add new ones or the guest. Players can also be added by inserting their coin or tapping a corner tag while the setup page is open.
2. **Team building** (D11):
   - **Fixed teams:** made by hand (drag players together), or 1-player teams (1 against 1).
   - **DYP:** teams drawn at random once, at the start. Can be redrawn while in setup.
   - **Monster DYP:** new random partner every round, individual ranking (§6.6).
   - Odd number of players for 2v2: one solo team (fixed, DYP), or one player sits out each round in rotation (Monster DYP) (D11).
3. **Format** (§6): every option that fits the number of teams, **sorted by estimated duration**, each showing: number of games, estimated time, minimum games per team (D11). The first one is preselected.
4. **Rules:** chosen like "New game" – one of the presets (default: "Official") or custom rules (goals to win, lead needed, max goals, time limit). The rules are copied into the tournament (a later preset edit doesn't change it; `settings.preset_id` only remembers the choice for the setup page) and always end in golden goal at the time limit, never a draw. The format options' estimates follow the chosen rules. Per stage: games per pairing (1 | best of 3 | best of 5), e.g. "final best of 3". Who gets the first ball (§7.3).
5. **Placements:** 3rd place game or shared 3rd (knockout formats); tie-breaks (§9).
6. **Start** → `running`. The teams and the bracket are fixed now; DYP draws happen here.

### Duration estimate (D11)
`games × (game length + changeover) ÷ tables`
- **game length** follows the stage's win conditions (D10: `win_score`, `win_by`, `max_score`, `time_limit_s`):
  - **expected goals** of a game: the winner's target score plus the loser's expected goals (≈ 0.6 × target, from this kicker's history once it has ≥ 10 games), at most `2 × max_score − 1`; `win_by` > 1 adds the expected extra goals near the end.
  - **time per goal:** this kicker's average play time per goal (≥ 10 games), otherwise 22 s – so the default first-to-10 game comes out at ≈ 6 min.
  - With a **time limit**, the game lasts the shorter of the two, plus ≈ 30 s for a golden goal when tied.
  - Best-of-n counts the expected number of games (best of 3 ≈ 2.5, best of 5 ≈ 4).
- **changeover:** 1 min (setting).
- **games:** exact for knockout, round robin, groups, Swiss; for double knockout the maximum (with bracket reset) is shown as "up to".

## 5. Seeding
- Teams are seeded by rating (D11): a 1-player team by the player's `solo` rating, a 2-player team by the average of both players' `team` ratings. Guests have the fixed guest rating (D9). Ties by random draw.
- Seeding decides byes (knockout), group draws (snake order: seed 1 → A, 2 → B, 3 → C, 4 → C, 5 → B, 6 → A, …), the first Swiss round.
- In setup the owner can swap two teams' seeds.

## 6. Formats
### 6.1 Round robin
Every team plays every other team once (twice: setting). Rounds by the circle method, so no team plays twice in a row where avoidable. Ranking §9.

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
- **Configurable** (owner, 2026-11-25):
  - how many advance per group: 1–4 (default 2), plus optionally the best third-placed teams to fill the bracket;
  - how the knockout is seeded: **crossed** (default: A1 vs B2, B1 vs A2, …, so teams from one group meet again as late as possible) or **re-seeded** by the group results (all group winners first, by points per game, then the runners-up, …; standard bracket order).

### 6.5 Swiss
- Rounds: ⌈log₂ teams⌉ by default, adjustable.
- Round 1 by seeding (top half vs bottom half); then teams with equal points play each other, **no rematches**; one **bye** per round if the number is odd, rotating to the lowest-ranked team without a bye yet. A bye counts as a win in the standings but not for ratings (D11).
- Optional final stage: top 4 (or 8) into a knockout.

### 6.6 Monster DYP
- Rounds like Swiss, but on **players**: each round all players are paired into new random teams (avoid repeating partners, then avoid repeating opponents), then teams are matched.
- **Individual ranking:** points of the games each player won, then the tie-breaks per player.
- Odd players: one sits out per round in rotation (D11); sitting out counts as neither win nor loss.
- Number of rounds: chosen in setup (default so that everyone plays ≈ 5 games).

## 7. Pairings and games
### 7.1 A pairing
- Holds team A, team B, the games played, its state: `waiting` (teams not known yet) → `ready` → `playing` → `done` | `walkover`.
- **Best of n:** the pairing is won by the first team with ⌈n/2⌉ game wins; no further games are played after that.
- The tournament shows a pairing as one row (e.g. "2 : 1") with its games below (D11).

### 7.2 Order of play
- **Any ready pairing can be played**, in or out of order: a pairing is `ready` as soon as both teams are known and neither team is playing. Pairings never wait for unrelated ones – if someone is at the loo, another ready pairing is played meanwhile (owner, 2026-11-25). Round-based formats (Swiss, Monster DYP) draw the next round only when the current one is complete, so there the choice is within the round.
- The tournament **suggests** the next pairing: the first ready one by round, then bracket position / group, skipping teams that just played where another ready pairing exists.
- The tournament page and the scoreboard show the suggestion ("next") and the other ready pairings, so players can get ready.

### 7.3 First ball and sides
- D11: the tournament decides the first ball. Game 1: the higher seed chooses → by default team A (higher seed) is home and gets the first ball. Later games of a pairing: the loser of the previous game gets the first ball; sides swap every game.

### 7.4 Results
- A tournament game ends like any game (spec 02 §8); the tournament reads the winner and updates the pairing. Draws can't happen (golden goal).
- **Edited games** (spec 02 §11): while the tournament runs or is paused, an edit of a tournament game updates its pairing. If that changes who won a pairing whose follow-up pairings were already played, the tournament **doesn't rewrite history**: those pairings are **conflicts**, shown with a warning, and `tournaments.manage` can rebuild them (§11). A finished tournament is frozen (§3, §11).
- **No results without the table** (owner, 2026-11-25): every pairing is played on the table, started by check-in or "Play"; goals added by hand during that game work as in any game. The table stands on its own.

## 8. The table during a tournament
- While a tournament is **running**, the table is in **tournament mode**:
  - The scoreboard shows the tournament name, stage/round and the two team names above the score; the overview shows the bracket or standings instead of the free-play sections.
  - Free play is not possible; to play a free game, pause the tournament first.
- **Starting a pairing** – the table knows who plays, so it never waits for registrations (owner, 2026-11-25):
  - **Check-in:** a coin or a corner tag of a tournament player means "I'm here". It doesn't register the player the normal way (spec 02 §4); the tournament puts the pairing's teams on the sides.
  - As soon as **one player of each team** of a ready pairing has checked in, that pairing goes on the table and the kickoff countdown runs (spec 02 §6). Usually that is the suggested pairing, but any ready pairing can start this way – out of order (§7.2).
  - If a team has several ready pairings (round robin), the pairing starts once the other team checks in too.
  - **"Play"** next to any ready pairing in the UI (`table.control`) puts it on the table at once, without check-ins.
  - Check-ins are kept until they are used or the table sleeps (coins are ejected at every game end anyway). Corner tags check in with M4.
  - The tournament decides sides and first ball (§7.3); the stage's rules apply.
- A finished game of a best-of pairing is followed by the next game of the same pairing with the countdown – the players are already there.
- The table going to sleep pauses the tournament (§3). On wake: "Continue tournament X?" – continue / not now.

## 9. Rankings and tie-breaks
- **Points:** 1 per pairing won (no draws, D11). Groups: points per pairing played (D11).
- **Tie-breaks**, in this order (configurable: order and which ones):
  1. head-to-head (pairings between the tied teams)
  2. game difference (games won − lost, for best-of)
  3. goal difference
  4. goals scored
  5. Buchholz (Swiss/Monster DYP: sum of the opponents' points)
  6. seed
- **Placements:** knockout: final winner 1st, loser 2nd, 3rd by 3rd place game or shared; double knockout §6.3; round robin and Swiss: the ranking; groups → knockout: by the knockout.

## 10. Dropouts and replacements (D11)
- **Replacing a player** (`tournaments.edit`, running or paused): the new player takes the old one's place – team and seed – from the next game on; played games keep their players. The old participant row stays, marked as replaced (`replaced_by`): the player keeps the tournament on their profile, marked "replaced", without a medal. In Monster DYP the substitute takes over the slot with its points; the standings show "Lena (for Tom)" (owner, 2026-11-25). A player can't take part twice (except the guest).
- **A team drops out** (`tournaments.edit`; in Monster DYP: a player). Not while its pairing is on the table – that game is finished or abandoned first. No undo; it is in the history.
  - Knockout and double knockout: every open pairing of the team goes to the opponent by **walkover**, now and for the pairings it would have reached.
  - Groups and round robin: if fewer than 50 % of the team's pairings were played, they are **annulled** (they don't count for anybody); otherwise the remaining ones are **forfeits** (walkovers for the opponents). Decided at the moment of the dropout and kept.
  - Swiss / Monster DYP: the team (player) is not drawn into rounds after the current one; its open pairing in the current round is a walkover; played pairings stay.
  - A team that dropped out is ranked last in that stage, **doesn't qualify** for the next stage (the next best moves up) and gets **no place**.
  - If both sides of a pairing dropped out, the one that dropped out later goes on (and loses its next pairing): what was decided stays decided.
- Walkovers, forfeits and byes never count for ratings or player stats (D11) – they are pairing results without games.
- In the core, a dropout is stored in the setup (`Setup.Drops`: stage, rounds drawn so far, annulled, order) and the state is derived as always: nothing that was already played changes.

## 11. Changing a running tournament
- `tournaments.edit` (own/all), running or paused: rename, set the rules of the stages that haven't started yet (preset or custom, like the setup), replace players, let teams drop out (§10).
- `tournaments.manage`: abandon, reinstate, delete (only without played games; otherwise abandon), **rebuild**.
- **Conflicts and rebuilding** (running or paused): every pairing remembers who it was put on the table for (`tournament_pairings.sides`, e.g. `t1-t4`). After an edit, a pairing whose games were played by other sides than the bracket has now is a conflict – e.g. a semi-final's winner changed after the final was played. The tournament page lists them with a warning and marks them in the bracket. **Rebuild** takes the games of the conflicting pairings (and of those drawn after them that change too) out of the tournament – they stay as normal, rated games – and those pairings are played again with the right teams. Not while a pairing is on the table.
- **Finished tournaments are frozen** (owner, 2026-11-25): at the finish the pairing results are stored with the tournament (`tournaments.frozen`). Its bracket, winner and medals come from them, so adding goals by hand afterwards can't win a tournament. **Voiding** a game of a finished tournament – any of its games, the final or an earlier one – reopens it: back to paused, the frozen results dropped, the pairing played again; later pairings that now disagree are conflicts as above. It freezes again when it finishes again.
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
- Tables (migration 0011):
  - `tournaments` (id, name, owner_account_id, state, settings JSON – the stages with their rules and games per pairing, tie-breaks, the draw seed –, created_at, started_at, ended_at)
  - `tournament_participants` (id, tournament_id, player_id, team_no NULL, seed, dropped_at) – the guest may take part several times
  - `tournament_pairings` (id, tournament_id, key, walkover NULL): a row only once a pairing is played or decided by walkover; the rest of the bracket is derived
  - `games.pairing_id` → that row, and `games.pairing_a_side` (home/away): where team A played, since sides swap every game
  - `tournament_events` (id, tournament_id, at, account_id, kind, details JSON): created, edited, started, paused, resumed, finished, abandoned, reinstated, reopened, player_replaced, dropout, walkover, rebuilt (manual_result: unused, §7.4).
  - Migration 0012: `tournament_participants.replaced_by`, `tournament_pairings.sides` (who it was put on the table for), `tournaments.frozen` (the results at the finish), the `reopened` event.
- A pairing's result = its walkover, or its finished games that aren't voided, in the order they ended.
- Package `tournaments` (the service, I/O) runs the active tournament: it starts it (DYP draw, seeding by rating unless the setup's order is kept), turns the engine's **tournament mode** on (`engine.SetTournament`), collects check-ins (the engine reports a tournament player's coin instead of registering it), stages pairings (`engine.StagePairing`: players, the stage's rules (copied from the chosen preset or custom; older setups without rules: the current preset), with golden goal, sides, first ball; then the normal kickoff countdown), stages the next game of a best-of by itself, finishes the tournament, and pauses it when the table sleeps during a tournament game. A running tournament is restored after a restart.
- The engine's game carries its pairing (`Game.Pairing`: row id + where team A plays), saved with the game.
- SSE topic `tournament` (state changes, results).

### API (M6 task 4)
| Route | Permission |
|---|---|
| `GET /api/tournaments` – the list (running and paused first) | `view.tournaments` |
| `GET /api/tournaments/:id` – the page: settings, participants, teams, stages with pairings and their games, groups, standings, placements, ready pairings in the order of play, the suggestion, the pairing on the table, check-ins, estimate, history, what the account may do. In setup, fixed teams get a preview of the bracket | `view.tournaments` |
| `GET /api/tournaments/options?teams=n[&monster=1]` – the formats with their estimates, sorted by duration (this table's pace once it has 10 games) | `tournaments.create` |
| `POST /api/tournaments`, `PUT /api/tournaments/:id` (setup only) – name, settings, participants | `tournaments.create` / `tournaments.edit` (own/all) |
| `POST …/:id/start`, `…/pause`, `…/continue` | `tournaments.run` (own/all) |
| `POST …/:id/play {pairing}` | `table.control` |
| `PATCH /api/tournaments/:id` – running or paused: name, rules of the stages not started (`rules`, `preset_id`) | `tournaments.edit` (own/all) |
| `POST …/:id/replace {participant, player}`, `POST …/:id/dropout {entrant}` (team number, Monster DYP place) | `tournaments.edit` (own/all) |
| `POST …/:id/abandon`, `…/reinstate`, `…/rebuild`, `DELETE …/:id` (only without games) | `tournaments.manage` |

"Own" = the account created the tournament. SSE topic `tournament` (`{id}`) on every change, for accounts with `view.tournaments`. During a tournament, free-play table actions answer `409 tournament`.

## 14. Milestone tasks (M6, proposed order)
1. `tournament/` core: formats, seeding, byes, pairing order, standings + tie-breaks, duration estimate (+ scenarios).
2. Store: tables, events; `games.pairing_id` wiring.
3. Table integration: tournament mode, registering pairings, results back, pause/sleep/continue.
4. API + permissions (`tournaments.*`), SSE.
5. Web: setup page with the format list, tournament page (bracket, groups, standings, now/next), scoreboard tournament mode, profile additions.
6. Dropouts, replacements, rebuild, frozen finished tournaments (no manual results, §7.4).
7. Load: a 64-team double knockout and a 40-player Monster DYP through `--sim`; e2e test of a small tournament.

## 15. Decided in review (2026-11-25)
1. **Points:** 1 per pairing won; tie-breaks head-to-head, game difference, goal difference, goals scored, Buchholz, seed (§9).
2. **Groups → knockout:** configurable – how many advance per group and crossed or re-seeded knockout (§6.4).
3. **First ball:** higher seed in game 1, then the loser of the previous game; sides swap every game (§7.3).
4. **Tournament mode:** the tournament owns the table while it runs; free play only after pausing it (§8).
5. **No manual results** (changed 2026-11-25, task 6): every pairing is played on the table (§7.4).
6. **Monster DYP:** sitting out gives no points; default rounds so everyone plays ≈ 5 games (§6.6).
7. **Duration estimate:** 22 s per goal and 1 min changeover without data, and the game length follows the stage's target score, lead and time limit (§4).
8. **Starting pairings:** the table doesn't wait for registrations – a check-in by coin/tag of one player per team, or "Play" in the UI, starts a pairing; any ready pairing can be played out of order (§7.2, §8).

9. **Task 6** (2026-11-25): Monster DYP substitutes take over the slot with its points; dropped teams don't qualify and get no place; no dropout while on the table; no undo; finished tournaments are frozen and reopen when one of their games is voided – any game, not only the final (§10, §11).

## 16. Open questions
- None.
