# Authoring Games for Gameday

This guide explains how to write a game configuration for Gameday. A *game* in
Gameday is a single YAML document uploaded on the game setup page. It declares
the field positions, the tournament phases, and the scoring elements. Teams,
divisions, and physical fields are managed separately in the web UI, so a game
author never touches them in the YAML.

The YAML has two top-level keys:

```yaml
Field:
  Positions: ...
Game:
  Phases: ...
  Elements: ...
```

Everything in this guide is grounded in the codebase. The valid option names
listed below are the complete, exhaustive set — anything else will be rejected
when the schedule is generated.

---

## 1. Overview

A Gameday game is a tournament description made of three cooperating parts:

- **Phases** — the sequential stages of the tournament (seeding, semifinals,
  finals, a deathmatch, and so on). Each phase picks a scheduler, a way of
  summing scores, and an optional tie breaker.
- **Advancement filters** — the glue between phases. A filter reads the
  scoreboard of an earlier phase (or the full team roster) and decides which
  teams advance into the current phase.
- **Elements** — the things a team can score during a match (objects placed,
  latches released, connections made). Elements define what the scorekeepers
  record and how each recorded action converts to points.

The upload flow: an operator opens the game setup page, uploads the YAML file,
and the server parses it into its internal model, persisting the phases,
advancement filters, elements, and scorecard definitions. From that point the
tournament runs through the normal schedule, scorekeeping, and advancement
workflow.

Before uploading, you can check a file offline with the linter:

```
gameday debug game lint <file>
```

It parses the file the same way the upload does, compiles every `expr`
expression (`Suppress`, `When`, `SliceExpr`), and verifies that every
referenced advancement filter, `SelectFrom` phase, schedule type, and tie
breaker exists. It exits non-zero and prints each problem if anything is
wrong, which is much easier to act on than an advancement failure at
runtime.

A phase can only be scheduled once the phases its filters read from are
**complete and frozen**. The `Roster` filter is the exception: it reads from
the full team list, so a phase whose only filter is `Roster` is always
schedulable.

---

## 2. Field Positions

```yaml
Field:
  Positions:
    - Name: red
      ID: 1
      Color1: ec5e5e
      Color2: f4a2a2
    - Name: blue
      ID: 2
      Color1: 5b7ab6
      Color2: 9fb1d4
```

Positions are the labeled slots a team occupies within a single field (red,
blue, green, yellow in the examples). The YAML only declares positions; the
**fields themselves and the pinning of divisions to fields are created in the
Fields UI**, not in the game config.

Each position:

| Key | Meaning |
|-----|---------|
| `Name` | Human-readable position label shown in the UI. |
| `ID` | Unique integer position identifier. |
| `Color1` | Primary banding color (light). |
| `Color2` | Secondary banding color (dark). The pair is used for row banding in tables. |

The number of positions determines how many teams play simultaneously per
field: a phase's match block size is `fields × positions`.

**Note for BEST games:** the fixed semifinal and finals rotations assume
**four positions** and lay teams out in the column order
**Yellow / Blue / Red / Green**. If a BEST game declares fewer or different
positions, those rotations will not be produced as intended.

---

## 3. Game Phases

```yaml
Game:
  Phases:
    - Name: seeding
      ID: 1
      ScoreSummation: AverageWithMulligan
      ScheduleType: RandomSeeding
```

A phase is one stage of the tournament. Fields:

