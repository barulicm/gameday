package game

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"testing"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
	"gorm.io/gorm"
)

// recordingFilter is a fake AdvancementFilter that records what the
// driver put in its context and, in include mode, advances every team
// in the roster.
type recordingFilter struct {
	name       string
	calls      int
	sbSize     int
	rosterSize int
}

func (f *recordingFilter) Name() string { return f.name }

func (f *recordingFilter) Apply(sctx *AdvancementFilterContext, rule string, mode GamePhaseAdvancementFilterMode, sliceExpr string) error {
	f.calls++
	f.sbSize = len(sctx.Scoreboard)
	f.rosterSize = len(sctx.Roster)
	if mode == GamePhaseAdvancementFilterModeInclude {
		for _, t := range sctx.Roster {
			sctx.Candidates[t.ID] = t
			sctx.Determinations = append(sctx.Determinations, AdvancementDeterminationResult{
				Filter: f.Name(),
				Rule:   rule,
				Team:   t,
				Result: AdvancementDeterminationAccept,
				Reason: "recording filter accepts roster",
			})
		}
	}
	return nil
}

func newTestModule(t *testing.T, opts ...web.Option) (*Module, *db.DB) {
	t.Helper()

	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))
	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	return newTestModuleWithDB(t, d, opts...)
}

// newTestModuleDSN is like newTestModule but honors GAMEDAY_TEST_PG_DSN
// when set, so the tests that use it can run against a shared Postgres
// database. Tests that use it must be self-contained (they cannot
// assume a fresh database).
func newTestModuleDSN(t *testing.T, opts ...web.Option) (*Module, *db.DB) {
	t.Helper()

	var (
		d   *db.DB
		err error
	)
	if dsn := os.Getenv("GAMEDAY_TEST_PG_DSN"); dsn != "" {
		d, err = db.New(db.WithDriver(db.DriverPostgres), db.WithDSN(dsn))
	} else {
		t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))
		d, err = db.New()
	}
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	return newTestModuleWithDB(t, d, opts...)
}

func newTestModuleWithDB(t *testing.T, d *db.DB, opts ...web.Option) (*Module, *db.DB) {
	t.Helper()

	defaults := []web.Option{web.WithDB(d)}
	defaults = append(defaults, opts...)
	ws, err := web.NewServer(defaults...)
	if err != nil {
		t.Fatalf("web.NewServer: %v", err)
	}

	tm := team.New("onsite", d, ws, nil)
	if tm == nil {
		t.Fatal("team.New failed")
	}
	if err := tm.Migrate(); err != nil {
		t.Fatalf("team Migrate: %v", err)
	}

	// Scorecard submits fire a background cloud push.  Unless a test
	// points GAMEDAY_CLOUD at a fake server, aim the client at an
	// unreachable local port so tests never dial the real cloud, and
	// wait for any in-flight push before the test's database is torn
	// down.
	if os.Getenv("GAMEDAY_CLOUD") == "" {
		t.Setenv("GAMEDAY_CLOUD", "http://127.0.0.1:1")
	}

	deps := modules.ModuleDeps{"team": tm}
	m := New("onsite", d, ws, deps)
	if m == nil {
		t.Fatal("game.New failed")
	}
	t.Cleanup(m.autoPush.wg.Wait)
	if err := m.Migrate(); err != nil {
		t.Fatalf("game Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.basePath = "/ui/mod/game"

	return m, d
}

// seedTeams creates n teams with distinct numbers. The number base is
// derived from the current maximum team number in the database (re-read
// on every call), so ranges stay contiguous and non-overlapping even on
// a shared database that already holds teams from earlier runs.
func seedTeams(t *testing.T, d *db.DB, n int) []team.Team {
	t.Helper()

	// Teams carry foreign keys to their division and region; Postgres
	// enforces them, so seed against the "Open" division that
	// team.Migrate guarantees and a get-or-created test region.
	open, err := gorm.G[team.Division](d.DB).Where(&team.Division{Name: "Open"}).First(context.Background())
	if err != nil {
		t.Fatalf("find Open division: %v", err)
	}
	region, err := gorm.G[team.Region](d.DB).Where(&team.Region{Name: "Test"}).First(context.Background())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find Test region: %v", err)
	}
	if region.ID == 0 {
		region = team.Region{Name: "Test"}
		if err := d.Create(&region).Error; err != nil {
			t.Fatalf("create Test region: %v", err)
		}
	}

	var maxNum int
	if err := d.Raw().Raw("SELECT COALESCE(MAX(number), 0) FROM teams").Scan(&maxNum).Error; err != nil {
		t.Fatalf("max team number: %v", err)
	}
	base := maxNum + n

	var teams []team.Team
	for i := 1; i <= n; i++ {
		tr := team.Team{Name: fmt.Sprintf("Team %d", i), Number: base - n + i, DivisionID: open.ID, RegionID: region.ID}
		if err := d.Create(&tr).Error; err != nil {
			t.Fatalf("create team: %v", err)
		}
		teams = append(teams, tr)
	}
	return teams
}

// TestRunAdvancementFiltersRosterSource verifies that a filter with
// SelectFrom of 0 is handed an empty scoreboard and a populated
// roster, and that candidates flow through from the roster.
func TestRunAdvancementFiltersRosterSource(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	rec := &recordingFilter{name: "testRosterRecorder"}
	RegisterAdvancementFilter(rec.Name(), rec)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      rec.Name(),
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	advancing, determinations, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}

	if rec.calls != 1 {
		t.Fatalf("filter called %d times, want 1", rec.calls)
	}
	if rec.sbSize != 0 {
		t.Errorf("scoreboard size = %d, want 0 for roster-sourced filter", rec.sbSize)
	}
	if rec.rosterSize != len(teams) {
		t.Errorf("roster size = %d, want %d", rec.rosterSize, len(teams))
	}
	if len(advancing) != len(teams) {
		t.Errorf("advancing = %d teams, want %d", len(advancing), len(teams))
	}
	if len(determinations) != len(teams) {
		t.Errorf("determinations = %d, want %d", len(determinations), len(teams))
	}
}

