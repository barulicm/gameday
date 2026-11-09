package best

import (
	"context"
	"fmt"
	"strconv"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// bestAwardComponentKeys lists the judged score categories that make up
// the BEST Award (rules §5).  The order is deliberate: it is exactly the
// order scoreBuffer indexes its values in (notebook, then tieBreakKeys),
// so the scorer reads a team's components by index, and it is also the
// canonical tie-break order.  Robot Performance is the fifth component
// and is derived from the seeding phase rather than recorded as a score
// type, so it is handled separately.
var bestAwardComponentKeys = append([]string{"notebook"}, tieBreakKeys...)

// bestAwardScorer computes the BEST Award total: notebook, marketing,
// poster, and video scores plus the derived Robot Performance points
// (rules §5, §4.2.1).  All five components are required for BEST Award
// consideration: a team is eligible only when every one of them is
// present, so a team missing any component is absent from the map
// regardless of the points it did earn.  Disabled teams are never
// eligible.
type bestAwardScorer struct {
	db   *db.DB
	perf performanceSource
	diag *awardDiagnosis
}

// Score implements Scorer.  On entry the diagnosis is reset, so the
// diagnostics a caller reads afterwards always describe this call.
func (s *bestAwardScorer) Score(ctx context.Context, teams []team.Team) (map[uint]float64, error) {
	diag := &awardDiagnosis{}
	s.diag = diag

	perf, err := s.perf.Points(ctx)
	if err != nil {
		return nil, err
	}
	diag.addPerformanceNotes(perf)

	types, err := scoreTypes(ctx, s.db, bestAwardComponentKeys)
	if err != nil {
		return nil, err
	}

	out := map[uint]float64{}

	diag.Components = make([]diagnosisColumn, 0, len(types)+1)
	for _, st := range types {
		diag.Components = append(diag.Components, diagnosisColumn{
			Name:   st.Key,
			Header: fmt.Sprintf("%s /%s", st.Name, formatMax(st.Max)),
		})
	}
	diag.Components = append(diag.Components, diagnosisColumn{
		Name:   performanceKey,
		Header: performanceHeader(),
	})

	if len(teams) == 0 {
		return out, nil
	}

	scope := make(map[uint]team.Team, len(teams))
	for _, t := range teams {
		if t.Disabled {
			continue
		}
		scope[t.ID] = t
	}

	buffer, err := scoreBuffer(ctx, s.db, scope)
	if err != nil {
		return nil, err
	}

	for _, t := range teams {
		if _, ok := scope[t.ID]; !ok {
			continue
		}

		row := candidateRow{Team: t}
		total := 0.0
		complete := true
		for i, st := range types {
			cell := candidateCell{Display: missingDisplay}
			value, ok := buffer[t.ID][i]
			if !ok {
				row.Missing = append(row.Missing, st.Name)
				complete = false
			} else {
				cell.Display = strconv.FormatFloat(float64(value), 'f', 2, 32)
				total += float64(value)
			}
			row.Cells = append(row.Cells, cell)
		}

		rpCell := candidateCell{Display: missingDisplay}
		if points, ok := perf.ByTeam[t.ID]; ok {
			rpCell.Display = strconv.FormatFloat(points, 'f', 2, 64)
			total += points
		} else {
			row.Missing = append(row.Missing, performanceName)
			complete = false
		}
		row.Cells = append(row.Cells, rpCell)

		row.Ready = complete
		if complete {
			out[t.ID] = total
		}
		diag.Candidates = append(diag.Candidates, row)
	}

	return out, nil
}

// Diagnose implements awardDiagnoster.
func (s *bestAwardScorer) Diagnose() *awardDiagnosis {
	return s.diag
}

// awardDiagnoster is an optional interface a Scorer may implement to
// explain which candidates it left out and why.  The awards page shows
// the diagnosis next to the ranking so an operator can see what a team
// still needs, rather than wondering where it went.
type awardDiagnoster interface {
	Diagnose() *awardDiagnosis
}

// awardDiagnosis is the eligibility picture behind an award: any notes
// explaining a degraded derivation, one column per component, and one
// row per candidate with its component values and what is missing.
type awardDiagnosis struct {
	Notes      []string
	Components []diagnosisColumn
	Candidates []candidateRow
}

// diagnosisColumn describes one component column of the eligibility
// table.  The columns are shared by every candidate row, which is what
// the template's header needs.
type diagnosisColumn struct {
	Name   string
	Header string
}

// missingDisplay is the placeholder for a component a team has no value
// for, matching the em dash the scores page uses for an empty cell.
const missingDisplay = "—"

// performanceHeader is the eligibility table's column header for Robot
// Performance, showing its maximum the way the judged columns do.
func performanceHeader() string {
	return fmt.Sprintf("%s /%s", performanceName, formatMax(performanceMax))
}

// formatMax renders a score maximum without a trailing ".00", so a
// component header reads "Notebook /300".
func formatMax(max float32) string {
	return strconv.FormatFloat(float64(max), 'f', -1, 32)
}

// candidateRow is one team's BEST Award component values.  Cells align
// with awardDiagnosis.Components.
type candidateRow struct {
	Team    team.Team
	Cells   []candidateCell
	Missing []string
	Ready   bool
}

// candidateCell is a single component's display value; an em dash when
// the team has no value for it.
type candidateCell struct {
	Display string
}

// addPerformanceNotes records why Robot Performance points are not
// available, when that is the case.
func (d *awardDiagnosis) addPerformanceNotes(perf *performanceTable) {
	switch perf.State {
	case performanceNoGameModule:
		d.Notes = append(d.Notes, "Robot Performance is unavailable: the game module is not loaded.")
	case performanceNoPhase:
		d.Notes = append(d.Notes, "Robot Performance is unavailable: no phase is marked with the Seeding role.")
	case performanceNotFrozen:
		note := "Robot Performance is unavailable: the Seeding phase is not frozen yet."
		if perf.PhaseName != "" {
			note = "Robot Performance is unavailable: the Seeding phase " + perf.PhaseName + " is not frozen yet."
		}
		d.Notes = append(d.Notes, note)
	case performanceReady:
		if perf.SplitByDivision {
			d.Notes = append(d.Notes, "Robot Performance points are ranked per division, matching the BEST Award, even though the Seeding phase is not division aware.")
		}
	}
}

// bestAwardDescription spells out the 1000-point composition of the
// BEST Award so the awards page can show the math.
const bestAwardDescription = "The 1000-point BEST Award: Engineering Notebook (300), Customer Marketing (250), Poster Display and Interview (100), Digital Marketing Video (100), and Robot Performance (250, derived from the seeding phase ranking). All five components are required for consideration."
