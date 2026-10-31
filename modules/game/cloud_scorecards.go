package game

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/web"
)

// stateOption is a single entry in the match state facet.
type stateOption struct {
	Value uint
	Name  string
}

// contestOption is a single entry in the contest facet.
type contestOption struct {
	ID   uint
	Name string
}

// cloudScorecardRow is one table row on the cloud scorecards page: a
// pushed placement plus its pre-computed state, contest, and phase
// names. Scored keys the Score column on the state rather than on the
// stored number, because a pushed 0 cannot tell an unscored match from
// a match that legitimately scored zero.
type cloudScorecardRow struct {
	Placement   CloudMatchPlacement
	StateName   string
	ContestName string
	PhaseName   string
	Scored      bool
}

// contestRow is a minimal local view of the contest module's Contest
// table. The game module reads it directly by table name rather than
// importing the contest module, because the contest module already
// depends on the game module and a reverse import would create a
// dependency cycle.
type contestRow struct {
	ID     uint
	Name   string
	Frozen bool
}

func (contestRow) TableName() string { return "contests" }

// uiViewCloudScorecards renders the cloud scorecards-by-team lookup
// page with server-side search, contest/state facet filters, and
// pagination, mirroring the team list page's filter mechanism. The
// page is open to any authenticated user.
func (m *Module) uiViewCloudScorecards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	q := strings.TrimSpace(r.URL.Query().Get("q"))
	contestID := m.ws.StrToUint(r.URL.Query().Get("contest_id"))
	state := m.ws.StrToUint(r.URL.Query().Get("state"))

	// Compose the active filter predicates once so the total (Count) and the
	// paged rows (Find) are always derived from the same constraint set.
	var (
		cond string
		args []interface{}
	)
	if q != "" {
		p := "%" + q + "%"
		cond = "(team_name LIKE ? OR CAST(team_id AS TEXT) LIKE ?)"
		args = []interface{}{p, p}
	}
	if contestID != 0 {
		if cond != "" {
			cond += " AND "
		}
		cond += "contest_id = ?"
		args = append(args, contestID)
	}
	if state != 0 {
		if cond != "" {
			cond += " AND "
		}
		cond += "state = ?"
		args = append(args, state)
	}

	// gorm's generic G[T] re-sessions with NewDB, so WHERE/LIMIT/OFFSET applied
	// to a raw *gorm.DB before G[T] are dropped. Apply the filters through the
	// generic chain (ChainInterface) so they survive; Count and Find share it.
	// The no-op Scopes downgrades the builder to the chain interface.
	base := gorm.G[CloudMatchPlacement](m.db.DB).Scopes(func(*gorm.Statement) {})
	if cond != "" {
		base = base.Where(cond, args...)
	}

	total, err := base.Count(ctx, "*")
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	page, pageSize := web.PageParams(r)
	placements, err := base.
		Order("contest_id, phase_id, round, match, field_id, position_id").
		Offset((page - 1) * pageSize).
		Limit(pageSize).
		Find(ctx)
	if err != nil {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	contestNames := m.cloudContestNames(ctx)
	phaseNames := m.cloudPhaseNames(ctx)
	out := make([]cloudScorecardRow, 0, len(placements))
	for _, p := range placements {
		out = append(out, cloudScorecardRow{
			Placement:   p,
			StateName:   MatchState(p.State).String(),
			ContestName: contestName(contestNames, p.ContestID),
			PhaseName:   phaseName(phaseNames, p.PhaseID),
			Scored:      MatchState(p.State).Scored(),
		})
	}

	states := make([]stateOption, 0, len(MatchStates))
	for _, s := range MatchStates {
		states = append(states, stateOption{Value: uint(s), Name: s.String()})
	}

	pages := 0
	if total > 0 {
		pages = int((total + int64(pageSize) - 1) / int64(pageSize))
	}
	window := pageWindow(page, pages)

	// The filter form drops "page" (resets to page 1); pager links re-append
	// it. Preserve the other query params on the pager links via filtersQS.
	fq := r.URL.Query()
	fq.Del("page")

	prevPage, nextPage := page-1, page+1
	m.ws.DoTemplate(w, r, "views/game/cloud_scorecards.p2", pongo2.Context{
		"rows":      out,
		"contests":  m.cloudContestOptions(ctx),
		"states":      states,
		"q":           q,
		"contestID":   contestID,
		"state":       state,
		"filtersQS":   fq.Encode(),
		"total":       total,
		"page":        page,
		"pages":       pages,
		"pageSize":    pageSize,
		"prevPage":    prevPage,
		"nextPage":    nextPage,
		"pageWindow":  window,
	})
}

