package game

import (
	"context"
	"fmt"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// seedScorecardElement creates a game element with two states and
// returns the element and its state IDs, so tests can build the EID_SID
// keys the scorecard values use.
func seedScorecardElement(t *testing.T, d *gorm.DB) (GameElement, uint, uint) {
	t.Helper()
	e := GameElement{EID: "widgets", Name: "Widgets", Type: "count"}
	if err := gorm.G[GameElement](d).Create(context.Background(), &e); err != nil {
		t.Fatalf("create element: %v", err)
	}
	s1 := GameElementState{GameElementID: e.ID, SID: "fabricated", Name: "Fabricated", Each: 10}
	if err := gorm.G[GameElementState](d).Create(context.Background(), &s1); err != nil {
		t.Fatalf("create state: %v", err)
	}
	s2 := GameElementState{GameElementID: e.ID, SID: "polished", Name: "Polished", Each: 5}
	if err := gorm.G[GameElementState](d).Create(context.Background(), &s2); err != nil {
		t.Fatalf("create state: %v", err)
	}
	return e, s1.ID, s2.ID
}

// seedCloudPlacement creates a cloud match placement and returns it.
func seedCloudPlacement(t *testing.T, d *gorm.DB, p CloudMatchPlacement) CloudMatchPlacement {
	t.Helper()
	if err := gorm.G[CloudMatchPlacement](d).Create(context.Background(), &p); err != nil {
		t.Fatalf("create placement: %v", err)
	}
	return p
}

// seedCloudValue creates a scorecard value for the given placement.
func seedCloudValue(t *testing.T, d *gorm.DB, placementID uint, element string, value int) {
	t.Helper()
	v := CloudScorecardValue{CloudMatchPlacementID: placementID, Element: element, Value: value}
	if err := gorm.G[CloudScorecardValue](d).Create(context.Background(), &v); err != nil {
		t.Fatalf("create value: %v", err)
	}
}

// scorecardTeamNames extracts the team name from each table row on the
// scorecards page (the Team cell is the first <td> in each row).
func scorecardTeamNames(t *testing.T, body string) []string {
	t.Helper()
	rows := scorecardRowRe.FindAllStringSubmatch(body, -1)
	out := make([]string, 0, len(rows))
	for _, r := range rows {
		out = append(out, strings.TrimSpace(r[1]))
	}
	return out
}

var scorecardRowRe = regexp.MustCompile(`<tr>\s*<td>(.*?)</td>`)

// scorecardTotal parses the "N scorecards" count line from the page.
func scorecardTotal(t *testing.T, body string) int {
	t.Helper()
	m := scorecardTotalRe.FindStringSubmatch(body)
	if m == nil {
		t.Fatalf("total count not found in body:\n%s", body)
	}
	n, err := strconv.Atoi(m[1])
	if err != nil {
		t.Fatalf("parse total count: %v", err)
	}
	return n
}

var scorecardTotalRe = regexp.MustCompile(`<p class="has-text-grey mb-2">\s*(\d+) scorecards?`)

func TestCloudScorecardsEmpty(t *testing.T) {
	m, _ := newTestModule(t)
	cm := newCloudTestModule(t, m.db, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("scorecards = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "No scorecards match") {
		t.Errorf("empty state missing:\n%s", body)
	}
	if scorecardTotal(t, body) != 0 {
		t.Errorf("total = %d, want 0", scorecardTotal(t, body))
	}
}

func TestCloudScorecardsList(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	p := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State:   uint(MatchStateComplete), Score: 150,
	})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("scorecards = %d, want 200: %s", code, body)
	}
	names := scorecardTeamNames(t, body)
	if len(names) != 1 || names[0] != "Alpha Robotics" {
		t.Fatalf("rows = %v, want [Alpha Robotics]", names)
	}
	if !strings.Contains(body, "<td>Contest 1</td>") {
		t.Error("missing contest cell Contest 1")
	}
	// Rounds are zero-indexed in storage; the display adds one, so the
	// seeded Round: 1 renders as 2.
	if !strings.Contains(body, "<td>2</td>") {
		t.Error("round not displayed one-indexed (want 2 for stored round 1)")
	}
	if !strings.Contains(body, fmt.Sprintf("/ui/mod/game/scorecards/%d\">View</a>", p.ID)) {
		t.Error("missing View link to the scorecard detail page")
	}
	if !strings.Contains(body, "<td>150</td>") {
		t.Error("Complete placement missing its pushed score 150")
	}
	if scorecardTotal(t, body) != 1 {
		t.Errorf("total = %d, want 1", scorecardTotal(t, body))
	}
}

