package game

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/db"
)

// readinessStub is a test AdvancementFilter that also implements
// AdvancementFilterReadiness: it reports a canned verdict and records
// the context of its last CanRun call so tests can assert what
// readiness was measured against.
type readinessStub struct {
	name     string
	ok       bool
	reason   string
	err      error
	calls    int
	lastSctx *AdvancementFilterContext
}

var _ AdvancementFilterReadiness = (*readinessStub)(nil)

func (s *readinessStub) Name() string { return s.name }

func (s *readinessStub) Apply(*AdvancementFilterContext, string, GamePhaseAdvancementFilterMode, string) error {
	return nil
}

func (s *readinessStub) CanRun(_ context.Context, sctx *AdvancementFilterContext) (bool, string, error) {
	s.calls++
	s.lastSctx = sctx
	return s.ok, s.reason, s.err
}

// withFilter installs f under name in the filter registry for the
// duration of the test, restoring the previous registration after.
func withFilter(t *testing.T, name string, f AdvancementFilter) {
	t.Helper()

	prev, had := filters[name]
	filters[name] = f
	t.Cleanup(func() {
		if had {
			filters[name] = prev
		} else {
			delete(filters, name)
		}
	})
}

func createPhase(t *testing.T, d *db.DB, phase GamePhase) GamePhase {
	t.Helper()
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase %s: %v", phase.Name, err)
	}
	return phase
}

func createGateFilter(t *testing.T, d *db.DB, filter GamePhaseAdvancementFilter) {
	t.Helper()
	if err := d.Create(&filter).Error; err != nil {
		t.Fatalf("create filter on phase %d: %v", filter.GamePhaseID, err)
	}
}

func loadGateState(t *testing.T, m *Module, ctx context.Context) ([]GamePhase, map[uint]bool) {
	t.Helper()
	phases, err := gorm.G[GamePhase](m.db.DB).Order("id").Find(ctx)
	if err != nil {
		t.Fatalf("load phases: %v", err)
	}
	complete, err := m.phaseCompletionStates(ctx, phases)
	if err != nil {
		t.Fatalf("phase completion: %v", err)
	}
	return phases, complete
}

