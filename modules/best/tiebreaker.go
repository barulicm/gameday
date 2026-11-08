package best

import (
	"context"
	"slices"
	"sort"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"gorm.io/gorm"
)

const TieBreakerBESTUnified = "BESTUnifiedTieBreaker"

// unifiedTieBreaker is the live tie-breaker function for the current
// module instance.  It is updated by New() on every construction so
// the registered function always queries the current database.  It is
// nil until the module is constructed; the registered dispatcher then
// leaves the input order untouched.
var unifiedTieBreaker modules.TieBreaker

// registerTieBreaker installs the dispatcher for TieBreakerBESTUnified
// in the global registry.  It is called from New(), so the name only
// resolves once the BEST module is actually loaded: a setup file that
// references it without the module reports as an unknown tie-breaker
// instead of silently resolving to a no-op.  It is idempotent so
// repeated module construction (multiple databases in one process,
// as tests do) never panics on a duplicate registration.
func registerTieBreaker() {
	if _, ok := modules.GetTieBreaker(TieBreakerBESTUnified); ok {
		return
	}
	modules.RegisterTieBreaker(TieBreakerBESTUnified, func(nums []int, mc int) []int {
		if unifiedTieBreaker == nil {
			out := make([]int, len(nums))
			copy(out, nums)
			return out
		}
		return unifiedTieBreaker(nums, mc)
	})
}

// TieBreaker orders the given tied team numbers by notebook score
// first, then by marketing, poster, and video scores (in that order,
// each higher first), then by team number.  The match count is
// accepted for interface compatibility and ignored.
func (n *NotebookAdvancement) TieBreaker(teamNumbers []int, _ int) []int {
	ctx := context.Background()

	teams, err := gorm.G[team.Team](n.db.DB).
		Where("number IN ?", teamNumbers).
		Find(ctx)
	if err != nil {
		return slices.Clone(teamNumbers)
	}

	scope := make(map[uint]team.Team, len(teams))
	for _, t := range teams {
		scope[t.ID] = t
	}

	buffer, err := scoreBuffer(ctx, n.db, scope)
	if err != nil {
		return slices.Clone(teamNumbers)
	}

	rows := make([]notebookRow, 0, len(teamNumbers))
	for _, t := range teams {
		row := notebookRow{
			Team:   t,
			TeamID: t.ID,
			Tie:    make([]float32, len(tieBreakKeys)),
		}
		if byType, ok := buffer[t.ID]; ok {
			row.Value = byType[0]
			for i := range tieBreakKeys {
				row.Tie[i] = byType[1+i]
			}
		}
		rows = append(rows, row)
	}

	sort.Slice(rows, func(i, j int) bool {
		return notebookRowLess(rows[i], rows[j])
	})

	out := make([]int, 0, len(rows))
	for _, row := range rows {
		out = append(out, row.Team.Number)
	}
	return out
}
