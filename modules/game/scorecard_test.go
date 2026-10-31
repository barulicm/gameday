package game

import (
	"encoding/json"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"testing"

	"github.com/gizmo-platform/gameday/pkg/db"
)

// seedScorecardPlacement creates a match placement in the given state
// for the team, on the shared test phase/field/position.
func seedScorecardPlacement(t *testing.T, d *db.DB, phaseID, fieldID, positionID, teamID uint, state MatchState) MatchPlacement {
	t.Helper()
	mp := MatchPlacement{
		Match:      int(state),
		PhaseID:    phaseID,
		FieldID:    fieldID,
		PositionID: positionID,
		TeamID:     teamID,
		State:      state,
	}
	if err := d.Create(&mp).Error; err != nil {
		t.Fatalf("create placement: %v", err)
	}
	return mp
}

// seedScorecardScore records the finalized score for a placement, as a
// submitted scorecard would.
func seedScorecardScore(t *testing.T, d *db.DB, mp MatchPlacement, score int) {
	t.Helper()
	ms := MatchScore{
		ID:               mp.ID,
		MatchPlacementID: mp.ID,
		GamePhaseID:      mp.PhaseID,
		TeamID:           mp.TeamID,
		Score:            score,
	}
	if err := d.Create(&ms).Error; err != nil {
		t.Fatalf("create score: %v", err)
	}
}

// scorecardFilterCookie builds the query-escaped JSON cookie the
// scorecard list page stores for saved filters.
func scorecardFilterCookie(t *testing.T, filter map[string][]uint) string {
	t.Helper()
	j, err := json.Marshal(filter)
	if err != nil {
		t.Fatalf("marshal filter: %v", err)
	}
	return url.QueryEscape(string(j))
}

