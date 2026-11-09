package best

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"strconv"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
)

type scoreCell struct {
	Key            string
	Name           string
	Max            float32
	Action         string
	Value          *float32
	ValueFormatted string
	Saved          bool
}

type scoreRow struct {
	Team  team.Team
	Cells []scoreCell
	Total *float32
}

// lowScorePercent is the percentage of a category's maximum below which a
// category is considered suspiciously scored.
const (
	lowScorePercent      = 33
	lowScorePercentBasis = 100
)

// lowScoreWarning reports a score category in which every entered score is
// below 33% of the category maximum. Judging an entire category that low
// usually means the scores were recorded on the wrong scale, so the scores
// page warns the operator.
type lowScoreWarning struct {
	Name      string
	Max       float32
	Threshold float32
	Scored    int
}

// belowLowScore reports whether value is under 33% of max. The comparison is
// done by cross-multiplication so a score of exactly 33% is not flagged, which
// float rounding on e.g. 99 of 300 would otherwise do.
func belowLowScore(value, max float32) bool {
	return float64(value)*lowScorePercentBasis < float64(max)*lowScorePercent
}

// lowScoreCategories returns one warning per category, in column order, that
// has at least one entered score and no score at or above 33% of its maximum.
// A category with no scores yet is not warned about, and a category whose
// maximum is not usable is skipped.
//
// Columns are identified by index across every row, not only across scored
// ones: a team whose only score sits in a later category must not lend that
// category's identity to the earlier columns it left blank.
func lowScoreCategories(rows []scoreRow) []lowScoreWarning {
	var widest *scoreRow
	for i := range rows {
		if widest == nil || len(rows[i].Cells) > len(widest.Cells) {
			widest = &rows[i]
		}
	}
	if widest == nil {
		return nil
	}

	var columns []*lowScoreWarning
	highest := make([]float32, 0, len(widest.Cells))
	for _, c := range widest.Cells {
		// A category without a usable maximum cannot be judged as low; the nil
		// placeholder keeps every later column aligned to its index.
		if c.Max <= 0 {
			columns = append(columns, nil)
			highest = append(highest, 0)
			continue
		}
		columns = append(columns, &lowScoreWarning{
			Name:      c.Name,
			Max:       c.Max,
			Threshold: c.Max * float32(lowScorePercent) / float32(lowScorePercentBasis),
		})
		highest = append(highest, 0)
	}

	for _, row := range rows {
		for i, c := range row.Cells {
			if i >= len(columns) || columns[i] == nil || c.Value == nil {
				continue
			}
			columns[i].Scored++
			if *c.Value > highest[i] {
				highest[i] = *c.Value
			}
		}
	}

	var warnings []lowScoreWarning
	for i, col := range columns {
		if col == nil || col.Scored == 0 {
			continue
		}
		if belowLowScore(highest[i], col.Max) {
			warnings = append(warnings, *col)
		}
	}
	return warnings
}

func (m *Module) ListTeamScores(ctx context.Context, saved string) ([]scoreRow, error) {
	teams, err := gorm.G[team.Team](m.db.DB).
		Preload("Division", nil).
		Preload("Region", nil).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		return nil, err
	}

	types, err := gorm.G[ScoreType](m.db.DB).Order("`order` ASC").Find(ctx)
	if err != nil {
		return nil, err
	}

	values, err := gorm.G[TeamScoreValue](m.db.DB).Find(ctx)
	if err != nil {
		return nil, err
	}

	byTeamType := make(map[uint]map[uint]float32, len(values))
	for _, v := range values {
		inner, ok := byTeamType[v.TeamID]
		if !ok {
			inner = make(map[uint]float32)
			byTeamType[v.TeamID] = inner
		}
		inner[v.ScoreTypeID] = v.Value
	}

	out := make([]scoreRow, 0, len(teams))
	for _, team := range teams {
		row := scoreRow{Team: team}
		var total *float32
		for _, st := range types {
			cell := scoreCell{
				Key:    st.Key,
				Name:   st.Name,
				Max:    st.Max,
				Action: path.Join(m.basePath, "scores", strconv.FormatInt(int64(team.ID), 10), st.Key),
				Saved:  saved == fmt.Sprintf("%d-%s", team.ID, st.Key),
			}
			if inner, ok := byTeamType[team.ID]; ok {
				if v, ok := inner[st.ID]; ok {
					cell.Value = &v
					cell.ValueFormatted = strconv.FormatFloat(float64(v), 'f', 2, 32)
					if total == nil {
						total = &v
					} else {
						t := *total + v
						total = &t
					}
				}
			}
			row.Cells = append(row.Cells, cell)
		}
		row.Total = total
		out = append(out, row)
	}

	return out, nil
}

func (m *Module) uiViewScores(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	teamScores, err := m.ListTeamScores(ctx, r.URL.Query().Get("saved"))
	if err != nil {
		m.internalError(w, r, err)
		return
	}

	types, err := gorm.G[ScoreType](m.db.DB).Order("`order` ASC").Find(ctx)
	if err != nil {
		m.internalError(w, r, err)
		return
	}

	m.ws.DoTemplate(w, r, "views/best/scores.p2", pongo2.Context{
		"scores":      teamScores,
		"score_types": types,
		"low_scores":  lowScoreCategories(teamScores),
	})
}

func (m *Module) uiViewScoreSet(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	ctx := r.Context()
	teamID := m.ws.StrToUint(chi.URLParam(r, "id"))
	fieldKey := chi.URLParam(r, "field")

	st, err := gorm.G[ScoreType](m.db.DB).Where(&ScoreType{Key: fieldKey}).First(ctx)
	if errors.Is(err, gorm.ErrRecordNotFound) {
		m.internalError(w, r, fmt.Errorf("unknown score type %q", fieldKey))
		return
	} else if err != nil {
		m.internalError(w, r, err)
		return
	}

	value := r.FormValue("value")
	if value == "" {
		if _, err := gorm.G[TeamScoreValue](m.db.DB).
			Where(&TeamScoreValue{TeamID: teamID, ScoreTypeID: st.ID}).
			Delete(ctx); err != nil {
			m.internalError(w, r, err)
			return
		}
	} else {
		f, err := strconv.ParseFloat(value, 32)
		if err != nil {
			m.internalError(w, r, fmt.Errorf("invalid score value %q: %w", value, err))
			return
		}
		if f < 0 || f > float64(st.Max) {
			m.internalError(w, r, fmt.Errorf("score %s for team %d must be between 0 and %g", st.Name, teamID, st.Max))
			return
		}
		if err := upsertTeamScore(ctx, m, teamID, st.ID, float32(f)); err != nil {
			m.internalError(w, r, err)
			return
		}
	}

	saved := fmt.Sprintf("%d-%s", teamID, fieldKey)
	http.Redirect(w, r, path.Join(m.basePath, "scores")+"?saved="+saved, http.StatusSeeOther)
}

func (m *Module) internalError(w http.ResponseWriter, r *http.Request, err error) {
	slog.Error("Error in best scores", "error", err)
	m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
}