// TestRunAdvancementFiltersRosterSourceNoFallback verifies that
// ScoreboardRanking produces zero candidates on an empty scoreboard
// (SelectFrom of 0) without panicking and without falling back to the
// roster.
func TestRunAdvancementFiltersRosterSourceNoFallback(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "PickTop4",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	advancing, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if len(advancing) != 0 {
		t.Errorf("advancing = %d teams, want 0 (no roster fallback)", len(advancing))
	}
}

// TestScoreboardRankingsEmptyWithTieBreaker verifies that rendering a
// scoreboard for a phase with a configured tie-breaker but no match
// scores returns an empty result instead of panicking in resolveTies.
func TestScoreboardRankingsEmptyWithTieBreaker(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	modules.RegisterTieBreaker("testEmptyTB", func(teamNumbers []int, matchCount int) []int {
		return teamNumbers
	})

	phase := GamePhase{
		Name:           "seeding",
		ScoreSummation: "AverageWithMulligan",
		ScheduleType:   "RandomSeeding",
		TieBreaker:     "testEmptyTB",
	}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}

	rows, err := m.scoreboardRankings(ctx, phase.ID, "")
	if err != nil {
		t.Fatalf("scoreboardRankings: %v", err)
	}
	if len(rows) != 0 {
		t.Errorf("scoreboardRankings returned %d rows, want 0", len(rows))
	}
}

// TestResolveTiesEmptyRows verifies that resolveTies returns cleanly
// when the scoreboard has no rows.
func TestResolveTiesEmptyRows(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	modules.RegisterTieBreaker("testEmptyTB2", func(teamNumbers []int, matchCount int) []int {
		return teamNumbers
	})

	phase := &GamePhase{TieBreaker: "testEmptyTB2"}
	rows := []RankingRow{}
	resolveTies(ctx, d, &rows, phase)

	if len(rows) != 0 {
		t.Errorf("resolveTies left %d rows, want 0", len(rows))
	}
}

// tiedScore is a (team number, score) pair used to build scratch
// ranking rows.
type tiedScore struct {
	number int
	score  int
}

// scratchRows builds ranking rows from (team number, score) pairs in
// the given order, the order the scoreboard query returns them, with
// dense ranks already assigned by score.
func scratchRows(pairs ...tiedScore) []RankingRow {
	rows := make([]RankingRow, 0, len(pairs))
	for _, p := range pairs {
		rows = append(rows, RankingRow{
			TeamID: uint(p.number),
			Team:   team.Team{ID: uint(p.number), Number: p.number},
			Score:  p.score,
		})
	}
	rank := 1
	for i := range rows {
		if i > 0 && rows[i-1].Score != rows[i].Score {
			rank++
		}
		rows[i].Rank = rank
	}
	return rows
}

// assertNoScoreInversion fails when a row with a lower score holds a
// rank ahead of a row with a higher score.
func assertNoScoreInversion(t *testing.T, rows []RankingRow) {
	t.Helper()
	for i := 1; i < len(rows); i++ {
		if rows[i].Score > rows[i-1].Score {
			t.Errorf("rank inversion: row %d (team %d, score %d) ranked above row %d (team %d, score %d)",
				i, rows[i].Team.Number, rows[i].Score,
				i-1, rows[i-1].Team.Number, rows[i-1].Score)
		}
	}
}

