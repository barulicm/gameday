package best

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"testing"

	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func newTestModule(t *testing.T) (*Module, *db.DB) {
	t.Helper()

	t.Setenv("GAMEDAY_DB", filepath.Join(t.TempDir(), "test.db"))

	d, err := db.New()
	if err != nil {
		t.Fatalf("db.New: %v", err)
	}

	ws, err := web.NewServer(web.WithDB(d))
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

	deps := modules.ModuleDeps{"team": tm}
	gm := game.New("onsite", d, ws, deps)
	if gm == nil {
		t.Fatal("game.New failed")
	}
	if err := gm.Migrate(); err != nil {
		t.Fatalf("game Migrate: %v", err)
	}
	deps["game"] = gm

	m := New("onsite", d, ws, deps)
	if m == nil {
		t.Fatal("best.New failed")
	}
	if err := m.Migrate(); err != nil {
		t.Fatalf("best Migrate: %v", err)
	}
	ws.AddTemplateLoader(m.TemplateLoader())
	m.NavList("/ui/mod/best")

	tm2 := team.Team{Name: "Test Team", Number: 42}
	if err := d.Create(&tm2).Error; err != nil {
		t.Fatalf("create team: %v", err)
	}

	return m, d
}

func teamByNumber(t *testing.T, d *db.DB) team.Team {
	t.Helper()

	tm, err := gorm.G[team.Team](d.DB).Where(&team.Team{Number: 42}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup team: %v", err)
	}
	return tm
}

func scoreTypeForKey(t *testing.T, d *db.DB, key string) ScoreType {
	t.Helper()

	st, err := gorm.G[ScoreType](d.DB).Where(&ScoreType{Key: key}).First(context.Background())
	if err != nil {
		t.Fatalf("lookup score type %q: %v", key, err)
	}
	return st
}

func countValues(t *testing.T, d *db.DB) int64 {
	t.Helper()

	var count int64
	if err := d.Model(&TeamScoreValue{}).Count(&count).Error; err != nil {
		t.Fatalf("count team score values: %v", err)
	}
	return count
}

func postScore(t *testing.T, m *Module, id, field, value string) *httptest.ResponseRecorder {
	t.Helper()

	rctx := chi.NewRouteContext()
	rctx.URLParams.Add("id", id)
	rctx.URLParams.Add("field", field)

	req := httptest.NewRequest("POST", "http://test.ui/ui/mod/best/scores/"+id+"/"+field,
		strings.NewReader(url.Values{"value": []string{value}}.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = req.WithContext(context.WithValue(req.Context(), chi.RouteCtxKey, rctx))

	w := httptest.NewRecorder()
	m.uiViewScoreSet(w, req)
	return w
}

func TestMigrateSeedsScoreTypes(t *testing.T) {
	m, d := newTestModule(t)

	types, err := gorm.G[ScoreType](d.DB).Order("`order` ASC").Find(context.Background())
	if err != nil {
		t.Fatalf("find score types: %v", err)
	}

	want := []struct {
		key   string
		max   float32
		order int
	}{
		{"notebook", 300, 1},
		{"marketing", 250, 2},
		{"poster", 100, 3},
		{"video", 100, 4},
	}

	if len(types) != len(want) {
		t.Fatalf("expected %d score types, got %d", len(want), len(types))
	}
	for i, w := range want {
		if types[i].Key != w.key || types[i].Max != w.max || types[i].Order != w.order {
			t.Errorf("type %d: got %+v, want key=%s max=%v order=%d", i, types[i], w.key, w.max, w.order)
		}
	}

	// Migrate again: must be idempotent
	if err := m.Migrate(); err != nil {
		t.Fatalf("second Migrate: %v", err)
	}
	types, err = gorm.G[ScoreType](d.DB).Order("`order` ASC").Find(context.Background())
	if err != nil {
		t.Fatalf("find score types after re-migrate: %v", err)
	}
	if len(types) != len(want) {
		t.Fatalf("after re-migrate: expected %d score types, got %d", len(want), len(types))
	}
}

func TestScoreSetUpsert(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	idStr := fmt.Sprintf("%d", tm.ID)
	st := scoreTypeForKey(t, d, "notebook")

	// Initial create
	w := postScore(t, m, idStr, "notebook", "150.5")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "?saved=") {
		t.Errorf("redirect Location missing ?saved=: %q", loc)
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected 1 value row, got %d", got)
	}
	v, err := gorm.G[TeamScoreValue](d.DB).
		Where(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID}).
		First(context.Background())
	if err != nil {
		t.Fatalf("lookup value: %v", err)
	}
	if v.Value != 150.5 {
		t.Errorf("expected value 150.5, got %v", v.Value)
	}

	// Second post must upsert (not insert)
	w = postScore(t, m, idStr, "notebook", "200")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on upsert, got %d: %s", w.Code, w.Body.String())
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected still 1 value row after upsert, got %d", got)
	}
	v, err = gorm.G[TeamScoreValue](d.DB).
		Where(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID}).
		First(context.Background())
	if err != nil {
		t.Fatalf("lookup value after upsert: %v", err)
	}
	if v.Value != 200 {
		t.Errorf("expected value 200 after upsert, got %v", v.Value)
	}
}

