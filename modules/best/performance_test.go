package best

import (
	"context"
	"errors"
	"testing"

	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// seedSeedingPhase creates a frozen phase carrying the Seeding role and
// records one completed match per team, scored so that teams appear in
// the order given (first = highest score).  The phase uses the average
// with mulligan summation, the way a real seeding phase is authored.
func seedSeedingPhase(t *testing.T, d *db.DB, teams []team.Team, divisionAware bool) game.GamePhase {
	t.Helper()

	phase := game.GamePhase{
		Name:           "Seeding",
		Role:           game.RoleSeeding,
		ScoreSummation: "AverageWithMulligan",
		TieBreaker:     TieBreakerBESTUnified,
		Frozen:         true,
		DivisionAware:  divisionAware,
	}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create seeding phase: %v", err)
	}

	for i, tm := range teams {
		mp := game.MatchPlacement{Match: i + 1, PhaseID: phase.ID, TeamID: tm.ID, FieldID: 1, State: game.MatchStateComplete}
		if err := d.Create(&mp).Error; err != nil {
			t.Fatalf("create placement: %v", err)
		}
		ms := game.MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: phase.ID, TeamID: tm.ID, MatchPlacementID: mp.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create score: %v", err)
		}
	}
	return phase
}

func TestPerformancePointsMapping(t *testing.T) {
	cases := []struct {
		rank, n int
		want    float64
	}{
		{1, 5, 250},
		{2, 5, 194},
		{3, 5, 138},
		{4, 5, 81},
		{5, 5, 25},
		{1, 2, 250},
		{2, 2, 25},
		{1, 1, 250},
		{0, 5, 0},
		{6, 5, 25},
		{1, 0, 0},
	}

	for _, tc := range cases {
		if got := performancePoints(tc.rank, tc.n); got != tc.want {
			t.Errorf("performancePoints(%d, %d) = %v, want %v", tc.rank, tc.n, got, tc.want)
		}
	}
}

// performanceRankingTable builds one ranking table of rankedTeam rows
// for the derivePerformance tests.
func performanceRankingTable(division string, rows ...game.RankingRow) game.PhaseRanking {
	return game.PhaseRanking{PhaseName: "Seeding", Role: game.RoleSeeding, Division: division, Rows: rows}
}

func rankingRow(number int, name string, division string, rank int) game.RankingRow {
	return game.RankingRow{
		TeamID: uint(number),
		Team: team.Team{
			ID:       uint(number),
			Name:     name,
			Number:   number,
			Division: team.Division{Name: division},
		},
		Rank: rank,
	}
}

func TestDerivePerformanceFlatField(t *testing.T) {
	table := performanceRankingTable("",
		rankingRow(101, "Alpha", "Open", 1),
		rankingRow(102, "Beta", "Open", 2),
		rankingRow(103, "Gamma", "Open", 3),
	)

	result := derivePerformance([]game.PhaseRanking{table}, true)
	if result.State != performanceReady {
		t.Fatalf("state = %q, want %q", result.State, performanceReady)
	}
	if result.SplitByDivision {
		t.Errorf("single-division field should not be reported as split")
	}
	if result.PhaseName != "Seeding" {
		t.Errorf("phase name = %q, want Seeding", result.PhaseName)
	}
	if result.Teams != 3 {
		t.Errorf("teams = %d, want 3", result.Teams)
	}

	wantByNumber := map[int]float64{101: 250, 102: 138, 103: 25}
	points := pointsByTeamNumber(result, table.Rows)
	for number, want := range wantByNumber {
		if points[number] != want {
			t.Errorf("team %d points = %v, want %v", number, points[number], want)
		}
	}
}

// pointsByTeamNumber re-keys a derivation's points by team number so
// synthetic rows with zero IDs can be asserted on.
func pointsByTeamNumber(result *performanceTable, rows []game.RankingRow) map[int]float64 {
	out := make(map[int]float64, len(rows))
	for _, row := range rows {
		out[row.Team.Number] = result.ByTeam[row.TeamID]
	}
	return out
}