// uiViewCloudScorecard renders a single pushed scorecard read-only,
// reusing the onsite scorecard template. The cloud stores only the
// site's own IDs, so the MatchInfo the template expects is synthesized
// from the placement with best-effort name lookups against the
// cloud's own game design; a miss falls back to an ID-based label.
func (m *Module) uiViewCloudScorecard(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()
	id := m.ws.StrToUint(chi.URLParam(r, "id"))

	p, err := gorm.G[CloudMatchPlacement](m.db.DB).
		Where(&CloudMatchPlacement{ID: id}).
		First(ctx)
	if err != nil {
		slog.Error("Error retrieving cloud scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	phaseName := fmt.Sprintf("Phase %d", p.PhaseID)
	if p.PhaseID != 0 {
		if phase, err := gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: p.PhaseID}).First(ctx); err == nil {
			phaseName = phase.Name
		}
	}
	fieldName := cloudFieldName(p)
	posName := fmt.Sprintf("Position %d", p.PositionID)
	posColor2 := "cccccc"
	if p.PositionID != 0 {
		if pos, err := gorm.G[FieldPosition](m.db.DB).Where(&FieldPosition{ID: p.PositionID}).First(ctx); err == nil {
			posName = pos.Name
			if pos.Color2 != "" {
				posColor2 = pos.Color2
			}
		}
	}

	info := MatchPlacement{
		Match:    p.Match,
		Phase:    GamePhase{Name: phaseName},
		Team:     team.Team{Name: p.TeamName},
		Field:    Field{Name: fieldName},
		Position: FieldPosition{Name: posName, Color2: posColor2},
	}

	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Find(ctx)
	if err != nil {
		slog.Error("Error retrieving cloud scorecard elements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	values, err := gorm.G[CloudScorecardValue](m.db.DB).
		Where(&CloudScorecardValue{CloudMatchPlacementID: p.ID}).
		Find(ctx)
	if err != nil {
		slog.Error("Error retrieving cloud scorecard values", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	scd := make(map[string]int, len(values))
	for _, v := range values {
		scd[v.Element] = v.Value
	}

	m.ws.DoTemplate(w, r, "views/game/scorecard.p2", pongo2.Context{
		"MatchInfo":     info,
		"Elements":      elements,
		"ScorecardData": scd,
		"edit_disable":  true,
	})
}

// cloudContestNames returns a best-effort map of contest ID to name
// from the contests table (a missing table, e.g. in tests, yields an
// empty map).
func (m *Module) cloudContestNames(ctx context.Context) map[uint]string {
	names := make(map[uint]string)
	if rows, err := gorm.G[contestRow](m.db.DB).Find(ctx); err == nil {
		for _, row := range rows {
			names[row.ID] = row.Name
		}
	}
	return names
}

// cloudPhaseNames returns a best-effort map of phase ID to name from
// the cloud's own game design.  The cloud stores only the site's phase
// ID, so a phase the cloud does not have falls back to "Phase <id>".
func (m *Module) cloudPhaseNames(ctx context.Context) map[uint]string {
	names := make(map[uint]string)
	if rows, err := gorm.G[GamePhase](m.db.DB).Find(ctx); err == nil {
		for _, row := range rows {
			names[row.ID] = row.Name
		}
	}
	return names
}

// phaseName resolves a phase ID to its display name, falling back to
// "Phase <id>" when the cloud's game design has no such phase.
func phaseName(names map[uint]string, id uint) string {
	if name, ok := names[id]; ok {
		return name
	}
	return fmt.Sprintf("Phase %d", id)
}

// cloudFieldName returns the display name of a pushed placement's
// field: the name the site pushed, or "Field <id>" for a push from an
// older site that did not send one.  The cloud's own Field rows are
// not consulted, because fields are created per site and the cloud's
// IDs do not correspond to a site's.
func cloudFieldName(p CloudMatchPlacement) string {
	if p.FieldName != "" {
		return p.FieldName
	}
	return fmt.Sprintf("Field %d", p.FieldID)
}

// contestName resolves a contest ID to its display name, falling back
// to "Contest <id>" when the contests table has no row for it.
func contestName(names map[uint]string, id uint) string {
	if name, ok := names[id]; ok {
		return name
	}
	return fmt.Sprintf("Contest %d", id)
}

// cloudContestOptions returns the contest facet options: the distinct
// contest IDs present in the stored placements, with best-effort names
// from the contests table (a missing table, e.g. in tests, falls back
// to "Contest <id>").
func (m *Module) cloudContestOptions(ctx context.Context) []contestOption {
	type idRow struct {
		ContestID uint `gorm:"column:contest_id"`
	}
	var idRows []idRow
	if err := m.db.Model(&CloudMatchPlacement{}).
		Select("contest_id").
		Distinct("contest_id").
		Order("contest_id ASC").
		Find(&idRows).Error; err != nil {
		return nil
	}

	names := m.cloudContestNames(ctx)
	out := make([]contestOption, 0, len(idRows))
	for _, row := range idRows {
		out = append(out, contestOption{ID: row.ContestID, Name: contestName(names, row.ContestID)})
	}
	return out
}

// pageWindow returns the page numbers to display as links in the pager,
// centered on page and clamped to [1, pages]. At most (radius*2 + 1)
// numbers are shown; the ellipsis logic is left to the template which
// only renders the window we hand it.
func pageWindow(page, pages int) []int {
	if pages <= 0 {
		return nil
	}
	const radius = 2
	start := page - radius
	if start < 1 {
		start = 1
	}
	end := page + radius
	if end > pages {
		end = pages
	}
	out := make([]int, 0, end-start+1)
	for n := start; n <= end; n++ {
		out = append(out, n)
	}
	return out
}