// TestCloudScorecardsListScoreByState verifies the cloud list dashes an
// unsettled pushed placement rather than rendering the zero that every
// unscored placement carries in the push. The dash is keyed on the
// stored match state via MatchState.Scored(), the same rule the onsite
// list uses, so no extra wire field is needed to tell an unscored match
// from one that legitimately scored zero.
func TestCloudScorecardsListScoreByState(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 0, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 150,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 0, Match: 1, PhaseID: 1,
		TeamID: 102, TeamName: "Beta Robotics",
		FieldID: 1, PositionID: 2,
		State: uint(MatchStateScheduled), Score: 0,
	})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("scorecards = %d, want 200: %s", code, body)
	}

	if row := scorecardRow(t, body, "Alpha Robotics"); !strings.Contains(row, "<td>150</td>") {
		t.Errorf("Complete row missing its score: %s", row)
	}
	beta := scorecardRow(t, body, "Beta Robotics")
	if !strings.Contains(beta, "&mdash;") {
		t.Errorf("Scheduled row missing dash placeholder: %s", beta)
	}
	if strings.Contains(beta, "<td>0</td>") {
		t.Errorf("Scheduled row rendered a bare zero instead of the dash: %s", beta)
	}
}

func TestCloudScorecardDetail(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedScorecardElement(t, d.DB)
	p := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State:   uint(MatchStateComplete), Score: 150,
	})
	seedCloudValue(t, d.DB, p.ID, "widgets_fabricated", 10)

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet,
		fmt.Sprintf("/ui/mod/game/scorecards/%d", p.ID), "")
	if code != http.StatusOK {
		t.Fatalf("scorecard detail = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Alpha Robotics") {
		t.Error("team name missing from scorecard header")
	}
	if !strings.Contains(body, "Widgets") {
		t.Error("element legend Widgets missing")
	}
	if !strings.Contains(body, `value="10"`) {
		t.Error("pushed value 10 not rendered in the count input")
	}
	if !strings.Contains(body, "disabled") {
		t.Error("scorecard inputs should be disabled (read-only)")
	}
	if strings.Contains(body, "Submit") {
		t.Error("read-only scorecard should not show a Submit button")
	}
}

func TestCloudScorecardDetailNotFound(t *testing.T) {
	m, _ := newTestModule(t)
	cm := newCloudTestModule(t, m.db, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards/999999", "")
	if code != http.StatusOK {
		t.Fatalf("scorecard detail = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Internal Error") {
		t.Errorf("missing internal error page:\n%s", body)
	}
}

func TestCloudScorecardsContestName(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	if err := d.AutoMigrate(&contestRow{}); err != nil {
		t.Fatalf("migrate contest: %v", err)
	}
	c := contestRow{Name: "Spring Fling"}
	if err := d.Create(&c).Error; err != nil {
		t.Fatalf("create contest: %v", err)
	}
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: c.ID, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("scorecards = %d, want 200: %s", code, body)
	}
	if !strings.Contains(body, "Spring Fling") {
		t.Errorf("contest name missing from facet:\n%s", body)
	}
}

func TestCloudScorecardsSearch(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 2, PhaseID: 1,
		TeamID: 202, TeamName: "Beta Bots",
		FieldID: 1, PositionID: 2,
		State: uint(MatchStateComplete), Score: 20,
	})

	// Search by team name.
	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards?q=Alpha", "")
	if code != http.StatusOK {
		t.Fatalf("search = %d, want 200: %s", code, body)
	}
	names := scorecardTeamNames(t, body)
	if len(names) != 1 || names[0] != "Alpha Robotics" {
		t.Fatalf("search Alpha = %v, want [Alpha Robotics]", names)
	}
	if scorecardTotal(t, body) != 1 {
		t.Errorf("search total = %d, want 1", scorecardTotal(t, body))
	}

	// Search by team ID.
	code, body, _ = routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards?q=202", "")
	if code != http.StatusOK {
		t.Fatalf("search = %d, want 200: %s", code, body)
	}
	names = scorecardTeamNames(t, body)
	if len(names) != 1 || names[0] != "Beta Bots" {
		t.Fatalf("search 202 = %v, want [Beta Bots]", names)
	}
}