// TestDerivePerformanceFlatSeedingSplitsByDivision covers a BEST Award
// given out per division: a seeding phase that is not division aware
// yields one field-wide ranking, which is regrouped so each division's
// top team earns the maximum.
func TestDerivePerformanceFlatSeedingSplitsByDivision(t *testing.T) {
	table := performanceRankingTable("",
		rankingRow(101, "Alpha", "Open", 1),
		rankingRow(102, "Beta", "Open", 2),
		rankingRow(103, "Gamma", "Open", 3),
		rankingRow(201, "Delta", "Rookie", 4),
		rankingRow(202, "Epsilon", "Rookie", 5),
	)

	result := derivePerformance([]game.PhaseRanking{table}, true)
	if !result.SplitByDivision {
		t.Fatalf("a flat multi-division phase should be reported as split")
	}

	// Each division is scored against its own size: Open has three
	// teams, Rookie has two, so Rookie's top team earns the maximum.
	points := pointsByTeamNumber(result, table.Rows)
	want := map[int]float64{101: 250, 102: 138, 103: 25, 201: 250, 202: 25}
	for number, expected := range want {
		if points[number] != expected {
			t.Errorf("team %d points = %v, want %v", number, points[number], expected)
		}
	}
}

// TestDerivePerformancePreservesTies checks that a tie in the phase
// stays a tie: the division-local rank advances only when the phase's
// rank changes, so tied teams earn identical points.
func TestDerivePerformancePreservesTies(t *testing.T) {
	table := performanceRankingTable("",
		rankingRow(101, "Alpha", "Open", 1),
		rankingRow(102, "Beta", "Open", 1),
		rankingRow(103, "Gamma", "Open", 3),
	)

	result := derivePerformance([]game.PhaseRanking{table}, true)
	points := pointsByTeamNumber(result, table.Rows)
	if points[101] != points[102] {
		t.Errorf("tied teams earned different points: %v vs %v", points[101], points[102])
	}
	if points[101] != 250 {
		t.Errorf("tied leaders points = %v, want 250", points[101])
	}
	// The tied pair holds the top level, so the third team is second of
	// three levels rather than last of three places.
	if points[103] != 138 {
		t.Errorf("trailing team points = %v, want 138", points[103])
	}
}

// TestDerivePerformanceDivisionAwareUnchanged checks that a phase that
// already ranked its own divisions is unaffected: the rows are already
// per division, so the local ranks match the phase's ranks.
func TestDerivePerformanceDivisionAwareUnchanged(t *testing.T) {
	tables := []game.PhaseRanking{
		performanceRankingTable("Open",
			rankingRow(101, "Alpha", "Open", 1),
			rankingRow(102, "Beta", "Open", 2),
		),
		performanceRankingTable("Rookie",
			rankingRow(201, "Delta", "Rookie", 1),
			rankingRow(202, "Epsilon", "Rookie", 2),
		),
	}

	result := derivePerformance(tables, true)
	if result.SplitByDivision {
		t.Errorf("a division aware phase should not be reported as split")
	}

	points := pointsByTeamNumber(result, append(append([]game.RankingRow{}, tables[0].Rows...), tables[1].Rows...))
	want := map[int]float64{101: 250, 102: 25, 201: 250, 202: 25}
	for number, expected := range want {
		if points[number] != expected {
			t.Errorf("team %d points = %v, want %v", number, points[number], expected)
		}
	}
}

// TestDerivePerformanceNoDivisionTeams checks that teams without a
// division all land in one bucket together, so they still earn points.
func TestDerivePerformanceNoDivisionTeams(t *testing.T) {
	table := performanceRankingTable("",
		rankingRow(101, "Alpha", "", 1),
		rankingRow(102, "Beta", "", 2),
	)

	result := derivePerformance([]game.PhaseRanking{table}, true)
	if result.SplitByDivision {
		t.Errorf("undivided teams should not be reported as a split")
	}
	points := pointsByTeamNumber(result, table.Rows)
	if points[101] != 250 || points[102] != 25 {
		t.Errorf("points = %v, want 250 and 25", points)
	}
}