// TestPhaseSchedulableReadiness covers the readiness verdicts: a
// blocking filter's reason lands in the verdict, a CanRun error
// degrades to not-ready without failing the call, a filter whose
// source phase is not ready is reported once (its own CanRun is not
// consulted), a When-skipped filter never asks CanRun, and a filter
// without the readiness interface is always ready.
func TestPhaseSchedulableReadiness(t *testing.T) {
	ctx := context.Background()

	t.Run("blocking verdict lands in reasons", func(t *testing.T) {
		m, d := newTestModule(t)
		stub := &readinessStub{name: "GateStub", ok: false, reason: "no notebook scores have been recorded yet"}
		withFilter(t, stub.name, stub)

		phase := createPhase(t, d, GamePhase{Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"})
		teams := seedTeams(t, d, 3)

		phases, complete := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, phase, phases, complete, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if verdict.CanRun {
			t.Errorf("verdict.CanRun = true, want false (reasons %v)", verdict.Reasons)
		}
		if len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "no notebook scores have been recorded yet") || !strings.Contains(verdict.Reasons[0], "Pick") {
			t.Errorf("reasons = %v, want one entry naming the rule and the reason", verdict.Reasons)
		}
		if stub.calls != 1 {
			t.Errorf("CanRun calls = %d, want 1", stub.calls)
		}
		// Readiness is measured against the full roster with an
		// empty blocklist.
		if stub.lastSctx == nil || len(stub.lastSctx.Roster) != 3 || len(stub.lastSctx.Remaining) != 3 {
			t.Errorf("readiness context = %+v, want the 3-team roster unpruned", stub.lastSctx)
		}
	})

	t.Run("multiple blockers collect in filter order", func(t *testing.T) {
		m, d := newTestModule(t)
		first := &readinessStub{name: "GateFirst", ok: false, reason: "first is down"}
		second := &readinessStub{name: "GateSecond", ok: false, reason: "second is down"}
		withFilter(t, first.name, first)
		withFilter(t, second.name, second)

		phase := createPhase(t, d, GamePhase{Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: first.name, Rule: "Alpha", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: second.name, Rule: "Beta", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"})
		teams := seedTeams(t, d, 3)

		phases, complete := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, phase, phases, complete, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if verdict.CanRun {
			t.Fatal("verdict.CanRun = true, want false")
		}
		if len(verdict.Reasons) != 2 ||
			!strings.Contains(verdict.Reasons[0], "Alpha: first is down") ||
			!strings.Contains(verdict.Reasons[1], "Beta: second is down") {
			t.Errorf("reasons = %v, want both blockers in filter order", verdict.Reasons)
		}
	})

	t.Run("CanRun error degrades to not-ready", func(t *testing.T) {
		m, d := newTestModule(t)
		stub := &readinessStub{name: "GateErr", err: errors.New("score database unreachable")}
		withFilter(t, stub.name, stub)

		phase := createPhase(t, d, GamePhase{Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"})
		teams := seedTeams(t, d, 3)

		phases, complete := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, phase, phases, complete, teams)
		if err != nil {
			t.Errorf("phaseSchedulable returned error %v, want degradation", err)
		}
		if verdict.CanRun {
			t.Error("verdict.CanRun = true, want false on a failed readiness check")
		}
		if len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "score database unreachable") {
			t.Errorf("reasons = %v, want the error message", verdict.Reasons)
		}
	})

	t.Run("unready source blocks once and skips the verdict", func(t *testing.T) {
		m, d := newTestModule(t)
		stub := &readinessStub{name: "GateStub", ok: false, reason: "no scores"}
		withFilter(t, stub.name, stub)

		source := createPhase(t, d, GamePhase{Name: "seeding"})
		phase := createPhase(t, d, GamePhase{Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: source.ID, SliceExpr: "4"})
		teams := seedTeams(t, d, 3)

		phases, complete := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, phase, phases, complete, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if verdict.CanRun {
			t.Fatal("verdict.CanRun = true, want false while the source is unfrozen")
		}
		if len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "seeding must be complete and frozen first") {
			t.Errorf("reasons = %v, want only the source-phase blocker", verdict.Reasons)
		}
		if stub.calls != 0 {
			t.Errorf("CanRun calls = %d, want 0 (the filter would read an empty board)", stub.calls)
		}
	})

	t.Run("false-When filter never asks CanRun", func(t *testing.T) {
		m, d := newTestModule(t)
		stub := &readinessStub{name: "GateStub", ok: false, reason: "no scores"}
		withFilter(t, stub.name, stub)

		phase := createPhase(t, d, GamePhase{Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4", When: "false"})
		teams := seedTeams(t, d, 3)

		phases, complete := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, phase, phases, complete, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if !verdict.CanRun {
			t.Errorf("verdict.CanRun = false (%v), want true while the filter is When-skipped", verdict.Reasons)
		}
		if stub.calls != 0 {
			t.Errorf("CanRun calls = %d, want 0", stub.calls)
		}
	})

	t.Run("filters without readiness are always ready", func(t *testing.T) {
		m, d := newTestModule(t)
		rec := &recordingFilter{name: "GatePlain"}
		withFilter(t, rec.name, rec)

		phase := createPhase(t, d, GamePhase{Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: rec.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"})
		teams := seedTeams(t, d, 3)

		phases, complete := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, phase, phases, complete, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if !verdict.CanRun {
			t.Errorf("verdict.CanRun = false (%v), want true", verdict.Reasons)
		}
	})

	// Regression: the source phase used to be looked up by indexing
	// the phase slice positionally with its ID, which read the wrong
	// row (or panicked) once IDs were non-contiguous.
	t.Run("non-contiguous source IDs resolve by ID", func(t *testing.T) {
		m, d := newTestModule(t)
		stub := &readinessStub{name: "GateStub", ok: true}
		withFilter(t, stub.name, stub)

		// IDs 1 (unfrozen) and 3 (frozen): a positional lookup of
		// source 3 into [p1, p3] would read p1 or run off the end.
		createPhase(t, d, GamePhase{ID: 1, Name: "distractor"})
		createPhase(t, d, GamePhase{ID: 3, Name: "seeding", Frozen: true})
		target := createPhase(t, d, GamePhase{ID: 5, Name: "wildcard"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: 5, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 3, SliceExpr: "4"})
		teams := seedTeams(t, d, 3)

		phases, _ := loadGateState(t, m, ctx)
		verdict, err := m.phaseSchedulable(ctx, target, phases, map[uint]bool{3: true}, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if !verdict.CanRun {
			t.Errorf("verdict.CanRun = false (%v), want true with source 3 frozen", verdict.Reasons)
		}

		// The same filter must still block when phase 3 (not 1) is
		// the one that is unfrozen.
		verdict, err = m.phaseSchedulable(ctx, target, phases, map[uint]bool{1: true}, teams)
		if err != nil {
			t.Fatalf("phaseSchedulable: %v", err)
		}
		if verdict.CanRun || len(verdict.Reasons) != 1 || !strings.Contains(verdict.Reasons[0], "seeding must be complete and frozen first") {
			t.Errorf("verdict = %+v, want the seeding blocker", verdict)
		}
	})
}

// TestPhaseSequence covers the presentation ordering: dependency
// order over SelectFrom edges, ID tie-breaks, deps outside the phase
// set, and the cycle fallback.
func TestPhaseSequence(t *testing.T) {
	ctx := context.Background()

	t.Run("dependency order with ID tie-breaks", func(t *testing.T) {
		m, d := newTestModule(t)
		for _, p := range []GamePhase{{ID: 1, Name: "one"}, {ID: 2, Name: "two"}, {ID: 3, Name: "three"}, {ID: 4, Name: "four"}} {
			createPhase(t, d, p)
		}
		// two waits on three, three waits on one, four references a
		// phase outside the set (which cannot be waited on).
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: 2, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 3, SliceExpr: "4"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: 3, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: 4, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 99, SliceExpr: "4"})

		phases, _ := loadGateState(t, m, ctx)
		seq, err := m.phaseSequence(ctx, phases)
		if err != nil {
			t.Fatalf("phaseSequence: %v", err)
		}
		got := []uint{}
		for _, p := range seq {
			got = append(got, p.ID)
		}
		want := []uint{1, 3, 2, 4}
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Errorf("sequence = %v, want %v", got, want)
		}
	})

	t.Run("no filters yields ID order", func(t *testing.T) {
		m, d := newTestModule(t)
		for _, p := range []GamePhase{{ID: 2, Name: "b"}, {ID: 1, Name: "a"}} {
			createPhase(t, d, p)
		}
		phases, _ := loadGateState(t, m, ctx)
		seq, err := m.phaseSequence(ctx, phases)
		if err != nil {
			t.Fatalf("phaseSequence: %v", err)
		}
		if len(seq) != 2 || seq[0].ID != 1 || seq[1].ID != 2 {
			t.Errorf("sequence = %v, want [1 2]", seq)
		}
	})

	t.Run("cycles fall back to ID order without hanging", func(t *testing.T) {
		m, d := newTestModule(t)
		for _, p := range []GamePhase{{ID: 1, Name: "a"}, {ID: 2, Name: "b"}} {
			createPhase(t, d, p)
		}
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: 1, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 2, SliceExpr: "4"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: 2, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 1, SliceExpr: "4"})

		phases, _ := loadGateState(t, m, ctx)
		seq, err := m.phaseSequence(ctx, phases)
		if err != nil {
			t.Fatalf("phaseSequence: %v", err)
		}
		if len(seq) != 2 || seq[0].ID != 1 || seq[1].ID != 2 {
			t.Errorf("sequence = %v, want both phases in ID order", seq)
		}
	})
}

// TestScheduleGateBlocksEndpoints drives the three generation
// endpoints for a phase whose readiness verdict is negative: each
// must render the blocked page and refuse to generate or accept, and
// each must proceed when the override parameter is present.
func TestScheduleGateBlocksEndpoints(t *testing.T) {
	m, d := newTestModule(t)
	stub := &readinessStub{name: "GateStub", ok: false, reason: "no notebook scores have been recorded yet"}
	withFilter(t, stub.name, stub)

	phase := createPhase(t, d, GamePhase{Name: "wildcard", ScheduleType: "OneShot"})
	createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: phase.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: 0, SliceExpr: "4"})
	teams := seedTeams(t, d, 4)
	ctx := context.Background()

	selectTeams := fmt.Sprintf("/ui/mod/game/schedule/phases/%d/select-teams", phase.ID)
	accept := fmt.Sprintf("/ui/mod/game/schedule/phases/%d/accept-schedule", phase.ID)

	countPlacements := func() int64 {
		t.Helper()
		n, err := gorm.G[MatchPlacement](d.DB).Count(ctx, "*")
		if err != nil {
			t.Fatalf("count placements: %v", err)
		}
		return n
	}

	t.Run("select-teams is blocked", func(t *testing.T) {
		_, body, _ := routerReq(t, m, adminContext(), "GET", selectTeams+"?st=OneShot", "")
		if !strings.Contains(body, "Schedule Generation Blocked") {
			t.Error("blocked page not rendered")
		}
		if !strings.Contains(body, "no notebook scores have been recorded yet") {
			t.Error("reason missing from blocked page")
		}
		if !strings.Contains(body, "Generate manually anyway") || !strings.Contains(body, "override=1") {
			t.Error("override escape hatch link missing from blocked page")
		}
	})

	t.Run("override unlocks the manual picker", func(t *testing.T) {
		code, body, _ := routerReq(t, m, adminContext(), "GET", selectTeams+"?st=OneShot&override=1", "")
		if code != 200 {
			t.Fatalf("code = %d, want 200", code)
		}
		if !strings.Contains(body, "Phase Schedule Generation") {
			t.Error("team picker not rendered with the override")
		}
		// The picker exists to be edited: with the override it must
		// not be inert.
		if strings.Contains(body, "inert") {
			t.Error("team picker is inert despite the override")
		}
	})

	t.Run("preview is blocked and creates nothing", func(t *testing.T) {
		_, body, _ := routerReq(t, m, adminContext(), "POST", selectTeams+"?st=OneShot", "")
		if !strings.Contains(body, "Schedule Generation Blocked") {
			t.Error("preview was not blocked")
		}
		if n := countPlacements(); n != 0 {
			t.Errorf("placements = %d, want 0", n)
		}
	})

	t.Run("accept is blocked and does not activate", func(t *testing.T) {
		_, body, _ := routerReq(t, m, adminContext(), "POST", accept, "")
		if !strings.Contains(body, "Schedule Generation Blocked") {
			t.Error("accept was not blocked")
		}
		if n := countPlacements(); n != 0 {
			t.Errorf("placements = %d, want 0", n)
		}
		stored, err := gorm.G[GamePhase](d.DB).Where(&GamePhase{ID: phase.ID}).First(ctx)
		if err != nil {
			t.Fatalf("reload phase: %v", err)
		}
		if stored.Active {
			t.Error("phase was activated by a blocked accept")
		}
	})

	t.Run("overridden accept proceeds", func(t *testing.T) {
		code, _, _ := routerReq(t, m, adminContext(), "POST", accept, "override=1")
		if code != 303 {
			t.Fatalf("code = %d, want 303 redirect", code)
		}
		stored, err := gorm.G[GamePhase](d.DB).Where(&GamePhase{ID: phase.ID}).First(ctx)
		if err != nil {
			t.Fatalf("reload phase: %v", err)
		}
		if !stored.Active {
			t.Error("overridden accept did not activate the phase")
		}
	})

	t.Run("preview carries the override to the accept form", func(t *testing.T) {
		seedFieldPosition(t, d, "Test")
		field, err := gorm.G[Field](d.DB).Where(&Field{Name: "Field 1"}).First(ctx)
		if err != nil {
			t.Fatalf("find Field 1: %v", err)
		}
		form := url.Values{}
		form.Add("fields", fmt.Sprintf("%d", field.ID))
		for _, tm := range teams {
			form.Add("selected_teams", fmt.Sprintf("%d", tm.ID))
		}
		form.Set("rounds", "1")
		form.Set("schedule_type", "OneShot")
		form.Set("override", "1")

		code, body, _ := routerReq(t, m, adminContext(), "POST", selectTeams+"?st=OneShot", form.Encode())
		if code != 200 {
			t.Fatalf("code = %d, want 200: %s", code, body)
		}
		// The accept form posts a relative URL, so the override has
		// to ride as a hidden field to survive the hop.
		if !strings.Contains(body, `name="override"`) {
			t.Error("accept form does not carry the override field")
		}
	})
}