// TestResolveTiesRenumbersBehindGroup verifies a resolved tied group
// of three or more teams pushes the rows behind it down instead of
// letting them keep their stale dense ranks: with a 100/90/90/90/80
// board the 80-pointer must rank below every 90-pointer, not share or
// leap a rank among them.
func TestResolveTiesRenumbersBehindGroup(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	modules.RegisterTieBreaker("testReverseTB", func(nums []int, _ int) []int {
		out := slices.Clone(nums)
		sort.Slice(out, func(i, j int) bool { return out[i] > out[j] })
		return out
	})

	rows := scratchRows(
		tiedScore{101, 100}, tiedScore{102, 90}, tiedScore{103, 90}, tiedScore{104, 90}, tiedScore{105, 80},
	)
	resolveTies(ctx, d, &rows, &GamePhase{TieBreaker: "testReverseTB"})

	assertNoScoreInversion(t, rows)
	wantOrder := []int{101, 104, 103, 102, 105}
	for i, want := range wantOrder {
		if rows[i].Team.Number != want {
			t.Errorf("position %d: team %d, want %d (%v)", i, rows[i].Team.Number, want, teamNumbers(rows))
		}
	}
	// Every team holds a distinct rank: the tie was fully broken.
	seen := map[int]struct{}{}
	for _, r := range rows {
		if _, dup := seen[r.Rank]; dup {
			t.Errorf("duplicate rank %d in %v", r.Rank, ranks(rows))
		}
		seen[r.Rank] = struct{}{}
	}
}

// TestResolveTiesMultipleGroupsAccumulateShift verifies that when two
// or more tied groups are resolved, every later group and every row
// behind them shifts by the accumulated expansion: on a
// 100/100/90/90/80 board the 80-pointer must land last, ahead of
// nobody.
func TestResolveTiesMultipleGroupsAccumulateShift(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	modules.RegisterTieBreaker("testIdentityTB", func(nums []int, _ int) []int {
		return slices.Clone(nums)
	})

	rows := scratchRows(
		tiedScore{101, 100}, tiedScore{102, 100}, tiedScore{103, 90}, tiedScore{104, 90}, tiedScore{105, 80},
	)
	resolveTies(ctx, d, &rows, &GamePhase{TieBreaker: "testIdentityTB"})

	assertNoScoreInversion(t, rows)
	wantOrder := []int{101, 102, 103, 104, 105}
	for i, want := range wantOrder {
		if rows[i].Team.Number != want {
			t.Errorf("position %d: team %d, want %d (%v)", i, rows[i].Team.Number, want, teamNumbers(rows))
		}
	}
	wantRanks := []int{1, 2, 3, 4, 5}
	for i, want := range wantRanks {
		if rows[i].Rank != want {
			t.Errorf("position %d: rank %d, want %d (%v)", i, rows[i].Rank, want, ranks(rows))
		}
	}
}

// TestResolveTiesPartialGroupKeepsTie verifies rows a resolved group
// did not reorder (a division aware phase's cross-division singleton
// sub-groups) still share ranks with each other when their scores
// match, while the reordered members expand ahead of them.
func TestResolveTiesPartialGroupKeepsTie(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	modules.RegisterTieBreaker("testPartialTB", func(nums []int, _ int) []int {
		return slices.Clone(nums)
	})

	// Rank-1 block: division Red has two teams the tie-breaker
	// separates, division Blue has one team nobody compares.  The Red
	// pair expands to strict ranks; the Blue team, sharing the block's
	// score, lands behind the pair but on top of a lower score, never
	// above it.
	rows := scratchRows(
		tiedScore{101, 90}, tiedScore{102, 90}, tiedScore{201, 90}, tiedScore{103, 80},
	)
	rows[0].Team.Division.Name = "Red"
	rows[1].Team.Division.Name = "Red"
	rows[2].Team.Division.Name = "Blue"
	rows[3].Team.Division.Name = "Red"
	resolveTies(ctx, d, &rows, &GamePhase{TieBreaker: "testPartialTB", DivisionAware: true})

	assertNoScoreInversion(t, rows)
	wantOrder := []int{101, 102, 201, 103}
	for i, want := range wantOrder {
		if rows[i].Team.Number != want {
			t.Errorf("position %d: team %d, want %d (%v)", i, rows[i].Team.Number, want, teamNumbers(rows))
		}
	}
	// The separated Red pair is strict; the untouched Blue team keeps
	// its own rank behind the pair; the 80-pointer shifts down with
	// the expansion.
	wantRanks := []int{1, 2, 3, 4}
	for i, want := range wantRanks {
		if rows[i].Rank != want {
			t.Errorf("position %d: rank %d, want %d (%v)", i, rows[i].Rank, want, ranks(rows))
		}
	}
}

// TestResolveTiesUntouchedBoardStaysShared verifies a board where the
// tie-breaker returns tied members in input order still reorders them
// strictly (the breaker decides strict order), while a separate tied
// block with only cross-division singletons keeps sharing its rank.
func TestResolveTiesCrossDivisionSingletonKeepsTie(t *testing.T) {
	_, d := newTestModule(t)
	ctx := context.Background()

	// A no-op breaker: it returns the input untouched, but the group
	// is still "resolved" in the sense that ranks expand.
	modules.RegisterTieBreaker("testNoopTB", func(nums []int, _ int) []int {
		return slices.Clone(nums)
	})

	rows := scratchRows(
		tiedScore{101, 90}, tiedScore{201, 90}, tiedScore{102, 80},
	)
	rows[0].Team.Division.Name = "Red"
	rows[1].Team.Division.Name = "Blue"
	rows[2].Team.Division.Name = "Red"
	resolveTies(ctx, d, &rows, &GamePhase{TieBreaker: "testNoopTB", DivisionAware: true})

	// Both 90s are in different divisions, so no sub-group of two
	// exists and nothing is reordered: the tie must stay shared.
	if rows[0].Rank != rows[1].Rank {
		t.Errorf("cross-division teams lost their shared rank: %v", ranks(rows))
	}
	if rows[2].Rank != 2 {
		t.Errorf("80-pointer rank = %d, want 2 (%v)", rows[2].Rank, ranks(rows))
	}
}