func TestCloudScorecardsContestFacet(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 2, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 202, TeamName: "Beta Bots",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 20,
	})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards?contest_id=2", "")
	if code != http.StatusOK {
		t.Fatalf("contest facet = %d, want 200: %s", code, body)
	}
	names := scorecardTeamNames(t, body)
	if len(names) != 1 || names[0] != "Beta Bots" {
		t.Fatalf("contest 2 = %v, want [Beta Bots]", names)
	}
	if scorecardTotal(t, body) != 1 {
		t.Errorf("contest total = %d, want 1", scorecardTotal(t, body))
	}
}

func TestCloudScorecardsStateFacet(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 2, PhaseID: 1,
		TeamID: 202, TeamName: "Beta Bots",
		FieldID: 1, PositionID: 2,
		State: uint(MatchStateScheduled), Score: 0,
	})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet,
		"/ui/mod/game/scorecards?state="+strconv.Itoa(int(MatchStateComplete)), "")
	if code != http.StatusOK {
		t.Fatalf("state facet = %d, want 200: %s", code, body)
	}
	names := scorecardTeamNames(t, body)
	if len(names) != 1 || names[0] != "Alpha Robotics" {
		t.Fatalf("state complete = %v, want [Alpha Robotics]", names)
	}
	if scorecardTotal(t, body) != 1 {
		t.Errorf("state total = %d, want 1", scorecardTotal(t, body))
	}
}

func TestCloudScorecardsCombinedFilters(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 2, PhaseID: 1,
		TeamID: 202, TeamName: "Alpha Bots",
		FieldID: 1, PositionID: 2,
		State: uint(MatchStateScheduled), Score: 0,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 2, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 303, TeamName: "Gamma Gears",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 30,
	})

	// q=Alpha + state=complete should match only the first placement.
	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet,
		"/ui/mod/game/scorecards?q=Alpha&state="+strconv.Itoa(int(MatchStateComplete)), "")
	if code != http.StatusOK {
		t.Fatalf("combined = %d, want 200: %s", code, body)
	}
	names := scorecardTeamNames(t, body)
	if len(names) != 1 || names[0] != "Alpha Robotics" {
		t.Fatalf("combined = %v, want [Alpha Robotics]", names)
	}
	if scorecardTotal(t, body) != 1 {
		t.Errorf("combined total = %d, want 1", scorecardTotal(t, body))
	}
}

func TestCloudScorecardsPagination(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	for i := 0; i < 12; i++ {
		seedCloudPlacement(t, d.DB, CloudMatchPlacement{
			ContestID: uint(i%2 + 1), Round: 1, Match: i + 1, PhaseID: 1,
			TeamID:  uint(i + 1), TeamName: fmt.Sprintf("Team %d", i+1),
			FieldID: 1, PositionID: uint(i%2 + 1),
			State: uint(MatchStateComplete), Score: i * 10,
		})
	}

	// Page 1: 10 rows, total 12.
	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("page 1 = %d, want 200: %s", code, body)
	}
	if names := scorecardTeamNames(t, body); len(names) != 10 {
		t.Fatalf("page 1 has %d rows, want 10", len(names))
	}
	if scorecardTotal(t, body) != 12 {
		t.Errorf("total = %d, want 12", scorecardTotal(t, body))
	}
	if !strings.Contains(body, "page=2") {
		t.Error("page 1 pager missing link to page 2")
	}

	// Page 2: the final 2 rows.
	code, body2, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards?page=2", "")
	if code != http.StatusOK {
		t.Fatalf("page 2 = %d, want 200: %s", code, body2)
	}
	if names := scorecardTeamNames(t, body2); len(names) != 2 {
		t.Fatalf("page 2 has %d rows, want 2", len(names))
	}
}