func TestScoreSetClear(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	st := scoreTypeForKey(t, d, "notebook")

	seed := TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID, Value: 10}
	if err := d.Create(&seed).Error; err != nil {
		t.Fatalf("seed value: %v", err)
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected 1 seeded value row, got %d", got)
	}

	w := postScore(t, m, fmt.Sprintf("%d", tm.ID), "notebook", "")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303 on clear, got %d: %s", w.Code, w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected 0 value rows after clear, got %d", got)
	}
}

func TestScoreSetOutOfRange(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	idStr := fmt.Sprintf("%d", tm.ID)

	w := postScore(t, m, idStr, "notebook", "99999")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error page, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted for out-of-range value, got %d", got)
	}

	// Negative values are also rejected
	w = postScore(t, m, idStr, "notebook", "-5")
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page for negative value, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted for negative value, got %d", got)
	}
}

func TestScoreSetUnknownField(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)

	w := postScore(t, m, fmt.Sprintf("%d", tm.ID), "bogus", "10")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error page, got %d", w.Code)
	}
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted for unknown field, got %d", got)
	}
}

func TestScoresRender(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	st := scoreTypeForKey(t, d, "notebook")
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID, Value: 150.5}).Error; err != nil {
		t.Fatalf("seed value: %v", err)
	}

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/scores/", nil)
	w := httptest.NewRecorder()
	m.uiViewScores(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	for _, want := range []string{
		"/static/css/scores.css",
		"Test Team",
		"<th class=\"has-text-right\">Total</th>",
		"value=\"150.50\"",
		"Total",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered page missing %q", want)
		}
	}

	// One empty cell must render the em-dash placeholder
	if !strings.Contains(body, `placeholder="—"`) {
		t.Errorf("rendered page missing empty-cell placeholder")
	}

	if got := strings.Count(body, "score-input"); got != 4 {
		t.Errorf("expected 4 score inputs (one per score type), got %d", got)
	}

	// Server-computed form actions must be well-formed (no double slash)
	if strings.Contains(body, "action=\"//") || strings.Contains(body, "scores//") {
		t.Errorf("rendered page contains a double-slash form action")
	}
	if !strings.Contains(body, fmt.Sprintf("action=\"/ui/mod/best/scores/%d/notebook\"", tm.ID)) {
		t.Errorf("rendered page missing expected form action for team %d", tm.ID)
	}
}