// stubPerformanceSource is a GameModule stand-in for the tests that need
// a specific derivation outcome without a database.
type stubPerformanceSource struct {
	tables []game.PhaseRanking
	err    error
}

func (s stubPerformanceSource) DivisionAware() bool { return false }
func (s stubPerformanceSource) PhaseRankings(_ context.Context, role game.PhaseRole, frozenOnly bool) ([]game.PhaseRanking, error) {
	if s.err != nil {
		return nil, s.err
	}
	if frozenOnly {
		return s.tables, nil
	}
	return s.tables, nil
}

func TestPerformanceFromSeedingWithoutGameModule(t *testing.T) {
	source := performanceFromSeeding{}
	table, err := source.Points(context.Background())
	if err != nil {
		t.Fatalf("Points: %v", err)
	}
	if table.State != performanceNoGameModule {
		t.Errorf("state = %q, want %q", table.State, performanceNoGameModule)
	}
	if len(table.ByTeam) != 0 {
		t.Errorf("expected no points, got %v", table.ByTeam)
	}
}

func TestPerformanceFromSeedingPropagatesError(t *testing.T) {
	sentinel := errors.New("boom")
	source := performanceFromSeeding{gm: stubPerformanceSource{err: sentinel}}
	if _, err := source.Points(context.Background()); !errors.Is(err, sentinel) {
		t.Errorf("Points error = %v, want %v", err, sentinel)
	}
}

// TestPerformanceFromSeedingNoPhase distinguishes a contest with no
// seeding phase from one whose seeding phase is not final yet.
func TestPerformanceFromSeedingNoPhase(t *testing.T) {
	m, _ := newTestModule(t)

	table, err := performanceFromSeeding{gm: m.gm}.Points(context.Background())
	if err != nil {
		t.Fatalf("Points: %v", err)
	}
	if table.State != performanceNoPhase {
		t.Errorf("state = %q, want %q", table.State, performanceNoPhase)
	}
}

func TestPerformanceFromSeedingNotFrozen(t *testing.T) {
	m, d := newTestModule(t)

	phase := game.GamePhase{Name: "Seeding", Role: game.RoleSeeding, ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}

	table, err := performanceFromSeeding{gm: m.gm}.Points(context.Background())
	if err != nil {
		t.Fatalf("Points: %v", err)
	}
	if table.State != performanceNotFrozen {
		t.Errorf("state = %q, want %q", table.State, performanceNotFrozen)
	}
	if table.PhaseName != "Seeding" {
		t.Errorf("phase name = %q, want Seeding", table.PhaseName)
	}
}

func TestPerformanceFromSeedingEndToEnd(t *testing.T) {
	m, d := newTestModule(t)

	open := divisionByName(t, d, "Open")
	teams := []team.Team{
		createTeam(t, d, 101, "Alpha", open.ID),
		createTeam(t, d, 102, "Beta", open.ID),
		createTeam(t, d, 103, "Gamma", open.ID),
	}
	seedSeedingPhase(t, d, teams, false)

	table, err := performanceFromSeeding{gm: m.gm}.Points(context.Background())
	if err != nil {
		t.Fatalf("Points: %v", err)
	}
	if table.State != performanceReady {
		t.Fatalf("state = %q, want %q", table.State, performanceReady)
	}

	want := map[uint]float64{teams[0].ID: 250, teams[1].ID: 138, teams[2].ID: 25}
	for id, expected := range want {
		if table.ByTeam[id] != expected {
			t.Errorf("team %d points = %v, want %v", id, table.ByTeam[id], expected)
		}
	}
}