func TestCloudScorecardsStatePreserved(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	for i := 0; i < 12; i++ {
		seedCloudPlacement(t, d.DB, CloudMatchPlacement{
			ContestID: 1, Round: 1, Match: i + 1, PhaseID: 1,
			TeamID:  uint(i + 1), TeamName: fmt.Sprintf("Alpha %d", i+1),
			FieldID: 1, PositionID: 1,
			State:   uint(MatchStateComplete), Score: i * 10,
		})
	}

	// q + state match 12 placements -> 2 pages, so the pager is rendered.
	qs := "/ui/mod/game/scorecards?q=Alpha&state=" + strconv.Itoa(int(MatchStateComplete)) + "&page=2"
	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, qs, "")
	if code != http.StatusOK {
		t.Fatalf("state = %d, want 200: %s", code, body)
	}
	// The search box and state select retain their submitted values.
	if !strings.Contains(body, `value="Alpha"`) {
		t.Error("search box lost its value")
	}
	if !strings.Contains(body, `<option value="`+strconv.Itoa(int(MatchStateComplete))+`" selected`) {
		t.Error("state facet lost its selection")
	}
	// Pager links re-emit the active filters (url.Values encodes keys in
	// alphabetical order, and pongo2 HTML-escapes & in the href) but not the
	// page param (page is re-added by the link itself).
	if !strings.Contains(body, "q=Alpha&amp;state="+strconv.Itoa(int(MatchStateComplete))) {
		t.Error("pager link did not preserve q and state")
	}
}

func TestCloudScorecardsUnguarded(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 1, Match: 1, PhaseID: 1,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})

	// A profile with no permissions still sees the page.
	noPerm := context.WithValue(context.Background(), web.ProfileKey{}, web.Profile{Username: "nobody"})
	code, body, _ := routerReq(t, cm, noPerm, http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("unguarded = %d, want 200: %s", code, body)
	}
	if strings.Contains(body, "Unauthorized") {
		t.Error("unguarded page should not show the unauthorized page")
	}
	if !strings.Contains(body, "Alpha Robotics") {
		t.Errorf("unguarded page missing team row:\n%s", body)
	}
}

// TestCloudScorecardsPhaseColumn verifies the list shows each
// placement's phase name from the cloud's game design, falling back to
// "Phase <id>" for a phase the cloud does not have.
func TestCloudScorecardsPhaseColumn(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})

	phase := GamePhase{Name: "Wildcard", ScoreSummation: "Total", ScheduleType: "OneShot"}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 0, Match: 1, PhaseID: phase.ID,
		TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 10,
	})
	seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 0, Match: 2, PhaseID: 99,
		TeamID: 102, TeamName: "Beta Bots",
		FieldID: 1, PositionID: 1,
		State: uint(MatchStateComplete), Score: 20,
	})

	code, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, "/ui/mod/game/scorecards", "")
	if code != http.StatusOK {
		t.Fatalf("scorecards = %d, want 200: %s", code, body)
	}
	for _, want := range []string{"<th>Phase</th>", "<td>Wildcard</td>", "<td>Phase 99</td>"} {
		if !strings.Contains(body, want) {
			t.Errorf("body missing %q", want)
		}
	}
}

// TestCloudScorecardDetailFieldName verifies the detail page shows the
// field name the site pushed, falling back to "Field <id>" for a push
// that carried none, rather than the cloud's own Field row (whose IDs
// do not correspond to a site's fields).
func TestCloudScorecardDetailFieldName(t *testing.T) {
	m, d := newTestModule(t)
	cm := newCloudTestModule(t, d, m.ws, modules.ModuleDeps{"team": m.tm.(modules.Web)})
	seedScorecardElement(t, d.DB)

	named := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 0, Match: 1, PhaseID: 1, TeamID: 101, TeamName: "Alpha Robotics",
		FieldID: 1, FieldName: "North Arena", PositionID: 1, State: uint(MatchStateComplete),
	})
	// The cloud's own field 2 is unrelated to the site's field 2.
	if err := d.Create(&Field{ID: 2, Name: "Cloud Practice Field"}).Error; err != nil {
		t.Fatalf("create cloud field: %v", err)
	}
	unnamed := seedCloudPlacement(t, d.DB, CloudMatchPlacement{
		ContestID: 1, Round: 0, Match: 2, PhaseID: 1, TeamID: 102, TeamName: "Beta Bots",
		FieldID: 2, PositionID: 1, State: uint(MatchStateComplete),
	})

	_, body, _ := routerReq(t, cm, adminContext(), http.MethodGet, fmt.Sprintf("/ui/mod/game/scorecards/%d", named.ID), "")
	if !strings.Contains(body, "North Arena") {
		t.Error("pushed field name North Arena missing")
	}
	_, body, _ = routerReq(t, cm, adminContext(), http.MethodGet, fmt.Sprintf("/ui/mod/game/scorecards/%d", unnamed.ID), "")
	if !strings.Contains(body, "Field 2") {
		t.Error("fallback field label Field 2 missing")
	}
	if strings.Contains(body, "Cloud Practice Field") {
		t.Error("detail page used the cloud's own field 2 instead of the fallback")
	}
}