func TestDebugGenerateScores(t *testing.T) {
	m, d := newTestModule(t)

	if err := m.DebugGenerateScores(); err != nil {
		t.Fatalf("DebugGenerateScores: %v", err)
	}

	tm := teamByNumber(t, d)
	types, err := gorm.G[ScoreType](d.DB).Find(context.Background())
	if err != nil {
		t.Fatalf("find score types: %v", err)
	}

	if got := countValues(t, d); got != int64(len(types)) {
		t.Fatalf("expected %d generated value rows, got %d", len(types), got)
	}

	for _, st := range types {
		v, err := gorm.G[TeamScoreValue](d.DB).
			Where(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: st.ID}).
			First(context.Background())
		if err != nil {
			t.Fatalf("missing generated value for type %s: %v", st.Key, err)
		}
		if v.Value < 0 || v.Value > st.Max {
			t.Errorf("generated value %v for type %s out of range [0, %v]", v.Value, st.Key, st.Max)
		}
	}

	// Running again must replace, not duplicate
	if err := m.DebugGenerateScores(); err != nil {
		t.Fatalf("second DebugGenerateScores: %v", err)
	}
	if got := countValues(t, d); got != int64(len(types)) {
		t.Fatalf("after re-generation expected %d rows, got %d", len(types), got)
	}
}

func postImport(t *testing.T, m *Module, csv string) *httptest.ResponseRecorder {
	t.Helper()

	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	part, err := mw.CreateFormFile("scores_file", "scores.csv")
	if err != nil {
		t.Fatalf("CreateFormFile: %v", err)
	}
	if _, err := part.Write([]byte(csv)); err != nil {
		t.Fatalf("write part: %v", err)
	}
	if err := mw.Close(); err != nil {
		t.Fatalf("close writer: %v", err)
	}

	req := httptest.NewRequest("POST", "http://test.ui/ui/mod/best/scores/import", &buf)
	req.Header.Set("Content-Type", mw.FormDataContentType())

	w := httptest.NewRecorder()
	m.uiViewImportScoresSubmit(w, req)
	return w
}

func valueForType(t *testing.T, d *db.DB, teamID, typeID uint) (float32, bool) {
	t.Helper()

	v, err := gorm.G[TeamScoreValue](d.DB).
		Where(&TeamScoreValue{TeamID: teamID, ScoreTypeID: typeID}).
		First(context.Background())
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false
	}
	if err != nil {
		t.Fatalf("lookup value: %v", err)
	}
	return v.Value, true
}

func TestImportScoresRender(t *testing.T) {
	m, _ := newTestModule(t)

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/scores/import", nil)
	w := httptest.NewRecorder()
	m.uiViewImportScores(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	for _, want := range []string{
		"<code>Team</code>",
		"name=\"scores_file\"",
		"enctype=\"multipart/form-data\"",
		"Team,Notebook,Marketing,Poster,Video",
		"42,75.00,62.50,25.00,25.00",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("rendered import page missing %q", want)
		}
	}
}

func TestImportScores(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	second := team.Team{Name: "Second Team", Number: 7}
	if err := d.Create(&second).Error; err != nil {
		t.Fatalf("create second team: %v", err)
	}

	nb := scoreTypeForKey(t, d, "notebook")
	mkt := scoreTypeForKey(t, d, "marketing")
	poster := scoreTypeForKey(t, d, "poster")

	// Pre-existing score that the import must not touch
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: poster.ID, Value: 99}).Error; err != nil {
		t.Fatalf("seed value: %v", err)
	}

	csv := "Team,Notebook,Marketing\n7,100,\nTest Team,200,50\n"
	w := postImport(t, m, csv)
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if loc := w.Header().Get("Location"); !strings.Contains(loc, "?imported=3") {
		t.Errorf("redirect Location missing ?imported=3: %q", loc)
	}

	// 7/Notebook = 100
	if v, ok := valueForType(t, d, second.ID, nb.ID); !ok || v != 100 {
		t.Errorf("expected 100 for team 7 notebook, got %v (present=%v)", v, ok)
	}
	// 7/Marketing empty cell must be left unset
	if _, ok := valueForType(t, d, second.ID, mkt.ID); ok {
		t.Errorf("expected no marketing value for team 7, but one exists")
	}
	// 42/Notebook = 200, 42/Marketing = 50
	if v, ok := valueForType(t, d, tm.ID, nb.ID); !ok || v != 200 {
		t.Errorf("expected 200 for test team notebook, got %v (present=%v)", v, ok)
	}
	if v, ok := valueForType(t, d, tm.ID, mkt.ID); !ok || v != 50 {
		t.Errorf("expected 50 for test team marketing, got %v (present=%v)", v, ok)
	}
	// Team 42 Poster column absent: pre-existing value must be untouched
	if v, ok := valueForType(t, d, tm.ID, poster.ID); !ok || v != 99 {
		t.Errorf("expected pre-existing 99 for test team poster to be untouched, got %v (present=%v)", v, ok)
	}
}