// Test scenario with multiple divisions, but one BEST Award overall ranking,
// as should happen at hub-level events.
func TestDerivePerformanceOverall(t *testing.T) {
	table := performanceRankingTable("",
		rankingRow(101, "Alpha", "Open", 1),
		rankingRow(102, "Beta", "Open", 2),
		rankingRow(103, "Gamma", "Open", 3),
		rankingRow(201, "Delta", "Rookie", 4),
		rankingRow(202, "Epsilon", "Rookie", 5),
	)

	result := derivePerformance([]game.PhaseRanking{table}, false)
	if result.SplitByDivision {
		t.Errorf("an overall ranking should never be reported as split")
	}

	points := pointsByTeamNumber(result, table.Rows)
	want := map[int]float64{101: 250, 102: 194, 103: 138, 201: 81, 202: 25}
	for number, expected := range want {
		if points[number] != expected {
			t.Errorf("team %d points = %v, want %v", number, points[number], expected)
		}
	}
}

func TestDerivePerformanceOverallPreservesTies(t *testing.T) {
	table := performanceRankingTable("",
		rankingRow(101, "Alpha", "Open", 1),
		rankingRow(201, "Delta", "Rookie", 1),
		rankingRow(102, "Beta", "Open", 3),
	)

	points := pointsByTeamNumber(derivePerformance([]game.PhaseRanking{table}, false), table.Rows)
	if points[101] != 250 || points[201] != 250 {
		t.Errorf("tied leaders across divisions = %v and %v, want 250 each", points[101], points[201])
	}
	if points[102] != 138 {
		t.Errorf("trailing team = %v, want 138", points[102])
	}
}

// TestBestAwardPerformanceFollowsAwardGrouping checks end to end that
// Robot Performance is ranked the way the BEST Award is given out:
// overall while no phase is division aware, per division once one is.
func TestBestAwardPerformanceFollowsAwardGrouping(t *testing.T) {
	for _, tc := range []struct {
		name          string
		divisionAware bool
		want          map[int]string
	}{
		{"overall award", false, map[int]string{101: "250.00", 102: "194.00", 103: "138.00", 201: "81.00", 202: "25.00"}},
		{"per-division award", true, map[int]string{101: "250.00", 102: "138.00", 103: "25.00", 201: "250.00", 202: "25.00"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			m, d := newTestModule(t)

			open := divisionByName(t, d, "Open")
			rookie := createDivision(t, d, "Rookie")
			teams := []team.Team{
				createTeam(t, d, 101, "Alpha", open.ID),
				createTeam(t, d, 102, "Beta", open.ID),
				createTeam(t, d, 103, "Gamma", open.ID),
				createTeam(t, d, 201, "Delta", rookie.ID),
				createTeam(t, d, 202, "Epsilon", rookie.ID),
			}
			// The seeding phase itself is never division aware here;
			// a later phase decides whether the awards are.
			seedSeedingPhase(t, d, teams, false)
			if tc.divisionAware {
				later := game.GamePhase{Name: "Finals", Role: game.RoleFinal, ScoreSummation: "Total", DivisionAware: true}
				if err := d.Create(&later).Error; err != nil {
					t.Fatalf("create division aware phase: %v", err)
				}
			}

			results, err := m.computeAwards(context.Background())
			if err != nil {
				t.Fatalf("computeAwards: %v", err)
			}
			var diag *awardDiagnosis
			for _, res := range results {
				if res.Award.Key == "best" {
					if res.Award.DivisionAware != tc.divisionAware {
						t.Fatalf("award DivisionAware = %v, want %v", res.Award.DivisionAware, tc.divisionAware)
					}
					diag = res.Diagnosis
				}
			}
			if diag == nil {
				t.Fatal("BEST Award has no diagnosis")
			}

			got := map[int]string{}
			for _, c := range diag.Candidates {
				got[c.Team.Number] = c.Cells[len(c.Cells)-1].Display
			}
			for number, want := range tc.want {
				if got[number] != want {
					t.Errorf("team %d Robot Performance = %s, want %s", number, got[number], want)
				}
			}
		})
	}
}