// TestScorecardListFilterCookieDoesNotCorruptStates reproduces the
// long-running-server bug where the state checkboxes on the scorecard
// filter form progressively lost options. Saving filters writes a JSON
// cookie; unmarshalling it into a filter that aliased the package-level
// MatchStates slice overwrote that global in place, permanently losing
// states for every later request.
func TestScorecardListFilterCookieDoesNotCorruptStates(t *testing.T) {
	m, d := newTestModule(t)

	phase := GamePhase{Name: "seeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field := Field{Name: "Filter Field"}
	if err := d.Create(&field).Error; err != nil {
		t.Fatalf("create field: %v", err)
	}
	position := FieldPosition{Name: "Filter Position"}
	if err := d.Create(&position).Error; err != nil {
		t.Fatalf("create position: %v", err)
	}
	teams := seedTeams(t, d, 2)

	seedScorecardPlacement(t, d, phase.ID, field.ID, position.ID, teams[0].ID, MatchStateComplete)
	seedScorecardPlacement(t, d, phase.ID, field.ID, position.ID, teams[1].ID, MatchStateScheduled)

	cookie := scorecardFilterCookie(t, map[string][]uint{
		"Phases":    {phase.ID},
		"Fields":    {field.ID},
		"Positions": {position.ID},
		"States":    {uint(MatchStateComplete)},
	})

	code, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/", "", "Cookie", "scorecard_filters="+cookie)
	if code != http.StatusOK {
		t.Fatalf("scorecard list = %d, want 200: %s", code, body)
	}

	// The saved filter applies: only the Complete match is listed.
	if !strings.Contains(body, teams[0].Name) {
		t.Errorf("complete-state team %q missing from filtered list", teams[0].Name)
	}
	if strings.Contains(body, teams[1].Name) {
		t.Errorf("scheduled-state team %q present despite state filter", teams[1].Name)
	}

	// The global state list must survive the cookie unmarshal intact:
	// every state present exactly once, in canonical order.
	if !slices.Equal(MatchStates, []MatchState{
		MatchStateScheduled,
		MatchStateQueued,
		MatchStateRunning,
		MatchStateScorable,
		MatchStateComplete,
		MatchStateNoShow,
		MatchStateDisqualified,
	}) {
		t.Errorf("MatchStates corrupted by filter cookie: %v", MatchStates)
	}

	// The filter form must still offer every state option.
	if got := strings.Count(body, `name="state"`); got != 7 {
		t.Errorf("state checkboxes = %d, want 7 (states lost from the filter form)", got)
	}
	for _, label := range []string{"Scheduled", "Queued", "Running", "Scorable", "Complete", "No-show", "Disqualified"} {
		if !strings.Contains(body, label) {
			t.Errorf("state option %q missing from filter form", label)
		}
	}

	// A follow-up request with no cookie falls back to all states.
	_, body, _ = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/", "")
	for _, tm := range teams {
		if !strings.Contains(body, tm.Name) {
			t.Errorf("team %q missing from unfiltered list", tm.Name)
		}
	}

	// Placements in the state the cookie dropped remain reachable.
	var count int64
	if err := d.Model(&MatchPlacement{}).Where("state = ?", MatchStateScheduled).Count(&count).Error; err != nil {
		t.Fatalf("count scheduled: %v", err)
	}
	if count != 1 {
		t.Errorf("scheduled placements = %d, want 1", count)
	}
}

// TestScorecardListClearFilters verifies the clear action drops the
// cookie and restores the full unfiltered view.
func TestScorecardListClearFilters(t *testing.T) {
	m, d := newTestModule(t)

	phase := GamePhase{Name: "seeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field := Field{Name: "Clear Field"}
	if err := d.Create(&field).Error; err != nil {
		t.Fatalf("create field: %v", err)
	}
	position := FieldPosition{Name: "Clear Position"}
	if err := d.Create(&position).Error; err != nil {
		t.Fatalf("create position: %v", err)
	}
	teams := seedTeams(t, d, 2)
	seedScorecardPlacement(t, d, phase.ID, field.ID, position.ID, teams[0].ID, MatchStateComplete)
	seedScorecardPlacement(t, d, phase.ID, field.ID, position.ID, teams[1].ID, MatchStateScheduled)

	// A filter cookie scoped to a phase this contest has no matches in
	// hides everything; clearing must restore them.
	cookie := scorecardFilterCookie(t, map[string][]uint{
		"Phases":    {99999},
		"Fields":    {field.ID},
		"Positions": {position.ID},
		"States":    {uint(MatchStateComplete)},
	})
	_, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/", "", "Cookie", "scorecard_filters="+cookie)
	if strings.Contains(body, teams[0].Name) {
		t.Fatal("mismatched-phase filter did not hide teams")
	}

	code, body, hdr := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/?filter_action=clear", "", "Cookie", "scorecard_filters="+cookie)
	if code != http.StatusOK {
		t.Fatalf("clear = %d, want 200: %s", code, body)
	}
	if !strings.Contains(strings.Join(hdr.Values("Set-Cookie"), ";"), "MaxAge=0") &&
		!strings.Contains(strings.Join(hdr.Values("Set-Cookie"), ";"), "Expires=") {
		t.Errorf("clear did not expire the cookie: %v", hdr.Values("Set-Cookie"))
	}
	for _, tm := range teams {
		if !strings.Contains(body, tm.Name) {
			t.Errorf("team %q missing after clearing filters", tm.Name)
		}
	}
}

// scorecardRow returns the rendered table row containing the team name,
// so a test can assert which score landed in which row.
func scorecardRow(t *testing.T, body, teamName string) string {
	t.Helper()
	for _, chunk := range strings.Split(body, "<tr>") {
		if strings.Contains(chunk, teamName) && strings.Contains(chunk, "</tr>") {
			return chunk[:strings.Index(chunk, "</tr>")]
		}
	}
	t.Fatalf("no table row for %q", teamName)
	return ""
}

// TestScorecardListShowsScores verifies the onsite list shows each
// scorecard's finalized score, like the cloud lookup page does, and a
// dash for matches that have not been settled yet. The dash is keyed on
// the placement's match state via MatchState.Scored(), not on whether a
// MatchScore row exists: only the terminal states Complete, NoShow and
// Disqualified are scored, so the seeded Scorable placement renders the
// placeholder even though its stored score is zero.
func TestScorecardListShowsScores(t *testing.T) {
	m, d := newTestModule(t)

	phase := GamePhase{Name: "seeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field := Field{Name: "Score Field"}
	if err := d.Create(&field).Error; err != nil {
		t.Fatalf("create field: %v", err)
	}
	position := FieldPosition{Name: "Score Position"}
	if err := d.Create(&position).Error; err != nil {
		t.Fatalf("create position: %v", err)
	}
	teams := seedTeams(t, d, 2)

	scored := seedScorecardPlacement(t, d, phase.ID, field.ID, position.ID, teams[0].ID, MatchStateComplete)
	seedScorecardScore(t, d, scored, 42)
	seedScorecardPlacement(t, d, phase.ID, field.ID, position.ID, teams[1].ID, MatchStateScorable)

	_, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/", "")
	if !strings.Contains(body, "<th>Score</th>") {
		t.Error("Score column header missing from scorecard list")
	}

	if row := scorecardRow(t, body, teams[0].Name); !strings.Contains(row, "<td>42</td>") {
		t.Errorf("scored row missing score: %s", row)
	}
	if row := scorecardRow(t, body, teams[1].Name); !strings.Contains(row, "&mdash;") {
		t.Errorf("unscored row missing dash placeholder: %s", row)
	}
	if row := scorecardRow(t, body, teams[1].Name); strings.Contains(row, "<td>0</td>") {
		t.Errorf("Scorable placement rendered a bare zero instead of the dash: %s", row)
	}
}

// bandColorRow builds a row for the banding tests.
func bandColorRow(positionID uint, color1, color2 string) scorecardListRow {
	return scorecardListRow{Placement: MatchPlacement{
		PositionID: positionID,
		Position:   FieldPosition{ID: positionID, Color1: color1, Color2: color2},
	}}
}

// TestBandScorecardRows covers the banding rule directly: the alternate
// color only appears where a row sits directly below another row of the
// same position.
func TestBandScorecardRows(t *testing.T) {
	t.Run("two positions never band", func(t *testing.T) {
		// match1 A, match1 B, match2 A, match2 B
		rows := []scorecardListRow{
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(2, "cccccc", "dddddd"),
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(2, "cccccc", "dddddd"),
		}
		bandScorecardRows(rows)
		want := []string{"aaaaaa", "cccccc", "aaaaaa", "cccccc"}
		for i, w := range want {
			if rows[i].BandColor != w {
				t.Errorf("row %d = %q, want %q", i, rows[i].BandColor, w)
			}
		}
	})

	t.Run("single position bands", func(t *testing.T) {
		rows := []scorecardListRow{
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(1, "aaaaaa", "bbbbbb"),
		}
		bandScorecardRows(rows)
		want := []string{"aaaaaa", "bbbbbb", "aaaaaa", "bbbbbb"}
		for i, w := range want {
			if rows[i].BandColor != w {
				t.Errorf("row %d = %q, want %q", i, rows[i].BandColor, w)
			}
		}
	})

	t.Run("a new position group restarts on the primary color", func(t *testing.T) {
		rows := []scorecardListRow{
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(1, "aaaaaa", "bbbbbb"),
			bandColorRow(2, "cccccc", "dddddd"),
		}
		bandScorecardRows(rows)
		want := []string{"aaaaaa", "bbbbbb", "cccccc"}
		for i, w := range want {
			if rows[i].BandColor != w {
				t.Errorf("row %d = %q, want %q", i, rows[i].BandColor, w)
			}
		}
	})

	t.Run("empty list", func(t *testing.T) {
		bandScorecardRows(nil)
	})
}

// TestScorecardListBanding verifies the rendered Position cells use the
// adjacent-aware banding: with both positions shown every cell keeps its
// primary color, and narrowing to one position bands its rows.
func TestScorecardListBanding(t *testing.T) {
	m, d := newTestModule(t)

	phase := GamePhase{Name: "seeding"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	field := Field{Name: "Banding Field"}
	if err := d.Create(&field).Error; err != nil {
		t.Fatalf("create field: %v", err)
	}
	left := FieldPosition{Name: "Left", Color1: "aabbcc", Color2: "112233"}
	right := FieldPosition{Name: "Right", Color1: "ddeeff", Color2: "445566"}
	if err := d.Create(&left).Error; err != nil {
		t.Fatalf("create left position: %v", err)
	}
	if err := d.Create(&right).Error; err != nil {
		t.Fatalf("create right position: %v", err)
	}
	teams := seedTeams(t, d, 4)

	// Matches 1 and 2, each on both positions.
	for i, match := range []MatchState{MatchStateScheduled, MatchStateComplete} {
		for j, positionID := range []uint{left.ID, right.ID} {
			mp := MatchPlacement{
				Match:      i + 1,
				PhaseID:    phase.ID,
				FieldID:    field.ID,
				PositionID: positionID,
				TeamID:     teams[i*2+j].ID,
				State:      match,
			}
			if err := d.Create(&mp).Error; err != nil {
				t.Fatalf("create placement: %v", err)
			}
		}
	}

	_, body, _ := routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/", "")
	for _, alt := range []string{"#112233", "#445566"} {
		if strings.Contains(body, alt) {
			t.Errorf("alternate band color %s used while both positions are shown", alt)
		}
	}
	for _, primary := range []string{"#aabbcc", "#ddeeff"} {
		if !strings.Contains(body, primary) {
			t.Errorf("primary color %s missing", primary)
		}
	}

	// Narrow to a single position: its stacked rows must now band.
	cookie := scorecardFilterCookie(t, map[string][]uint{
		"Phases":    {phase.ID},
		"Fields":    {field.ID},
		"Positions": {left.ID},
		"States":    {uint(MatchStateScheduled), uint(MatchStateComplete)},
	})
	_, body, _ = routerReq(t, m, adminContext(), http.MethodGet, "/ui/mod/game/scorecard/", "", "Cookie", "scorecard_filters="+cookie)
	if n := strings.Count(body, "background-color: #aabbcc"); n != 1 {
		t.Errorf("primary band count = %d, want 1:\n%s", n, body)
	}
	if n := strings.Count(body, "background-color: #112233"); n != 1 {
		t.Errorf("alternate band count = %d, want 1:\n%s", n, body)
	}
	if strings.Contains(body, "#ddeeff") {
		t.Error("filtered-out position still rendered a band")
	}
}