func TestImportScoresOverwrite(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	nb := scoreTypeForKey(t, d, "notebook")
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: nb.ID, Value: 10}).Error; err != nil {
		t.Fatalf("seed value: %v", err)
	}

	w := postImport(t, m, "Team,Notebook\n42,250\n")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if got := countValues(t, d); got != 1 {
		t.Fatalf("expected still 1 value row after overwrite, got %d", got)
	}
	if v, ok := valueForType(t, d, tm.ID, nb.ID); !ok || v != 250 {
		t.Errorf("expected 250 after overwrite, got %v (present=%v)", v, ok)
	}
}

func TestImportScoresNameMatch(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	mkt := scoreTypeForKey(t, d, "marketing")

	w := postImport(t, m, "Team,Marketing\nTest Team,123\n")
	if w.Code != http.StatusSeeOther {
		t.Fatalf("expected 303, got %d: %s", w.Code, w.Body.String())
	}
	if v, ok := valueForType(t, d, tm.ID, mkt.ID); !ok || v != 123 {
		t.Errorf("expected 123 by name match, got %v (present=%v)", v, ok)
	}
}

func TestImportScoresInvalidRows(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	nb := scoreTypeForKey(t, d, "notebook")
	mkt := scoreTypeForKey(t, d, "marketing")

	csv := "Team,Notebook,Marketing\n42,99999,40\nTest Team,300,1000\n"
	w := postImport(t, m, csv)
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error report, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	for _, want := range []string{
		"99999",
		"out of range",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("error report missing %q", want)
		}
	}

	// Row 2 (42): notebook rejected, marketing applied
	if v, ok := valueForType(t, d, tm.ID, mkt.ID); !ok || v != 40 {
		t.Errorf("expected 40 marketing from row 2, got %v (present=%v)", v, ok)
	}
	// Row 3 (Test Team): notebook 300 (at max) applied, marketing 1000 rejected.
	// Notebook being 300 (not 99999) confirms the row-2 out-of-range value was
	// skipped, and the row count confirms the row-3 marketing was skipped too.
	if v, ok := valueForType(t, d, tm.ID, nb.ID); !ok || v != 300 {
		t.Errorf("expected 300 notebook from row 3, got %v (present=%v)", v, ok)
	}
	if got := countValues(t, d); got != 2 {
		t.Fatalf("expected 2 value rows, got %d", got)
	}
}

func TestImportScoresUnknownTeam(t *testing.T) {
	m, d := newTestModule(t)

	w := postImport(t, m, "Team,Notebook\n999,10\n")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error report, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "unknown team") {
		t.Errorf("error report missing unknown team message: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows for unknown team, got %d", got)
	}
}

func TestImportScoresMissingTeamColumn(t *testing.T) {
	m, d := newTestModule(t)

	w := postImport(t, m, "Notebook,Marketing\n42,10,20\n")
	if w.Code != http.StatusOK {
		t.Fatalf("expected 200 error page, got %d: %s", w.Code, w.Body.String())
	}
	if !strings.Contains(w.Body.String(), "Internal Error") {
		t.Errorf("expected internal error page, got: %s", w.Body.String())
	}
	if got := countValues(t, d); got != 0 {
		t.Fatalf("expected no rows persisted, got %d", got)
	}
}

