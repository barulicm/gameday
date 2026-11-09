package best

import (
	"context"
	"math"
	"slices"

	"github.com/gizmo-platform/gameday/modules/game"
)

// Robot Performance points (rules §4.2.1) are not a judged score: they
// are derived from a team's final rank in the seeding phase.  Because
// the value is computed from live competition results it is never
// stored as a ScoreType, never appears on the scores page, and is never
// part of the CSV import; it is recomputed on every read.
const (
	performanceKey   = "performance"
	performanceName  = "Robot Performance"
	performanceMax   = 250.0
	performanceFloor = 25.0

	// performanceSpan is the range a ranking is mapped across: rank 1
	// earns performanceMax and the last place earns performanceFloor.
	performanceSpan = performanceMax - performanceFloor
)

// performanceState reports why Robot Performance points are or are not
// available, so the awards page can explain an incomplete BEST Award
// ranking rather than silently omitting teams.
type performanceState string

const (
	// performanceReady means the seeding phase was final and points
	// were derived from it.
	performanceReady performanceState = "ready"
	// performanceNoGameModule means the game module is not part of the
	// module graph, so no phase rankings exist at all.
	performanceNoGameModule performanceState = "noGameModule"
	// performanceNoPhase means no phase carries the Seeding role.
	performanceNoPhase performanceState = "noPhase"
	// performanceNotFrozen means the seeding phase exists but its
	// results are not final yet.
	performanceNotFrozen performanceState = "notFrozen"
)

// performanceTable is the derived Robot Performance points for every
// ranked team, plus enough context for the awards page to explain a
// derivation that could not happen.
type performanceTable struct {
	// ByTeam maps team ID to its Robot Performance points.
	ByTeam map[uint]float64
	// State reports the outcome of the derivation.
	State performanceState
	// Teams is how many teams received points.
	Teams int
	// PhaseName is the seeding phase the points came from, when known.
	PhaseName string
	// SplitByDivision records that a seeding phase which is not itself
	// division aware was ranked per division (see derivePerformance).
	SplitByDivision bool
}

// performanceSource supplies Robot Performance points.  The indirection
// lets the BEST Award scorer be tested without a game module.
type performanceSource interface {
	Points(ctx context.Context) (*performanceTable, error)
}

// performancePoints maps a team's rank in a field of n to points,
// linearly from performanceMax for rank 1 down to performanceFloor for
// rank n (rules §4.2.1: unique points for each level of the ranking).
// A single-team field cannot span a range, so it earns the maximum.
// Results are rounded to whole points and clamped to the range.
func performancePoints(rank, n int) float64 {
	if n <= 0 || rank < 1 {
		return 0
	}
	if n == 1 {
		return performanceMax
	}
	points := performanceMax - performanceSpan*float64(rank-1)/float64(n-1)
	points = math.Round(points)
	if points < performanceFloor {
		return performanceFloor
	}
	if points > performanceMax {
		return performanceMax
	}
	return points
}

// performanceFromSeeding derives Robot Performance points from the
// phase carrying the game module's Seeding role, the way the Game Award
// reads the Final phase: the phase's own score summation and tie breaker
// decide the ranking, and the results count only once it is frozen.
// ByDivision follows the BEST Award's own grouping: points are ranked
// within each division only when the award is given out per division.
type performanceFromSeeding struct {
	gm         GameModule
	byDivision bool
}

// Points implements performanceSource.  When more than one phase carries
// the role, a team's place comes from the first one (phases are returned
// in ID order), so no team is ranked twice.
func (p performanceFromSeeding) Points(ctx context.Context) (*performanceTable, error) {
	if p.gm == nil {
		return &performanceTable{ByTeam: map[uint]float64{}, State: performanceNoGameModule}, nil
	}

	tables, err := p.gm.PhaseRankings(ctx, game.RoleSeeding, true)
	if err != nil {
		return nil, err
	}
	if len(tables) == 0 {
		// Distinguish "no seeding phase" from "seeding phase not final
		// yet" so the awards page can name the actual blocker.
		state := performanceNoPhase
		unfrozen, err := p.gm.PhaseRankings(ctx, game.RoleSeeding, false)
		if err != nil {
			return nil, err
		}
		if len(unfrozen) > 0 {
			state = performanceNotFrozen
		}
		out := &performanceTable{ByTeam: map[uint]float64{}, State: state}
		if len(unfrozen) > 0 {
			out.PhaseName = unfrozen[0].PhaseName
		}
		return out, nil
	}

	return derivePerformance(tables, p.byDivision), nil
}

// derivePerformance turns seeding-phase ranking tables into points.
//
// When byDivision is false, the BEST Award is one overall ranking, so
// every team is one field: rank 1 of the contest earns the maximum and
// the last team the floor.
//
// When byDivision is true, the award is decided within each division, so
// points are too.  A seeding phase that is not itself division aware
// yields a single field-wide table, where a small division's best team
// is ranked behind every team of the field; regrouping the returned rows
// by each team's own division fixes that case and is a no-op for a
// division aware phase, whose rows already belong to one division each.
//
// Ranks are never re-decided here: the division-local rank advances only
// when the phase's rank changes, so a tie in the phase stays a tie and
// earns the same points, and the phase's configured tie breaker is
// preserved.
func derivePerformance(tables []game.PhaseRanking, byDivision bool) *performanceTable {
	out := &performanceTable{ByTeam: map[uint]float64{}, State: performanceReady}

	// Collect all teams, keeping each phase table's ranking order and letting
	// the first phase a team appears in decide its place. Optionally group
	// teams by devision.
	groups := map[string][]game.RankingRow{}
	seen := map[uint]struct{}{}
	for _, table := range tables {
		if out.PhaseName == "" && table.PhaseName != "" {
			out.PhaseName = table.PhaseName
		}
		for _, row := range table.Rows {
			if _, dup := seen[row.TeamID]; dup {
				continue
			}
			seen[row.TeamID] = struct{}{}
			name := ""
			if byDivision {
				name = row.Team.Division.Name
			}
			groups[name] = append(groups[name], row)
		}
	}

	divisions := make([]string, 0, len(groups))
	for name := range groups {
		divisions = append(divisions, name)
	}
	slices.Sort(divisions)

	for _, name := range divisions {
		rows := groups[name]
		n := len(rows)
		rank := 0
		prev := 0
		for i, row := range rows {
			if i == 0 || row.Rank != prev {
				rank++
				prev = row.Rank
			}
			out.ByTeam[row.TeamID] = performancePoints(rank, n)
			out.Teams++
		}
	}

	// A phase that ranked its own divisions already produced per-division
	// tables; only a field-wide phase whose teams belong to more than one
	// division was split here.
	split := byDivision
	for _, table := range tables {
		if table.Division != "" {
			split = false
			break
		}
	}
	out.SplitByDivision = split && len(groups) > 1

	return out
}