// TestOrderTiedGroupSanitizes verifies a misbehaving tie-breaker can
// never drop or duplicate a team: unknown and repeated numbers are
// skipped and any team it failed to return keeps its slot at the end.
func TestOrderTiedGroupSanitizes(t *testing.T) {
	rows := scratchRows(tiedScore{101, 90}, tiedScore{102, 90}, tiedScore{103, 90})
	group := []int{0, 1, 2}

	broken := func(nums []int, _ int) []int {
		return []int{103, 103, 999, 101}
	}
	got := orderTiedGroup(broken, rows, group, 0)

	want := []int{2, 0, 1}
	if len(got) != len(want) {
		t.Fatalf("got %d indices, want %d: %v", len(got), len(want), got)
	}
	for i, w := range want {
		if got[i] != w {
			t.Errorf("position %d: index %d, want %d (%v)", i, got[i], w, got)
		}
	}
}

func teamNumbers(rows []RankingRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Team.Number)
	}
	return out
}

func ranks(rows []RankingRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Rank)
	}
	return out
}

// TestRosterAdvancementInclude verifies the Roster filter advances
// every team in the roster in include mode with one determination per
// team.
func TestRosterAdvancementInclude(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "PickAll",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	advancing, determinations, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if len(advancing) != len(teams) {
		t.Errorf("advancing = %d teams, want %d", len(advancing), len(teams))
	}
	if len(determinations) != len(teams) {
		t.Errorf("determinations = %d, want %d", len(determinations), len(teams))
	}
	for _, det := range determinations {
		if det.Result != AdvancementDeterminationAccept {
			t.Errorf("determination for team %d = %v, want accept", det.Team.ID, det.Result)
		}
	}
}

// TestRosterAdvancementExclude verifies the Roster filter removes
// every roster team from the candidates in exclude mode while leaving
// non-roster candidates untouched.
func TestRosterAdvancementExclude(t *testing.T) {
	sctx := AdvancementFilterContext{
		Roster:     map[uint]team.Team{1: {ID: 1}, 2: {ID: 2}},
		Candidates: map[uint]team.Team{1: {ID: 1}, 2: {ID: 2}, 3: {ID: 3}},
	}
	f := new(RosterAdvancement)
	if err := f.Apply(&sctx, "DropAll", GamePhaseAdvancementFilterModeExclude, ""); err != nil {
		t.Fatalf("Apply: %v", err)
	}
	if len(sctx.Candidates) != 1 {
		t.Errorf("candidates = %d, want 1 (only team 3 survives)", len(sctx.Candidates))
	}
	if _, ok := sctx.Candidates[3]; !ok {
		t.Error("team 3 not in candidates, want it to survive")
	}
}