func scoreCells(maxes ...float32) []scoreCell {
	names := []string{"Notebook", "Marketing", "Poster", "Video"}
	cells := make([]scoreCell, 0, len(maxes))
	for i, max := range maxes {
		name := ""
		if i < len(names) {
			name = names[i]
		}
		cells = append(cells, scoreCell{Key: names[i], Name: name, Max: max})
	}
	return cells
}

func withValues(cells []scoreCell, values ...float32) []scoreCell {
	for i, v := range values {
		value := v
		cells[i].Value = &value
	}
	return cells
}

// onlyValue scores a single cell, leaving every other cell of the row blank.
func onlyValue(cells []scoreCell, idx int, v float32) []scoreCell {
	value := v
	cells[idx].Value = &value
	return cells
}

func TestLowScoreCategories(t *testing.T) {
	tcs := []struct {
		name string
		rows [][]scoreCell
		want []string
	}{
		{
			name: "no teams",
			rows: nil,
			want: nil,
		},
		{
			name: "no scores entered",
			rows: [][]scoreCell{withValues(scoreCells(300, 250))},
			want: nil,
		},
		{
			name: "every notebook score below a third",
			rows: [][]scoreCell{
				withValues(scoreCells(300, 250), 50, 100),
				withValues(scoreCells(300, 250), 90, 80),
			},
			want: []string{"Notebook"},
		},
		{
			name: "a score at exactly 33 percent clears the warning",
			rows: [][]scoreCell{
				withValues(scoreCells(300, 250), 20, 10),
				withValues(scoreCells(300, 250), 99, 5),
			},
			want: []string{"Marketing"},
		},
		{
			name: "a single zero score still warns",
			rows: [][]scoreCell{withValues(scoreCells(100), 0)},
			want: []string{"Notebook"},
		},
		{
			name: "categories judged normally are silent",
			rows: [][]scoreCell{withValues(scoreCells(100, 100), 33, 90)},
			want: nil,
		},
		{
			name: "partially scored categories warn from entered scores only",
			rows: [][]scoreCell{
				withValues(scoreCells(300, 250), 5, 0),
				withValues(scoreCells(300, 250)),
			},
			want: []string{"Notebook", "Marketing"},
		},
		{
			// Regression: the first scored cell a team has may sit in a later
			// category. Columns are keyed by index, so that cell must not
			// become the identity of the earlier categories it left blank,
			// which used to duplicate a warning and drop a real one.
			name: "a score only in a later category keeps its own column",
			rows: [][]scoreCell{
				onlyValue(scoreCells(300, 250, 100, 100), 2, 1),
				onlyValue(scoreCells(300, 250, 100, 100), 0, 200),
			},
			want: []string{"Poster"},
		},
		{
			// A category with no usable maximum can never be judged low, and
			// must not shift the categories after it out of alignment.
			name: "a category with no usable maximum is skipped in place",
			rows: [][]scoreCell{
				onlyValue(scoreCells(0, 300, 250), 1, 10),
				onlyValue(scoreCells(0, 300, 250), 2, 200),
			},
			want: []string{"Marketing"},
		},
		{
			name: "a score only in the last category still checks the rest",
			rows: [][]scoreCell{
				onlyValue(scoreCells(300, 250, 100, 100), 3, 5),
				onlyValue(scoreCells(300, 250, 100, 100), 0, 5),
			},
			want: []string{"Notebook", "Video"},
		},
	}

	for _, tc := range tcs {
		t.Run(tc.name, func(t *testing.T) {
			var rows []scoreRow
			for _, cells := range tc.rows {
				rows = append(rows, scoreRow{Cells: cells})
			}

			got := lowScoreCategories(rows)
			if len(got) != len(tc.want) {
				t.Fatalf("expected %d warnings, got %d: %+v", len(tc.want), len(got), got)
			}
			for i, w := range tc.want {
				if got[i].Name != w {
					t.Errorf("warning %d: expected %q, got %+v", i, w, got[i])
				}
			}
		})
	}
}