// TestPhaseListGenerateButtonStates verifies the phase list control:
// the next-up phase with unmet parameters renders a disabled control
// carrying the reason (title + help line), a phase waiting behind it
// renders no control at all, and a ready next-up phase renders the
// normal Generate Schedule link.
func TestPhaseListGenerateButtonStates(t *testing.T) {
	// seeding (frozen) -> wildcard (next up, blocked) -> final
	// (waiting behind the wildcard, shows nothing).
	setup := func(t *testing.T, wildcardReady bool) (*Module, GamePhase, GamePhase) {
		t.Helper()
		m, d := newTestModule(t)
		stub := &readinessStub{name: "GateStub", ok: wildcardReady, reason: "no notebook scores have been recorded yet"}
		withFilter(t, stub.name, stub)

		seeding := createPhase(t, d, GamePhase{Name: "seeding", Frozen: true, ScheduleType: "OneShot"})
		wildcard := createPhase(t, d, GamePhase{Name: "wildcard", ScheduleType: "OneShot"})
		fin := createPhase(t, d, GamePhase{Name: "final", ScheduleType: "OneShot"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: wildcard.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: seeding.ID, SliceExpr: "2"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: fin.ID, Filter: stub.name, Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: wildcard.ID, SliceExpr: "2"})
		teams := seedTeams(t, d, 4)
		// Play the seeding phase so it counts as complete: an
		// incomplete source would block the wildcard for a
		// different reason than the stub's.
		playPhase(t, d, seeding.ID, teams)
		return m, wildcard, fin
	}

	t.Run("blocked next-up phase explains itself", func(t *testing.T) {
		m, wildcard, fin := setup(t, false)
		_, body, _ := routerReq(t, m, adminContext(), "GET", "/ui/mod/game/schedule/", "")

		if !strings.Contains(body, `aria-disabled="true"`) {
			t.Error("disabled control missing aria-disabled")
		}
		title := fmt.Sprintf(`title="Pick: no notebook scores have been recorded yet"`)
		if !strings.Contains(body, title) {
			t.Errorf("rollover title %q missing", title)
		}
		help := fmt.Sprintf(`<p class="help" id="blocked-%d">Pick: no notebook scores have been recorded yet</p>`, wildcard.ID)
		if !strings.Contains(body, help) {
			t.Errorf("help line %q missing", help)
		}
		// A disabled control is not a link.
		if strings.Contains(body, fmt.Sprintf("schedule/phases/%d/select-teams", wildcard.ID)) {
			t.Error("blocked phase still links to select-teams")
		}
		// A phase waiting behind the next one up shows nothing.
		if strings.Contains(body, fmt.Sprintf("schedule/phases/%d/select-teams", fin.ID)) ||
			strings.Contains(body, fmt.Sprintf(`id="blocked-%d"`, fin.ID)) {
			t.Error("phase behind the next one up rendered a control")
		}
	})

	t.Run("ready next-up phase renders the link", func(t *testing.T) {
		m, wildcard, _ := setup(t, true)
		_, body, _ := routerReq(t, m, adminContext(), "GET", "/ui/mod/game/schedule/", "")

		want := fmt.Sprintf(`href="schedule/phases/%d/select-teams?st=OneShot">Generate Schedule</a>`, wildcard.ID)
		if !strings.Contains(body, want) {
			t.Errorf("generate link %q missing", want)
		}
		if strings.Contains(body, `aria-disabled="true"`) {
			t.Error("ready phase rendered a disabled control")
		}
	})

	// A suppressed phase can never carry the Generate control, so it
	// must not consume the next-up marker: stopping at it would strand
	// every phase behind it with no control at all.
	t.Run("suppressed next-up phase passes the control on", func(t *testing.T) {
		m, d := newTestModule(t)

		seeding := createPhase(t, d, GamePhase{Name: "seeding", Frozen: true, ScheduleType: "OneShot"})
		wildcard := createPhase(t, d, GamePhase{Name: "wildcard", ScheduleType: "OneShot", Suppress: "len(Roster) < 8", SuppressMsg: "fewer than 8 teams"})
		fin := createPhase(t, d, GamePhase{Name: "final", ScheduleType: "OneShot"})
		// The final selects from the wildcard, but only when the
		// wildcard actually runs; with 4 teams it is suppressed, so
		// the final draws directly from the seeding phase.
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: fin.ID, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: wildcard.ID, SliceExpr: "2", When: "len(Roster) >= 8"})

		teams := seedTeams(t, d, 4)
		playPhase(t, d, seeding.ID, teams)

		_, body, _ := routerReq(t, m, adminContext(), "GET", "/ui/mod/game/schedule/", "")

		want := fmt.Sprintf(`href="schedule/phases/%d/select-teams?st=OneShot">Generate Schedule</a>`, fin.ID)
		if !strings.Contains(body, want) {
			t.Errorf("generate link %q missing while the next phase in sequence is suppressed", want)
		}
		if !strings.Contains(body, "fewer than 8 teams") {
			t.Error("suppressed phase lost its suppression message")
		}
	})

	// The skip is a continue, not a break, so a whole run of
	// suppressed phases is walked past until a phase that can carry
	// the control is found.
	t.Run("run of suppressed phases passes the control on", func(t *testing.T) {
		m, d := newTestModule(t)

		seeding := createPhase(t, d, GamePhase{Name: "seeding", Frozen: true, ScheduleType: "OneShot"})
		createPhase(t, d, GamePhase{Name: "wildcard", ScheduleType: "OneShot", Suppress: "len(Roster) < 8"})
		createPhase(t, d, GamePhase{Name: "last-chance", ScheduleType: "OneShot", Suppress: "len(Roster) < 16"})
		fin := createPhase(t, d, GamePhase{Name: "final", ScheduleType: "OneShot"})
		createGateFilter(t, d, GamePhaseAdvancementFilter{GamePhaseID: fin.ID, Filter: "ScoreboardRanking", Rule: "Pick", Mode: GamePhaseAdvancementFilterModeInclude, SelectFrom: seeding.ID, SliceExpr: "2"})

		teams := seedTeams(t, d, 4)
		playPhase(t, d, seeding.ID, teams)

		_, body, _ := routerReq(t, m, adminContext(), "GET", "/ui/mod/game/schedule/", "")

		want := fmt.Sprintf(`href="schedule/phases/%d/select-teams?st=OneShot">Generate Schedule</a>`, fin.ID)
		if !strings.Contains(body, want) {
			t.Errorf("generate link %q missing behind two suppressed phases", want)
		}
	})
}