// TestRunAdvancementFiltersRealSource verifies that a non-zero
// SelectFrom still loads the source phase scoreboard.
func TestRunAdvancementFiltersRealSource(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 5)

	src := GamePhase{Name: "qualifying", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&src).Error; err != nil {
		t.Fatalf("create source phase: %v", err)
	}
	for i, tr := range teams {
		ms := MatchScore{Score: 10 + i, GamePhaseID: src.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	rec := &recordingFilter{name: "testRealSourceRecorder"}
	RegisterAdvancementFilter(rec.Name(), rec)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      rec.Name(),
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  src.ID,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	_, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if rec.calls != 1 {
		t.Fatalf("filter called %d times, want 1", rec.calls)
	}
	if rec.sbSize != len(teams) {
		t.Errorf("scoreboard size = %d, want %d from source phase", rec.sbSize, len(teams))
	}
}

// TestRunAdvancementFiltersExcludeThenInclude verifies the
// "best of the losers" pipeline: an exclude filter rejects the top
// seeders, and the subsequent include filter selects the top teams
// of what remains, so the include cutoff applies to the remaining
// field rather than the whole field.  Mirrors the byte2bite Wildcard
// phase (drop the seeding leaders, then take the top remaining
// teams from the scoreboard).
func TestRunAdvancementFiltersExcludeThenInclude(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 12)

	src := GamePhase{Name: "qualifying", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&src).Error; err != nil {
		t.Fatalf("create source phase: %v", err)
	}
	// Distinct scores: teams[0] has the highest score, so the
	// scoreboard is teams[0] (highest) through teams[11] (lowest).
	for i, tr := range teams {
		ms := MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: src.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	phase := GamePhase{Name: "wildcard", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	excl := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "DropTop4",
		Mode:        GamePhaseAdvancementFilterModeExclude,
		SelectFrom:  src.ID,
		SliceExpr:   "4",
	}
	if err := d.Create(&excl).Error; err != nil {
		t.Fatalf("create exclude filter: %v", err)
	}
	incl := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "PickTop6",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  src.ID,
		SliceExpr:   "6",
	}
	if err := d.Create(&incl).Error; err != nil {
		t.Fatalf("create include filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{excl, incl}

	advancing, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}

	// The top 4 seeders are rejected; of the 8 remaining teams the
	// top 6 (teams[4] through teams[9]) advance.  Under the old
	// whole-field cutoff only teams[4] and teams[5] would advance.
	for i := 0; i < 4; i++ {
		if _, ok := advancing[teams[i].ID]; ok {
			t.Errorf("excluded team %d (scoreboard position %d) advanced", teams[i].ID, i+1)
		}
	}
	for i := 4; i < 10; i++ {
		if _, ok := advancing[teams[i].ID]; !ok {
			t.Errorf("remaining team %d (scoreboard position %d) did not advance", teams[i].ID, i+1)
		}
	}
	for i := 10; i < len(teams); i++ {
		if _, ok := advancing[teams[i].ID]; ok {
			t.Errorf("out-of-cutoff team %d (scoreboard position %d) advanced", teams[i].ID, i+1)
		}
	}
	if len(advancing) != 6 {
		t.Errorf("advancing = %v, want 6 teams", advancing)
	}
}

// TestRunAdvancementFiltersUnregisteredFilter verifies that an
// unregistered filter name produces an error from the driver.
func TestRunAdvancementFiltersUnregisteredFilter(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 3)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "DefinitelyNotRegistered",
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		SliceExpr:   "4",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	if _, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams); err == nil {
		t.Fatal("expected error for unregistered filter, got nil")
	}
}