func TestLowScoreCategoriesCountsAndThreshold(t *testing.T) {
	rows := []scoreRow{
		{Cells: withValues(scoreCells(300), 10)},
		{Cells: withValues(scoreCells(300), 20)},
	}

	got := lowScoreCategories(rows)
	if len(got) != 1 {
		t.Fatalf("expected 1 warning, got %+v", got)
	}
	if got[0].Scored != 2 {
		t.Errorf("expected Scored 2, got %d", got[0].Scored)
	}
	if got[0].Max != 300 {
		t.Errorf("expected Max 300, got %v", got[0].Max)
	}
	if diff := got[0].Threshold - 99; diff > 0.01 && diff < -0.01 {
		t.Errorf("expected threshold near 99, got %v", got[0].Threshold)
	}
}

func TestScoresRenderLowScoreWarning(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	nb := scoreTypeForKey(t, d, "notebook")
	mkt := scoreTypeForKey(t, d, "marketing")
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: nb.ID, Value: 12}).Error; err != nil {
		t.Fatalf("seed low notebook: %v", err)
	}
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: mkt.ID, Value: 200}).Error; err != nil {
		t.Fatalf("seed healthy marketing: %v", err)
	}

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/scores/", nil)
	w := httptest.NewRecorder()
	m.uiViewScores(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	body := w.Body.String()

	if !strings.Contains(body, "notification is-warning") {
		t.Errorf("expected a warning notification, got: %s", body)
	}
	if !strings.Contains(body, "Notebook") || !strings.Contains(body, "below 33%") {
		t.Errorf("warning does not name the low notebook category: %s", body)
	}
	// Both numbers in the notification render with two decimal places, never
	// raw float32 output like "300.000000".
	if !strings.Contains(body, "possible 300.00 points") {
		t.Errorf("warning does not show the category maximum at 2dp: %s", body)
	}
	if !strings.Contains(body, "under 99.00") {
		t.Errorf("warning does not show the threshold at 2dp: %s", body)
	}
	if warning := notificationText(t, body); strings.Contains(warning, ".000000") {
		t.Errorf("warning renders unformatted floats: %s", warning)
	}
	// A category judged healthily must not be warned about.
	if strings.Contains(body, "Marketing are below") {
		t.Errorf("healthy marketing category warned about: %s", body)
	}
}

// notificationText returns just the first warning notification on the page, so
// assertions about the warning's formatting are not confused by the score
// table's own markup.
func notificationText(t *testing.T, body string) string {
	t.Helper()

	start := strings.Index(body, "notification is-warning")
	if start < 0 {
		t.Fatalf("page has no warning notification")
	}
	end := strings.Index(body[start:], "</div>")
	if end < 0 {
		t.Fatalf("warning notification is not closed")
	}
	return body[start : start+end]
}

func TestScoresRenderNoWarningWhenUnscored(t *testing.T) {
	m, d := newTestModule(t)

	tm := teamByNumber(t, d)
	nb := scoreTypeForKey(t, d, "notebook")
	if err := d.Create(&TeamScoreValue{TeamID: tm.ID, ScoreTypeID: nb.ID, Value: 250}).Error; err != nil {
		t.Fatalf("seed notebook: %v", err)
	}

	req := httptest.NewRequest("GET", "http://test.ui/ui/mod/best/scores/", nil)
	w := httptest.NewRecorder()
	m.uiViewScores(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("expected 200, got %d: %s", w.Code, w.Body.String())
	}
	if strings.Contains(w.Body.String(), "notification is-warning") {
		t.Errorf("expected no warning, got: %s", w.Body.String())
	}
}