| Key | Meaning |
|-----|---------|
| `Name` | Human-readable phase name. |
| `ID` | Unique integer id. Other phases' filters reference this via `SelectFrom`. |
| `Role` | Optional role name declaring the phase's competitive purpose. Registered roles: `Exhibition`, `Seeding`, `Semifinal`, `Final`, plus `Wildcard` when the BEST module is enabled. Unknown non-empty roles are rejected by the linter. Roles let modules (and later tooling) find e.g. "the seeding phase" without hardcoding author-assigned IDs; omitting it simply means no role is assigned. Marking exactly one phase `Seeding` is load-bearing when the BEST module is enabled: its frozen ranking produces every team's Robot Performance points, and without it no team can be ranked for the BEST Award. |
| `ScheduleType` | The scheduler to use. Must be a registered generator name (see §7). |
| `ScoreSummation` | How each team's phase score is aggregated across its matches. One of `AverageWithMulligan` or `Total` (see below). |
| `AdvancementFilters` | Optional list of filters that decide which teams enter this phase (see §5). |
| `DivisionAware` | Optional boolean. When `true`, the phase is scheduled per division and interleaved, using the division-to-field pinning from the Fields UI (see §7, division-aware scheduling). |
| `TieBreaker` | Optional name of a tie breaker to apply when teams are tied on the scoreboard (see §6). |
| `HideScores` | Optional boolean. When `true`, scores for this phase are hidden from display. |
| `Suppress` | Optional boolean [expr-lang/expr](https://expr-lang.org/) **suppression** condition for this phase. Empty (omitted) means the phase is never suppressed. While the expression is `true`, the phase is suppressed: the Generate Schedule button is hidden (the row stays visible) and schedule actions are rejected server-side. See "Conditional phases" below. |
| `SuppressMsg` | Optional message displayed on the phase row while the phase is suppressed (e.g. "too few teams; finals suppressed"). Ignored when `Suppress` is empty or false. |
| `Active` / `Frozen` | Runtime state, set in the UI rather than authored in YAML. At most one phase is `Active`; `Frozen` locks a completed phase so it can be read by advancement filters. |

### Score summation

`ScoreSummation` has exactly two valid values:

| Value | Meaning |
|-------|---------|
| `AverageWithMulligan` | The default. For each team, the **lowest-scoring match is dropped** and the remaining matches are averaged. Equivalent to `(sum − min) / (count − 1)`; a team with a single match falls back to that match's average. |
| `Total` | The sum of all of the team's match scores. |

Which one you pick changes the scoreboard rankings that advancement filters
read. `AverageWithMulligan` is forgiving of a single bad match; `Total` rewards
consistency over more matches.

### When a phase becomes schedulable

A phase can generate its schedule when **both** of these hold:

1. **Every phase its advancement filters read from is complete and
   frozen.** A filter with `SelectFrom: 0` (the full roster) imposes no
   such requirement.
2. **Every filter's data is actually present.** A filter can declare that
   it cannot answer yet — for example the BEST `BESTNotebook` filter is
   not ready until at least one team has a recorded notebook score,
   because until then it would silently reject everyone. A filter whose
   data is loaded but which happens to select no team *is* ready; use
   `Suppress` (below) for phases that should not run at all for a
   roster-shape reason.

Neither requirement applies to a filter whose `When` is `false` (see
"Conditional filters" in §5): a skipped filter never blocks. A phase with
no advancement filters inherits the full roster and is always schedulable.

On the phase list, **only the next phase up shows a Generate Schedule
control** (phases are ordered by their `SelectFrom` dependencies). When
the next phase's generation parameters are unmet, the button is grayed
out and carries the reason — as a hover tooltip and as help text under
the button, e.g. `Pick: no notebook scores have been recorded yet` or
`Pick: Seeding must be complete and frozen first`. All unmet parameters
are listed at once. Phases waiting behind the next one up show no button.

The gate is enforced server-side on the select/preview/accept actions,
but an operator can override it: the blocked page links to **Generate
manually anyway**, which appends `?override=1` and unlocks the manual
team picker. The override travels with the operator through the preview
and accept steps, so a hand-built schedule is possible whenever the
automatic selection cannot run.

### Conditional phases (`Suppress`)

The `Suppress` field adds an operator-authored **suppression** condition on top
of the completeness rule above. It is an
[expr-lang/expr](https://expr-lang.org/) expression that must evaluate to a
boolean. An empty or omitted `Suppress` never suppresses.

```yaml
    - Name: finals
      ID: 3
      ScoreSummation: Total
      ScheduleType: OneShot
      Suppress: len(Roster) < 4
      SuppressMsg: too few teams; finals suppressed
```

The expression is evaluated against a context with four top-level variables:

| Variable | Value |
|----------|-------|
| `Phases` | The phase list in authored order, **zero-indexed** (`Phases[0]` is the first phase). Each entry exposes `ID`, `Name`, `Active`, `Frozen`, and `Complete` (true when the phase has at least one placement and every placement is in a terminal state: `Complete`, `NoShow`, or `Disqualified`). |
| `Roster` | The full team roster, a map keyed by team ID. Note the expr interpreter cannot index a map with an integer literal (`Roster[1]` fails) or pass a map to `count()`; use `len(Roster)` for the roster size instead. |
| `Scoreboard` | The ranked scoreboard rows, in rank order. Sourced from the phase named by this phase's **first advancement filter's `SelectFrom`**; if there is no filter (or it reads the full roster, `SelectFrom: 0`), the **active phase's** scoreboard is used, and if no phase is active the list is empty. Each row exposes `Team` (a team record with `Name`, `Number`, ...), `TeamID`, `Rank`, `Average`, `Mulligan`, `Total`, `Score`, `Max`, `Min`, and `Count`. |
| `Division` | The name of the division being evaluated; the empty string for the whole field. |

Behavior:

- While `Suppress` is `true`, the phase is **suppressed**: the **Generate
  Schedule** button is hidden on the phase list (the phase row itself stays
  visible) and the select/preview/accept schedule actions are rejected
  server-side, so the suppression cannot be bypassed by posting directly.
- `SuppressMsg`, when present, is shown as help text on the phase row **while the
  phase is suppressed** — use it to explain why the phase is off.
- A **division-aware** phase is evaluated once per configured division name,
  and is suppressed when the condition holds for **any** configured division.
  For example, `Suppress: Division != "Open"` on a division-aware phase is never
  suppressed while "Open" is the only division, but is suppressed once any
  other division exists, because the condition passes for at least one of
  them.
- The expression must compile and evaluate to a boolean; a syntax error, a
  runtime error, or a non-boolean result is treated as an error and reported
  in the server logs (the phase is treated as suppressed and the action
  fails).

Examples:

```yaml
Suppress: len(Roster) < 4                       # suppress: too few teams
Suppress: len(Roster) < 8                       # suppress: too few for an 8-team phase
Suppress: Phases[0].Frozen                      # suppress once the first phase is frozen
Suppress: Phases[0].Complete and Phases[1].Complete
Suppress: Scoreboard[0].Rank == 1               # suppress once the leaderboard has settled
Suppress: Scoreboard[0].Team.Name == "Team 3"
Suppress: Division == "Open"                    # suppress for that division's evaluation
Suppress: true                                  # always suppressed (the phase never runs)
```

---

## 4. Game Elements

Elements describe what the scorekeepers record during a match and how it turns
into points. At setup, each element/state combination becomes a scorecard
entry the scorekeeper clicks.

```yaml
Game:
  Elements:
    - ID: 1
      EID: widgets
      Name: Widgets Fabricated
      Desc: Scores as count completed.
      Type: count
      States:
        - ID: 1
          SID: fabricated
          Name: Fabricated
          Desc: Fabrication Completed.
          Each: 10
          Max: 10
```

### Element

| Key | Meaning |
|-----|---------|
| `ID` | Unique integer element id. |
| `EID` | Stable slug identifier (lowercase, no spaces) used to key the element. |
| `Name` | Display name on the scorecard. |
| `Desc` | Optional description shown to scorekeepers. |
| `Type` | One of `count`, `boolean`, or `radio`. Determines how states score. |
| `States` | List of the element's states. |

### State

| Key | Meaning |
|-----|---------|
| `ID` | Unique integer state id. |
| `SID` | Stable slug identifier for the state. |
| `Name` | Display name on the scorecard. |
| `Desc` | Optional description. |
| `Each` | Points awarded per occurrence (for `count` and `boolean`). |
| `Max` | Optional cap on the number of occurrences (for `count`). |
| `Values` | For `radio` only: the list of mutually exclusive choices (see below). |

### Element types

The `Type` field is matched case-insensitively and has exactly three values:

**`count`** — the scorekeeper records how many times something happened. The
state contributes `count × Each` points. An optional `Max` caps the count, so
a state with `Each: 20` and `Max: 4` scores at most 80 points.

```yaml
- ID: 1
  EID: widgets
  Name: Widgets Fabricated
  Type: count
  States:
    - ID: 1
      SID: fabricated
      Name: Fabricated
      Each: 10
      Max: 10
```

**`boolean`** — the scorekeeper flags whether something happened. If flagged,
the state contributes its `Each` points; if not, zero. Booleans have no count
and no `Max`.

```yaml
- ID: 2
  EID: loc_factoids
  Name: LOC Factoids
  Type: boolean
  States:
    - ID: 4
      SID: white
      Name: White LOC Factoid
      Each: 100
    - ID: 5
      SID: gold
      Name: Golden LOC Factoid
      Each: 100
```

**`radio`** — the scorekeeper picks exactly one of several mutually exclusive
values. Each value carries its own `Points`, and exactly one value should be
marked `Default: true` so the scorecard starts there.

```yaml
- ID: 6
  EID: wsd_gate_latch
  Name: WSD Gate Latch
  Type: radio
  States:
    - ID: 11
      SID: released
      Name: Latches Released
      Values:
        - ID: 1
          VID: none
          Name: None Released
          Points: 0
          Default: true
        - ID: 2
          VID: one_released
          Name: 1 Released
          Points: 100
```

The selected value's `Points` is what the state contributes. A `radio` state
has no `Each` or `Max`.

A complete worked set of elements (including all three types) is in
`test_data/4p_with_objectives.yml`.

---

## 5. Advancement Filters

Advancement filters chain phases together. Each filter reads a scoreboard and
adds or removes teams from the pool that advances into the phase the filter is
attached to.

```yaml
Game:
  Phases:
    - Name: deathmatch
      ID: 2
      ScoreSummation: Total
      ScheduleType: RandomSeeding
      AdvancementFilters:
        - Rule: PickTop4
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 1
          SliceExpr: 4
```

### Filter fields

| Key | Meaning |
|-----|---------|
| `Filter` | The registered filter name. One of `Roster`, `ScoreboardRanking`, or `BESTNotebook`. |
| `Mode` | `include` or `exclude`. `include` keeps the rows the filter selects; `exclude` drops them. |
| `SelectFrom` | The `ID` of the source phase whose scoreboard feeds this filter. `0` means the full team roster (no source phase). |
| `SliceExpr` | An expression, evaluated to an **integer**, that sets the cutoff. Must evaluate to an int or the filter fails. |
| `When` | Optional boolean [expr-lang/expr](https://expr-lang.org/) expression gating whether this filter runs at all. Empty (omitted) means always applied. See "Conditional filters" below. |
| `Rule` | A **human-readable label only** (e.g. `PickAll`, `PickTop4`). It is echoed into per-team advancement determinations for humans but has **no effect on the logic**. Do not encode behavior in it. |

### How multiple filters combine

When a phase has several filters, they run **in order** against one shared
candidate pool. An `include` filter **adds** the teams it selects; an `exclude`
filter **removes** the teams it selects. Teams an `exclude` filter removes are
blocked for the rest of the phase, so a later `include` filter cannot
re-advance them. This is what makes an "exclude, then include from what
remains" chain work: for example, dropping the top seeders from the previous
phase's scoreboard and then taking the top remaining teams by notebook

```yaml
AdvancementFilters:
  - Rule: DropTopSeeders
    Filter: ScoreboardRanking
    Mode: exclude
    SelectFrom: 1
    SliceExpr: 7
  - Rule: TopRemainingNotebooks
    Filter: BESTNotebook
    Mode: include
    SelectFrom: 1
    SliceExpr: 4
```

advances the top 4 notebooks **among the teams that were not in the top 7
seeders**. The advancing set for the phase is whatever is left in the pool
after the last filter runs, so a phase whose filters are all `include`
advances the union of everything selected, while a lone `exclude` filter
advances nothing.

### The three valid filters

**`Roster`** — operates on the full team roster. `include` adds every team;
`exclude` removes every team. It ignores `SliceExpr`. A phase with a single
`Roster` include filter (or no filters at all) starts from the whole field of
teams, so it is always schedulable.

```yaml
- Rule: PickAll
  Filter: Roster
  Mode: include
  SelectFrom: 0
```

**`ScoreboardRanking`** — reads the source phase's scoreboard and selects the
top N rows **by position/index**. `SliceExpr` is evaluated to an integer
cutoff; rows at indices `0 .. cutoff−1` (the top N rows) are selected. This is
**index-based**, not rank-value-based: it takes the first N rows regardless of
whether their displayed ranks are contiguous. An empty source scoreboard
selects zero teams.

```yaml
- Rule: PickTop4
  Filter: ScoreboardRanking
  Mode: include
  SelectFrom: 1
  SliceExpr: 4
```

**`BESTNotebook`** — BEST-module only. Selects teams **by rank value**: a team
is included if its rank is `≤ cutoff` (the cutoff from `SliceExpr`). Unlike
`ScoreboardRanking`, this is **rank-value-based**. It operates on the teams in
the source scoreboard and **falls back to the full roster** if the scoreboard
is empty. It requires the BEST module and its recorded notebook scores; a team
with no notebook score is rejected in `include` mode and left untouched in
`exclude` mode.

```yaml
- Rule: TopNotebooks
  Filter: BESTNotebook
  Mode: include
  SelectFrom: 2
  SliceExpr: 4
```

> **Careful:** `ScoreboardRanking` and `BESTNotebook` use different cutoff
> semantics. `ScoreboardRanking` takes the first N *rows*; `BESTNotebook` takes
> all rows whose *rank* is ≤ N. They coincide only when there are no ties.

`BESTNotebook` is only available when the **BEST module** is loaded (see §8).

### Conditional filters (`When`)

The `When` field adds an operator-authored condition to a single filter, on
top of the phase-level condition from §3. It is an
[expr-lang/expr](https://expr-lang.org/) expression that must evaluate to a
boolean. An empty or omitted `When` is always satisfied. There is no `SuppressMsg`
on filters; filters either run or they do not.

```yaml
    - Name: finals
      ID: 3
      AdvancementFilters:
        - Rule: PickTop4
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 2
          SliceExpr: 4
          When: Phases[1].Frozen
```

The expression is evaluated against the **same context** as a phase's `Suppress`
(see §3): `Phases`, `Roster`, `Scoreboard`, and `Division`, with the same
expr limitations. One difference: a filter's `Scoreboard` is the scoreboard of
**its own `SelectFrom`** phase (not the phase's first filter), so
`Scoreboard[0].Rank` refers to the top row of the source this filter reads.
A `SelectFrom` of `0` sees an empty scoreboard.

Behavior:

- While `When` is `false`, the filter is skipped: it selects **no teams**,
  contributes nothing to the advancing pool (neither adds nor removes), and
  produces no advancement determinations. The other filters on the phase run
  normally, so a phase whose filters are all skipped still resolves to
  whatever its non-conditional filters produced.
- The expression must compile and evaluate to a boolean; a syntax error, a
  runtime error, or a non-boolean result is treated as an error and reported
  in the server logs, and the team-selection action fails.
- A filter's `When` **does** affect whether the phase is schedulable: only
  filters whose `When` is `true` require their source phase to be complete and
  frozen before the Generate Schedule button is shown (empty or omitted counts
  as `true`; for a division-aware phase, `true` for at least one configured
  division suffices). A filter whose `When` is `false` imposes no such
  requirement, which is how a phase can route around a suppressed
  intermediate phase (see below).

Examples:

```yaml
When: Phases[1].Frozen                       # only once the source phase is frozen
When: Phases[0].Complete                     # once the source phase is complete
When: len(Roster) == 12                      # only when exactly 12 teams are in
When: Scoreboard[0].Rank == 1                # the source scoreboard has settled
When: Division == "Open"                     # this division's evaluation only
When: true                                   # always (equivalent to omitting)
```

#### Routing around a suppressed phase

The two `When`/`Suppress` polarities are meant to be used in concert: the
phase-level `Suppress` suppresses a phase that cannot run, and complementary
filter-level `When`s let a downstream phase pick up a different source in its
place.
Suppose seeding (ID 1) feeds the semifinals (ID 2), which feed the finals
(ID 3), and the semifinals generator needs 8 or 16 teams:

```yaml
    - Name: semifinal
      ID: 2
      Suppress: len(Roster) < 8      # suppress: too few teams for semifinals
      SuppressMsg: too few teams; semifinals suppressed
      # ...
    - Name: finals
      ID: 3
      AdvancementFilters:
        - Rule: Top4FromSemifinals
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 2                # the normal path, from the semifinals
          SliceExpr: 4
          When: len(Roster) >= 8       # runs only when the semifinals run
        - Rule: Top4FromSeeding
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 1                # the fallback, straight from seeding
          SliceExpr: 4
          When: len(Roster) < 8        # runs only when the semifinals are suppressed
```

The two filters are complementary: exactly one `When` is true at any time, so
exactly one of them runs. Because the schedulability check skips filters whose
`When` is false, the finals stay schedulable with 4 teams: the `SelectFrom: 2`
filter is ignored instead of demanding that the semifinals phase (suppressed,
and therefore never complete or frozen) be ready.

---

## 6. Tie Breakers

When two or more teams are tied on the phase scoreboard, a phase can declare a
tie breaker to order the tied group deterministically.

```yaml
- Name: finals
  ID: 3
  ScoreSummation: Total
  ScheduleType: BESTFinals
  TieBreaker: BESTUnifiedTieBreaker
```

The `TieBreaker` field holds a **string name** of a registered tie breaker. If
the name is not registered, the system logs a warning and leaves the ties
unbroken (tied teams keep their shared rank).

### The only valid value

The **only registered tie breaker is `BESTUnifiedTieBreaker`**, provided by the
BEST module; the name only resolves while the module is loaded, so a game file
naming it at an event without the BEST module reports as an unknown tie breaker
when linted. It orders a tied group by:

1. Notebook score, highest first
2. Marketing score, highest first
3. Poster score, highest first
4. Video score, highest first
5. Team number, lowest first

The match count passed in is ignored. If the required BEST scores cannot be
read, the tie breaker falls back to leaving the input order unchanged.

Because it depends on BEST notebook/marketing/poster/video scores,
`BESTUnifiedTieBreaker` is only useful in a tournament running the BEST
module. If you omit `TieBreaker` (or leave it empty), tied teams keep their
shared rank.

When a phase is `DivisionAware`, a tied group is first split by division name
and only sub-groups of two or more teams are reordered, so teams in different
divisions are never compared against each other.

Once a tie is broken, every team behind the reordered group shifts down by the
number of places the group expanded, so ranks stay strictly ordered by score:
a team with fewer points can never end up ranked above one with more, and a
team left tied with another (only possible across divisions of a `DivisionAware`
phase) shares a rank rather than colliding with an unrelated team.

### Robot Performance points

When the BEST module is enabled it derives each team's **Robot Performance**
points (game rules §4.2.1) from the phase tagged `Role: Seeding`, and feeds them
to the BEST Award as its fifth component. The points are not authored here and
are never entered in the BEST scores UI: they are recomputed from the seeding
phase's own `ScoreSummation` and `TieBreaker` on every read, mapped linearly from
250 points for 1st place down to 25 for last, and only count once the phase is
frozen. Points are awarded per division (rules §9), so a seeding phase that is
not `DivisionAware` is still split by each team's division. A contest with no
`Seeding` phase therefore yields no Robot Performance points, and because all
five components are required, no BEST Award ranking either; the awards page says
so explicitly.

---

## 7. Schedulers (Generators)

The `ScheduleType` field names the scheduler that produces the matches for a
phase. Gameday knows exactly **six** registered generators. Anything else
produces an `UnknownGenerator` error.

| Name | Rounds (max / default) | Dynamic | Constraints and notes |
|------|------------------------|---------|------------------------|
| `Annealed` | 25 / 5 | no | The quality-first scheduler, and the recommended default for any phase that would otherwise use `RandomSeeding`. Runs a fixed-shape local search that strongly optimizes replay spacing, distinct opponents, field/position diversity, and low variance across teams — measurably better than `RandomSeeding` (e.g. at 40 teams / 2 fields / 4 positions / 6 rounds: 18 distinct opponents vs 13, and replay gaps kept at or above one round of spacing). Prefers a pre-baked design from the embedded archive when the shape (teams × fields × positions, round cap) is covered; otherwise it searches live, which still returns a valid schedule but with somewhat lower quality. |
| `RandomSeeding` | 10 / 7 | no | Produces exactly the configured number of rounds. Each round is a random assignment of teams into `fields × positions` slots, then refined to maximize worst-case team downtime and position diversity. If teams don't divide evenly, a leftover partial match is committed. `Annealed` dominates it on every quality metric and should be preferred unless `RandomSeeding` is specifically wanted. |
| `OneShot` | 1 / 1 | no | A single random round. If teams don't divide evenly, a leftover partial match is committed. |
| `BID` | 100 / 1 | yes | Balanced Incomplete Block Design with block size `k = fields × positions` and λ = 1 (every pair of teams plays together exactly once). Requires `teams > k`; otherwise it falls back to a single random round. The configured `Rounds` is treated as a **minimum** number of appearances per team, with balanced extra blocks appended; the final round count is computed dynamically. |
| `BESTSemifinal` | 3 / 3 | no | BEST-module only. A fixed rotation for **exactly 8 or 16 teams** (6 or 12 matches respectively). Teams are seeded 1-based in the column order Yellow/Blue/Red/Green. With multiple fields, consecutive matches are grouped into blocks of `fields` per field. |
| `BESTFinals` | 4 / 4 | no | BEST-module only. A fixed finals rotation of 4 matches, keyed off 1-based semifinal rank, in column order Yellow/Blue/Red/Green. `Generate` errors unless the phase has **exactly 4 teams, 1 field, and at least 4 positions**. The winner is decided by total points from the score layer, not by the generator. |

`BESTSemifinal` and `BESTFinals` come from the BEST module and are registered only when
that module is loaded.

### Division-aware scheduling

Setting `DivisionAware: true` on a phase routes it through division-aware
scheduling instead of a single pooled schedule. Gameday:

1. Generates an independent schedule for each division.
2. Pins each division to its assigned field(s) — the field-to-division
   assignment comes from the **Fields UI**, not the YAML.
3. Interleaves the per-division schedules round by round, remapping each
   division's local field indices to its pinned global fields and tagging every
   match with its division.
4. Optionally compacts matches that use non-overlapping fields, unless the
   division is marked `NoCompact`.

Because the YAML only declares positions, the number of fields and which
division occupies which field must be set up in the Fields UI before a
division-aware phase can be scheduled.

### Choosing a scheduler

- **Seeding / early rounds:** `Annealed` for a fixed number of rounds — it is
  strictly better than `RandomSeeding` at opponent diversity, replay spacing,
  and position/field variety, with lower variance across teams — or
  `BID` when you want every pair of teams to meet exactly once.
- **Single exhibition round:** `OneShot`.
- **BEST semifinals:** `BESTSemifinal` (8 or 16 teams).
- **BEST finals:** `BESTFinals` (exactly 4 teams, 1 field, ≥4 positions).

---

## 8. Gotchas

1. **Every advancement filter needs a registered `Filter` name.** Each entry
   under `AdvancementFilters` is looked up in the filter registry by name. A
   missing `Filter` field, or one with a name that isn't registered, fails the
   load with `advancement filter %q is not registered`. Only use the names in
   §5.

2. **BEST-only options require the BEST module.** `BESTSemifinal`, `BESTFinals`, the
   `BESTNotebook` filter, and the `BESTUnifiedTieBreaker` tie breaker all come
   from the BEST module and are only registered when that module is loaded. A
   game using any of them without the BEST module will fail to resolve the
   name.

3. **Fixed-rotation constraints are strict.** `BESTSemifinal` accepts exactly 8 or
   16 teams. `BESTFinals` errors unless the phase has exactly 4 teams, 1 field,
   and at least 4 positions. `BID` needs `teams > fields × positions`; if not,
   it silently falls back to a single random round.

4. **`OneShot` is 1/1, not 10/1.** The registered configuration for `OneShot`
   is max 1 / default 1 round. (A pair of stray methods in the source returning
   10 are not the registered config and are not what the UI or scheduler uses.)

5. **`SliceExpr` must evaluate to an integer.** If the expression does not
   produce an int, applying the filter fails. And `Mode` must be exactly
   `include` or `exclude`.

6. **Phase ordering is enforced by completion, not by `ID`.** A phase cannot be
   scheduled until every source phase its filters read from is complete **and**
   frozen. A `Roster`-sourced filter (`SelectFrom: 0`) is always satisfied.

7. **`Rule` is a label, not logic.** The `Rule` field on a filter is a
   human-readable label echoed into per-team determinations. It does not
   influence which teams advance. Don't put behavior in it.

8. **The two ranking filters differ in cutoff semantics.** `ScoreboardRanking`
   is **index-based** — it takes the top N rows of the scoreboard.
   `BESTNotebook` is **rank-value-based** — it takes all teams whose rank is ≤
   N. With ties present, they select different sets.

---

## 9. Complete Annotated Example

This is a corrected, annotated four-position BEST-style game. Note that the
finals phase uses `BESTFinals` (the registered name), not `Final`.

```yaml
---
Field:
  # Field positions. The fields themselves and division-to-field pinning are
  # configured in the Fields UI, not here.
  Positions:
    - Name: red
      ID: 1
      Color1: ec5e5e   # light banding color
      Color2: f4a2a2   # dark banding color
    - Name: blue
      ID: 2
      Color1: 5b7ab6
      Color2: 9fb1d4
    - Name: green
      ID: 3
      Color1: 8de98d
      Color2: bef3be
    - Name: yellow
      ID: 4
      Color1: f5f551
      Color2: fcfcd3

Game:
  Phases:
    # Phase 1: seeding. Every team enters (Roster, SelectFrom 0). Random
    # schedule; score is the mulligan average across matches.
    - Name: seeding
      ID: 1
      ScoreSummation: AverageWithMulligan
      ScheduleType: RandomSeeding
      AdvancementFilters:
        - Rule: PickAll            # label only; no logic
          Filter: Roster           # add the entire roster
          Mode: include
          SelectFrom: 0            # 0 = full roster, no source phase

    # Phase 2: semifinals. Top teams from seeding advance. Division-aware, so
    # it uses the division-to-field pinning from the Fields UI. The filter's
    # When keeps it from picking anyone until seeding is frozen.
    - Name: semifinal
      ID: 2
      ScoreSummation: Total
      DivisionAware: true
      ScheduleType: BESTSemifinal   # BEST rotation; 8 or 16 teams
      AdvancementFilters:
        - Rule: PickTop8           # label only
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 1            # read the seeding scoreboard (ID 1)
          SliceExpr: 8             # top 8 rows (index-based)
          When: Phases[0].Frozen

    # Phase 3: finals. Top 4 from the semifinals. Exactly 4 teams, 1 field,
    # >= 4 positions. Ties broken by the BEST unified tie breaker.
    # The Suppress condition suppresses the finals while fewer than 4 teams are
    # in; SuppressMsg explains that on the phase row while it holds.
    - Name: finals
      ID: 3
      ScoreSummation: Total
      DivisionAware: true
      ScheduleType: BESTFinals     # the only valid finals name
      TieBreaker: BESTUnifiedTieBreaker
      Suppress: len(Roster) < 4
      SuppressMsg: too few teams; finals suppressed
      AdvancementFilters:
        - Rule: PickTop4           # label only
          Filter: ScoreboardRanking
          Mode: include
          SelectFrom: 2            # read the semifinal scoreboard (ID 2)
          SliceExpr: 4             # top 4 rows (index-based)

  Elements:
    # A count element: two states, each capped by Max.
    - ID: 1
      EID: factoids
      Name: Net True Factoids
      Desc: Scores as net-true.
      Type: count
      States:
        - ID: 1
          SID: collected
          Name: Collected
          Desc: Resting on the robot.
          Each: 10
        - ID: 2
          SID: sorted
          Name: Sorted
          Desc: Inside the True sorting tray.
          Each: 5
        - ID: 3
          SID: installed
          Name: Installed
          Desc: Inside a Neural Net Node.
          Each: 15

    # A boolean element: each state is an all-or-nothing flag.
    - ID: 2
      EID: loc_factoids
      Name: LOC Factoids
      Type: boolean
      States:
        - ID: 4
          SID: white
          Name: White LOC Factoid
          Desc: Any legal scoring position.
          Each: 100
        - ID: 5
          SID: gold
          Name: Golden LOC Factoid
          Desc: Any legal scoring position.
          Each: 100

    # A count element with per-state caps.
    - ID: 3
      EID: net_connections
      Name: Network Connections
      Type: count
      States:
        - ID: 6
          SID: 2_node
          Name: 2-Node Connections
          Each: 20
          Max: 4
        - ID: 7
          SID: 3_node
          Name: 3-Node Connections
          Each: 60
          Max: 2

    # A radio element: exactly one value chosen, one marked Default.
    - ID: 4
      EID: wsd_gate_latch
      Name: WSD Gate Latch
      Type: radio
      States:
        - ID: 8
          SID: released
          Name: Latches Released
          Values:
            - ID: 1
              VID: none
              Name: None Released
              Points: 0
              Default: true        # scorecard starts here
            - ID: 2
              VID: one_released
              Name: 1 Released
              Points: 100
            - ID: 3
              VID: two_released
              Name: 2 Released
              Points: 130
            - ID: 4
              VID: three_released
              Name: 3 Released
              Points: 140
            - ID: 5
              VID: four_released
              Name: 4 Released
              Points: 145
```

To build your own game, copy the shapes above: declare the positions your
fields have, define phases that chain via advancement filters, pick a
registered `ScheduleType` for each, and describe the scorecard with `count`,
`boolean`, and `radio` elements.