// TestPhaseSchedulable verifies the scheduling gate: a phase whose
// only filter is roster-sourced is schedulable immediately, a phase
// gated on a real source waits for that source to complete and be
// frozen, and a mixed phase is still gated by its real source.
func TestPhaseSchedulable(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	if err := d.Create(&GamePhase{Name: "qualifying", Frozen: true}).Error; err != nil {
		t.Fatalf("create phase 1: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "seeding" /* roster-sourced only */}).Error; err != nil {
		t.Fatalf("create phase 2: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "mix" /* roster + real source */}).Error; err != nil {
		t.Fatalf("create phase 3: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "waiting" /* real source only */}).Error; err != nil {
		t.Fatalf("create phase 4: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "manual" /* no filters */}).Error; err != nil {
		t.Fatalf("create phase 5: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "routed" /* false-When filter must not block */}).Error; err != nil {
		t.Fatalf("create phase 6: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "routed-on" /* true-When filter must block */}).Error; err != nil {
		t.Fatalf("create phase 7: %v", err)
	}

	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 2, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 3, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 3, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 4, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 6, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4", When: "false"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	if err := d.Create(&GamePhaseAdvancementFilter{GamePhaseID: 7, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4", When: "len(Roster) < 4"}).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}

	teams := seedTeams(t, d, 3)

	phases, err := findPhases(m, ctx)
	if err != nil {
		t.Fatalf("find phases: %v", err)
	}

	cases := []struct {
		name  string
		phase uint
		comp  map[uint]bool
		want  bool
	}{
		{"roster-only phase is immediately schedulable", 2, map[uint]bool{}, true},
		{"mixed phase waits on real source", 3, map[uint]bool{}, false},
		{"mixed phase satisfied when real source complete and frozen", 3, map[uint]bool{1: true}, true},
		{"real-source phase waits when source incomplete", 4, map[uint]bool{}, false},
		{"real-source phase satisfied when source complete and frozen", 4, map[uint]bool{1: true}, true},
		{"filterless phase is schedulable", 5, map[uint]bool{}, true},
		{"false-When filter never blocks schedulability", 6, map[uint]bool{}, true},
		{"false-When filter stays out of the way when source is ready", 6, map[uint]bool{1: true}, true},
		{"true-When filter blocks while its source is not ready", 7, map[uint]bool{}, false},
		{"true-When filter satisfied once its source is ready", 7, map[uint]bool{1: true}, true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			var target GamePhase
			for _, p := range phases {
				if p.ID == tc.phase {
					target = p
					break
				}
			}
			verdict, err := m.phaseSchedulable(ctx, target, phases, tc.comp, teams)
			if err != nil {
				t.Fatalf("phaseSchedulable: %v", err)
			}
			if verdict.CanRun != tc.want {
				t.Errorf("phaseSchedulable = %v (%v), want %v", verdict.CanRun, verdict.Reasons, tc.want)
			}
		})
	}
}

func findPhases(m *Module, ctx context.Context) ([]GamePhase, error) {
	return gorm.G[GamePhase](m.db.DB).Find(ctx)
}

// TestRunAdvancementFiltersWhen verifies the per-filter When
// condition: an empty or true condition runs the filter, false skips
// it (contributing no candidates or determinations), and the
// expression sees the same context as a phase When expression,
// including the filter's own SelectFrom scoreboard.  Compilation,
// runtime, and non-boolean errors propagate.
func TestRunAdvancementFiltersWhen(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 3)

	if err := d.Create(&GamePhase{Name: "qualifying", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}).Error; err != nil {
		t.Fatalf("create qualifying: %v", err)
	}
	if err := d.Create(&GamePhase{Name: "finals", ScoreSummation: "Total", ScheduleType: "OneShot"}).Error; err != nil {
		t.Fatalf("create finals: %v", err)
	}
	phases := whenTestPhases(t, m, ctx)
	if len(phases) != 2 {
		t.Fatalf("expected 2 phases, got %d", len(phases))
	}
	byName := map[string]GamePhase{}
	for _, p := range phases {
		byName[p.Name] = p
	}
	qualifying, finals := byName["qualifying"], byName["finals"]

	// Seed qualifying as complete and with a scoreboard.
	mp := MatchPlacement{PhaseID: qualifying.ID, TeamID: teams[0].ID, State: MatchStateComplete}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create match placement: %v", err)
	}
	for i, tr := range teams {
		ms := MatchScore{Score: 10 + i, GamePhaseID: qualifying.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	rec := &recordingFilter{name: "testFilterWhenRecorder"}
	RegisterAdvancementFilter(rec.Name(), rec)
	filt := GamePhaseAdvancementFilter{
		GamePhaseID: finals.ID,
		Filter:      rec.Name(),
		Rule:        "PickRoster",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  qualifying.ID,
		SliceExpr:   "3",
	}
	if err := d.Create(&filt).Error; err != nil {
		t.Fatalf("create filter: %v", err)
	}
	finals.AdvancementFilters = []GamePhaseAdvancementFilter{filt}

	phaseComplete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phaseCompletionStates: %v", err)
	}

	cases := []struct {
		name      string
		when      string
		division  string
		wantErr   bool
		wantCalls int
		wantAdv   int
		wantDets  int
	}{
		{"empty When runs the filter", "", "", false, 1, len(teams), len(teams)},
		{"literal true runs the filter", "true", "", false, 1, len(teams), len(teams)},
		{"literal false skips the filter", "false", "", false, 0, 0, 0},
		{"phase state reference satisfied (qualifying complete)", "Phases[1].Complete", "", false, 1, len(teams), len(teams)},
		{"phase state reference unsatisfied (finals not frozen)", "Phases[0].Frozen", "", false, 0, 0, 0},
		{"roster size satisfied", "len(Roster) == 3", "", false, 1, len(teams), len(teams)},
		{"scoreboard from own SelectFrom", "Scoreboard[0].Rank == 1", "", false, 1, len(teams), len(teams)},
		{"scoreboard rank mismatch skips", "Scoreboard[0].Rank == 2", "", false, 0, 0, 0},
		{"division context mismatch skips", `Division == "Open"`, "", false, 0, 0, 0},
		{"division context satisfied", `Division == ""`, "", false, 1, len(teams), len(teams)},
		{"syntax error propagates", "this is not valid(((", "", true, 0, 0, 0},
		{"non-boolean result propagates", "1", "", true, 0, 0, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			filt.When = tc.when
			finals.AdvancementFilters = []GamePhaseAdvancementFilter{filt}
			rec.calls = 0

			advancing, determinations, err := m.runAdvancementFilters(ctx, finals, phases, phaseComplete, tc.division, teams)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("runAdvancementFilters = no error, want error")
				}
				return
			}
			if err != nil {
				t.Fatalf("runAdvancementFilters: %v", err)
			}
			if rec.calls != tc.wantCalls {
				t.Errorf("filter called %d times, want %d", rec.calls, tc.wantCalls)
			}
			if len(advancing) != tc.wantAdv {
				t.Errorf("advancing = %d teams, want %d", len(advancing), tc.wantAdv)
			}
			if len(determinations) != tc.wantDets {
				t.Errorf("determinations = %d, want %d", len(determinations), tc.wantDets)
			}
		})
	}
}

// TestRunAdvancementFiltersWhenSkippedStillRunsOthers verifies that a
// filter skipped by a false When condition does not stop the
// remaining filters on the phase from applying.
func TestRunAdvancementFiltersWhenSkippedStillRunsOthers(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 4)

	phase := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	skipped := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "PickAll",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		When:        "false",
	}
	if err := d.Create(&skipped).Error; err != nil {
		t.Fatalf("create skipped filter: %v", err)
	}
	kept := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "PickAll",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
		When:        "true",
	}
	if err := d.Create(&kept).Error; err != nil {
		t.Fatalf("create kept filter: %v", err)
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{skipped, kept}

	advancing, determinations, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}
	if len(advancing) != len(teams) {
		t.Errorf("advancing = %d teams, want %d (kept filter ran)", len(advancing), len(teams))
	}
	if len(determinations) != len(teams) {
		t.Errorf("determinations = %d, want %d (only the kept filter emits)", len(determinations), len(teams))
	}
	for _, det := range determinations {
		if det.Rule != "PickAll" {
			t.Errorf("determination rule = %q, want PickAll", det.Rule)
		}
	}
}

// seedPhaseScore creates a completed MatchPlacement for (phase, team)
// and a matching MatchScore, returning the placement.
func seedPhaseScore(t *testing.T, d *db.DB, phaseID uint, tr team.Team, score int) MatchPlacement {
	t.Helper()
	return seedPhaseScoreField(t, d, phaseID, tr, score, "Test")
}

// seedPhaseScoreField is like seedPhaseScore but names the field the
// placement is seeded on.
func seedPhaseScoreField(t *testing.T, d *db.DB, phaseID uint, tr team.Team, score int, fieldName string) MatchPlacement {
	t.Helper()

	// MatchPlacement carries foreign keys to a field and a position;
	// Postgres enforces them, so seed against get-or-created rows.
	field, err := gorm.G[Field](d.DB).Where(&Field{Name: fieldName}).First(context.Background())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find %s field: %v", fieldName, err)
	}
	if field.ID == 0 {
		field = Field{Name: fieldName}
		if err := d.Create(&field).Error; err != nil {
			t.Fatalf("create %s field: %v", fieldName, err)
		}
	}
	position, err := gorm.G[FieldPosition](d.DB).Where(&FieldPosition{Name: "Test"}).First(context.Background())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		t.Fatalf("find Test position: %v", err)
	}
	if position.ID == 0 {
		position = FieldPosition{Name: "Test"}
		if err := d.Create(&position).Error; err != nil {
			t.Fatalf("create Test position: %v", err)
		}
	}

	mp := MatchPlacement{PhaseID: phaseID, TeamID: tr.ID, FieldID: field.ID, PositionID: position.ID, State: MatchStateComplete}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create placement: %v", err)
	}
	ms := MatchScore{ID: mp.ID, MatchPlacementID: mp.ID, GamePhaseID: phaseID, TeamID: tr.ID, Score: score}
	if err := d.Create(&ms).Error; err != nil {
		t.Fatalf("create score: %v", err)
	}
	return mp
}

// TestPhaseScoreStats verifies the min, max, and average of a phase's
// scores, and that the placements (with their teams) reported for the
// extremes are the ones that produced the values.
func TestPhaseScoreStats(t *testing.T) {
	m, d := newTestModuleDSN(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 3)
	phase := GamePhase{Name: "qualifying", ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}

	scores := []int{10, 30, 20}
	for i, tr := range teams {
		seedPhaseScore(t, d, phase.ID, tr, scores[i])
	}

	stats, err := m.PhaseScoreStats(ctx, phase.ID)
	if err != nil {
		t.Fatalf("PhaseScoreStats: %v", err)
	}
	if stats.Min != 10 {
		t.Errorf("Min = %d, want 10", stats.Min)
	}
	if stats.Max != 30 {
		t.Errorf("Max = %d, want 30", stats.Max)
	}
	if stats.Avg != 20 {
		t.Errorf("Avg = %d, want 20", stats.Avg)
	}
	if stats.MinPlacement.TeamID != teams[0].ID {
		t.Errorf("MinPlacement.TeamID = %d, want %d", stats.MinPlacement.TeamID, teams[0].ID)
	}
	if stats.MaxPlacement.TeamID != teams[1].ID {
		t.Errorf("MaxPlacement.TeamID = %d, want %d", stats.MaxPlacement.TeamID, teams[1].ID)
	}
	if stats.MinPlacement.Team.Name != teams[0].Name {
		t.Errorf("MinPlacement.Team.Name = %q, want %q (preload)", stats.MinPlacement.Team.Name, teams[0].Name)
	}
	if stats.MinPlacement.Field.Name != "Test" || stats.MaxPlacement.Field.Name != "Test" {
		t.Errorf("extreme placements missing Field preload: min=%q max=%q", stats.MinPlacement.Field.Name, stats.MaxPlacement.Field.Name)
	}
	if stats.MinPlacement.Position.Name != "Test" || stats.MaxPlacement.Position.Name != "Test" {
		t.Errorf("extreme placements missing Position preload: min=%q max=%q", stats.MinPlacement.Position.Name, stats.MaxPlacement.Position.Name)
	}
}

// TestPhaseScoreStatsEmpty verifies that a phase with no scores
// returns a zero PhaseStats and a nil error.
func TestPhaseScoreStatsEmpty(t *testing.T) {
	m, d := newTestModuleDSN(t)
	ctx := context.Background()

	seedTeams(t, d, 3)
	phase := GamePhase{Name: "qualifying", ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}

	stats, err := m.PhaseScoreStats(ctx, phase.ID)
	if err != nil {
		t.Fatalf("PhaseScoreStats: %v", err)
	}
	if stats.Min != 0 || stats.Max != 0 || stats.Avg != 0 {
		t.Errorf("stats = %+v, want zero PhaseStats", stats)
	}
	if stats.MinPlacement.ID != 0 || stats.MaxPlacement.ID != 0 {
		t.Errorf("stats = %+v, want zero PhaseStats", stats)
	}
}

// TestPhaseScoreStatsTie verifies that when several placements share
// an extreme value, the one with the lowest MatchScore ID is
// reported.
func TestPhaseScoreStatsTie(t *testing.T) {
	m, d := newTestModuleDSN(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 2)
	phase := GamePhase{Name: "qualifying", ScoreSummation: "Total"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}

	first := seedPhaseScore(t, d, phase.ID, teams[0], 20)
	seedPhaseScore(t, d, phase.ID, teams[1], 20)

	stats, err := m.PhaseScoreStats(ctx, phase.ID)
	if err != nil {
		t.Fatalf("PhaseScoreStats: %v", err)
	}
	if stats.Max != 20 {
		t.Errorf("Max = %d, want 20", stats.Max)
	}
	if stats.MaxPlacement.ID != first.ID {
		t.Errorf("MaxPlacement.ID = %d, want %d (lowest ID wins the tie)", stats.MaxPlacement.ID, first.ID)
	}
}

// TestRunAdvancementFiltersSliceExprSeesFullRoster verifies that
// len(Roster) in a slicing expression is the full roster even after an
// earlier exclude filter removed teams.  Mirrors the byte2bite Wildcard
// phase at a 40-team event: the exclude takes 14 and the include must
// still see more than 32 teams and take 8, not 4.
func TestRunAdvancementFiltersSliceExprSeesFullRoster(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 40)

	src := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&src).Error; err != nil {
		t.Fatalf("create source phase: %v", err)
	}
	for i, tr := range teams {
		ms := MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: src.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	phase := GamePhase{Name: "wildcard", ScoreSummation: "Total", ScheduleType: "OneShot"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	excl := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "IgnoreSemifinalAdvancement",
		Mode:        GamePhaseAdvancementFilterModeExclude,
		SelectFrom:  src.ID,
		SliceExpr:   "len(Roster)>32 ? 14 : 7",
	}
	incl := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "TopRemaining",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  src.ID,
		SliceExpr:   "len(Roster)>32 ? 8 : 4",
	}
	for _, f := range []*GamePhaseAdvancementFilter{&excl, &incl} {
		if err := d.Create(f).Error; err != nil {
			t.Fatalf("create filter: %v", err)
		}
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{excl, incl}

	advancing, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}

	if len(advancing) != 8 {
		t.Errorf("advancing = %d teams, want 8", len(advancing))
	}
	for i := 14; i < 22; i++ {
		if _, ok := advancing[teams[i].ID]; !ok {
			t.Errorf("team at scoreboard position %d did not advance", i+1)
		}
	}
}

// TestRunAdvancementFiltersRosterRespectsBlocklist verifies that a
// Roster include after an exclude filter does not re-advance the
// teams the exclude blocked.
func TestRunAdvancementFiltersRosterRespectsBlocklist(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	teams := seedTeams(t, d, 6)

	src := GamePhase{Name: "seeding", ScoreSummation: "AverageWithMulligan", ScheduleType: "RandomSeeding"}
	if err := d.Create(&src).Error; err != nil {
		t.Fatalf("create source phase: %v", err)
	}
	for i, tr := range teams {
		ms := MatchScore{Score: (len(teams) - i) * 10, GamePhaseID: src.ID, TeamID: tr.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("create match score: %v", err)
		}
	}

	phase := GamePhase{Name: "rest", ScoreSummation: "Total", ScheduleType: "OneShot"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	excl := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "ScoreboardRanking",
		Rule:        "DropTop2",
		Mode:        GamePhaseAdvancementFilterModeExclude,
		SelectFrom:  src.ID,
		SliceExpr:   "2",
	}
	incl := GamePhaseAdvancementFilter{
		GamePhaseID: phase.ID,
		Filter:      "Roster",
		Rule:        "Everyone",
		Mode:        GamePhaseAdvancementFilterModeInclude,
		SelectFrom:  0,
	}
	for _, f := range []*GamePhaseAdvancementFilter{&excl, &incl} {
		if err := d.Create(f).Error; err != nil {
			t.Fatalf("create filter: %v", err)
		}
	}
	phase.AdvancementFilters = []GamePhaseAdvancementFilter{excl, incl}

	advancing, _, err := m.runAdvancementFilters(ctx, phase, []GamePhase{}, map[uint]bool{}, "", teams)
	if err != nil {
		t.Fatalf("runAdvancementFilters: %v", err)
	}

	for i := 0; i < 2; i++ {
		if _, ok := advancing[teams[i].ID]; ok {
			t.Errorf("blocked team at scoreboard position %d re-advanced", i+1)
		}
	}
	if len(advancing) != 4 {
		t.Errorf("advancing = %d teams, want 4", len(advancing))
	}
}
