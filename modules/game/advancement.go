package game

import (
	"context"
	"fmt"
	"log/slog"
	"maps"
	"sort"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// RankingRow is a single team's aggregated scoreboard row for one
// phase (and, for a division aware phase, one division).  Score holds
// the value the phase's ScoreSummation selected, and Rank is the
// team's position, resolved through the phase's TieBreaker when one is
// configured.
type RankingRow struct {
	Team     team.Team
	TeamID   uint
	Rank     int
	Average  int `gorm:"column:avg"`
	Mulligan int
	Total    int
	Score    int
	Max      int
	Min      int
	Count    int
}

func (m *Module) scoreboardRankings(ctx context.Context, phaseID uint, division string) ([]RankingRow, error) {
	var phase GamePhase
	var err error
	if phaseID == 0 {
		phase, err = gorm.G[GamePhase](m.db.DB).Where("active = true").First(ctx)
	} else {
		phase, err = gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: phaseID}).First(ctx)
	}
	if err != nil {
		slog.Error("Error fetching scoreboard data", "error", err)
		return nil, err
	}

	orderBy := "mulligan"
	switch phase.ScoreSummation {
	case "Total":
		orderBy = "total"
	}

	// This query is terrible, but it is what is required to
	// generate the SQL using the query builder.  Even then its
	// not entirely portable, as it depends on the existence of
	// SUM(), MAX(), MIN(), and COUNT() being direct callables in
	// the dialect of SQL this runs against.  Since this is only
	// really expected to run against SQLite and PostgreSQL, this
	// is fine (TM) but if someone decides to run this against
	// T-SQL, undefined behaviors may happen.
	rowData, err := gorm.G[RankingRow](m.db.DB,
		clause.Select{
			Expression: clause.CommaExpression{Exprs: []clause.Expression{
				clause.NamedExpr{SQL: "?", Vars: []interface{}{clause.Column{Name: "team_id"}}},
				clause.NamedExpr{SQL: "CAST(AVG(?) AS INT) AS avg", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{
					SQL: "COALESCE((SUM(?) - MIN(?)) / (COUNT(?) - 1), CAST(AVG(?) AS INT)) AS mulligan",
					Vars: []interface{}{
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
						clause.Column{Name: "score"},
					},
				},
				clause.NamedExpr{SQL: "SUM(?) AS total", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{SQL: "MIN(?) AS min", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{SQL: "MAX(?) AS max", Vars: []interface{}{clause.Column{Name: "score"}}},
				clause.NamedExpr{SQL: "COUNT(?) AS count", Vars: []interface{}{clause.Column{Name: "score"}}},
			}},
		},
		clause.From{Tables: []clause.Table{{Name: "match_scores"}}},
		clause.Where{Exprs: []clause.Expression{clause.Eq{Column: "game_phase_id", Value: phase.ID}}},
		clause.GroupBy{Columns: []clause.Column{{Name: "team_id"}}},
		clause.OrderBy{Columns: []clause.OrderByColumn{{
			Column: clause.Column{Name: orderBy},
			Desc:   true,
		}}},
	).Preload("Team.Division", nil).Find(ctx)
	if err != nil {
		slog.Error("Error selecting scoreboard data", "error", err)
		return nil, err
	}

	out := []RankingRow{}
	for _, row := range rowData {
		if row.Team.Division.Name != division && division != "" {
			continue
		}
		out = append(out, row)
	}

	rank := 1
	for i, row := range out {
		switch phase.ScoreSummation {
		case "Total":
			out[i].Score = row.Total
		case "AverageWithMulligan":
			out[i].Score = row.Mulligan
		}

		// Setup the rank, which is different than the index
		// because of ties.
		if i > 0 && out[i-1].Score != out[i].Score {
			rank++
		}
		out[i].Rank = rank
	}

	// Resolve ties using phase-configured tie-breaker.
	if phase.TieBreaker != "" {
		resolveTies(ctx, m.db, &out, &phase)
	}

	return out, nil
}

// PhaseStats holds the aggregate score statistics for a single game
// phase.
type PhaseStats struct {
	Min int
	Max int
	Avg int

	// MinPlacement and MaxPlacement are the placements (with their
	// team, field, and position) that produced the minimum and maximum
	// scores. When several placements share an extreme value, the one
	// with the lowest MatchScore ID is reported.
	MinPlacement MatchPlacement
	MaxPlacement MatchPlacement
}

// PhaseScoreStats returns the minimum, maximum, and average score for
// the given game phase. For the minimum and maximum, the MatchPlacement
// (with its team, field, and position) that produced the value is
// included. A phase with no scores returns a zero PhaseStats and a nil
// error.
func (m *Module) PhaseScoreStats(ctx context.Context, phaseID uint) (PhaseStats, error) {
	var out PhaseStats

	rows, err := gorm.G[MatchScore](m.db.DB).
		Where(&MatchScore{GamePhaseID: phaseID}).
		Order("id ASC").
		Preload("MatchPlacement.Team", nil).
		Preload("MatchPlacement.Field", nil).
		Preload("MatchPlacement.Position", nil).
		Find(ctx)
	if err != nil {
		slog.Error("Error fetching phase scores", "error", err)
		return out, err
	}

	if len(rows) == 0 {
		return out, nil
	}

	sum := 0
	for i, r := range rows {
		sum += r.Score
		if i == 0 || r.Score < out.Min {
			out.Min = r.Score
			out.MinPlacement = r.MatchPlacement
		}
		if i == 0 || r.Score > out.Max {
			out.Max = r.Score
			out.MaxPlacement = r.MatchPlacement
		}
	}
	out.Avg = sum / len(rows)

	return out, nil
}

// PhaseRankings returns the final rankings of every phase carrying the
// given role, as one PhaseRanking per phase-and-division pair (a phase
// that is not division aware yields a single entry with an empty
// Division; a division aware phase yields one entry per division that
// has ranked teams).  Ranks use each phase's configured
// ScoreSummation and TieBreaker and restart at 1 in each division.
// When frozenOnly is set, phases that are not frozen are silently
// skipped.  An unknown or unused role yields an empty slice, not an
// error.  Phases are returned in ID order, which matches tournament
// order only when phases are authored with monotonically increasing
// IDs.
func (m *Module) PhaseRankings(ctx context.Context, role PhaseRole, frozenOnly bool) ([]PhaseRanking, error) {
	// The string form is deliberate: Where(&GamePhase{Role: ""})
	// would drop the zero-value condition and match every phase.
	phases, err := gorm.G[GamePhase](m.db.DB).
		Where("phase_role = ?", role).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		slog.Error("Error fetching phases by role", "error", err, "role", role)
		return nil, err
	}

	out := []PhaseRanking{}
	for _, phase := range phases {
		if frozenOnly && !phase.Frozen {
			continue
		}
		tables, err := m.phaseRankingForPhase(ctx, phase)
		if err != nil {
			return nil, err
		}
		out = append(out, tables...)
	}
	return out, nil
}

// filterScoreboard loads the raw scoreboard feeding a filter, returning
// an empty board for a roster-sourced filter (selectFrom of 0), which
// selects from the pool instead of from a scoreboard.
func (m *Module) filterScoreboard(ctx context.Context, selectFrom uint, division string) ([]RankingRow, error) {
	if selectFrom == 0 {
		return []RankingRow{}, nil
	}
	return m.scoreboardRankings(ctx, selectFrom, division)
}

// filterContext builds the AdvancementFilterContext a filter is handed:
// the roster minus the blocklist, with the scoreboard pruned the same
// way, so a filter's cutoff applies to the teams that remain.  Both
// readiness checks and Apply receive a context built here, so the field
// a filter is measured against is always the field it runs on.
func filterContext(teams []team.Team, rowData []RankingRow, excluded map[uint]struct{}) AdvancementFilterContext {
	roster := makeRoster(teams)
	remaining := make(map[uint]team.Team, len(roster))
	for id, t := range roster {
		if _, blocked := excluded[id]; !blocked {
			remaining[id] = t
		}
	}
	board := make([]RankingRow, 0, len(rowData))
	for _, row := range rowData {
		if _, blocked := excluded[row.Team.ID]; !blocked {
			board = append(board, row)
		}
	}
	return AdvancementFilterContext{
		Roster:     roster,
		Remaining:  remaining,
		Scoreboard: board,
	}
}

// runAdvancementFilters applies every advancement filter configured on
// a phase for a single division.  The filters run in order against a
// single shared candidate pool: an include filter adds the teams it
// selects, and an exclude filter removes the teams it selects.  Teams
// an exclude filter removes join a blocklist that is then stripped from
// the field every later filter sees, so an include filter's cutoff
// applies to the teams that remain (a "best of the losers" ranking) and
// no later include can re-advance them.  For a filter with SelectFrom
// of 0 the scoreboard is left empty so the filter selects from the full
// roster (the start-of-schedule case); otherwise the scoreboard for the
// referenced phase is loaded.  A filter with a When condition is
// evaluated against the same context as a phase When expression (with
// the filter's own raw scoreboard) and is skipped entirely when it
// evaluates to false.  The remaining candidates and the accumulated
// determinations are returned.
func (m *Module) runAdvancementFilters(ctx context.Context, phase GamePhase, phases []GamePhase, phaseComplete map[uint]bool, division string, teams []team.Team) (map[uint]struct{}, []AdvancementDeterminationResult, error) {
	determinations := []AdvancementDeterminationResult{}
	// candidates is the shared pool of teams advanced so far; include
	// filters add to it.  excluded is the blocklist of teams an exclude
	// filter removed, which is stripped from the field of every later
	// filter and must never be re-advanced.
	candidates := make(map[uint]team.Team)
	excluded := make(map[uint]struct{})
	wctx := WhenContext{
		Phases:   phaseStates(phases, phaseComplete),
		Roster:   makeRoster(teams),
		Division: division,
	}
	for _, filter := range phase.AdvancementFilters {
		rowData, err := m.filterScoreboard(ctx, filter.SelectFrom, division)
		if err != nil {
			slog.Error("Error retrieving filter scoreboard", "filter", filter, "error", err)
			return nil, nil, err
		}

		wctx.Scoreboard = rowData
		satisfied, err := evalWhen(filter.When, wctx, fmt.Sprintf("When expression on filter %q", filter.Rule))
		if err != nil {
			slog.Error("Error evaluating filter When", "filter", filter, "error", err)
			return nil, nil, err
		}
		if !satisfied {
			slog.Debug("Skipping advancement filter (When not satisfied)", "filter", filter)
			continue
		}

		f, exists := filters[filter.Filter]
		if !exists {
			slog.Error("Tried to load unregistered filter", "filter", filter)
			return nil, nil, fmt.Errorf("advancement filter %q is not registered", filter.Filter)
		}

		// The field the filter sees is the roster minus the
		// blocklist, so a cutoff applies to the teams that remain
		// rather than to the whole field.
		sctx := filterContext(teams, rowData, excluded)

		if filter.Mode == GamePhaseAdvancementFilterModeExclude {
			// An exclude filter removes teams.  Run it against a
			// copy of the remaining field so every removal lands;
			// the field-minus-result diff is the filter's full
			// target set, which joins the blocklist.
			sctx.Candidates = make(map[uint]team.Team, len(sctx.Remaining))
			maps.Copy(sctx.Candidates, sctx.Remaining)
			if err := f.Apply(&sctx, filter.Rule, filter.Mode, filter.SliceExpr); err != nil {
				slog.Error("Error applying advancement filter", "filter", filter, "error", err)
				return nil, nil, err
			}
			for id := range sctx.Remaining {
				if _, kept := sctx.Candidates[id]; !kept {
					excluded[id] = struct{}{}
				}
			}
			// Keep the pool free of newly blocked teams as well.
			for id := range excluded {
				delete(candidates, id)
			}
		} else {
			sctx.Candidates = candidates
			if err := f.Apply(&sctx, filter.Rule, filter.Mode, filter.SliceExpr); err != nil {
				slog.Error("Error applying advancement filter", "filter", filter, "error", err)
				return nil, nil, err
			}
		}

		determinations = append(determinations, sctx.Determinations...)
	}

	advancing := make(map[uint]struct{}, len(candidates))
	for id := range candidates {
		advancing[id] = struct{}{}
	}
	return advancing, determinations, nil
}

// filterWhenSatisfied evaluates a single filter's When expression for
// one division, using the filter's own scoreboard as the Scoreboard
// context (mirroring runAdvancementFilters).  An empty When expression
// is always satisfied, i.e. the filter always runs.
func (m *Module) filterWhenSatisfied(ctx context.Context, phases []GamePhase, phaseComplete map[uint]bool, teams []team.Team, filter GamePhaseAdvancementFilter, division string) (bool, error) {
	scoreboard, err := m.filterScoreboard(ctx, filter.SelectFrom, division)
	if err != nil {
		return false, err
	}
	wctx := WhenContext{
		Phases:     phaseStates(phases, phaseComplete),
		Roster:     makeRoster(teams),
		Scoreboard: scoreboard,
		Division:   division,
	}
	return evalWhen(filter.When, wctx, fmt.Sprintf("When expression on filter %q", filter.Rule))
}

// AdvancementReadiness is a phase's verdict on whether its advancement
// filters can produce a correct candidate list right now.
type AdvancementReadiness struct {
	CanRun bool
	// Reasons holds one entry per blocking filter or missing source
	// phase, in filter order.  It is empty when CanRun is true.
	Reasons []string
}

// block marks the phase as unable to run and records why.
func (r *AdvancementReadiness) block(reason string) {
	r.CanRun = false
	r.Reasons = append(r.Reasons, reason)
}

// phaseSchedulable reports whether the filters configured on a phase
// can run right now.  A filter needs two things: its source phase
// complete and frozen (a roster-sourced filter, SelectFrom of 0, has
// no such requirement), and — when the filter implements
// AdvancementFilterReadiness — its own data present and usable.  A
// filter whose When condition evaluates to false is skipped by
// runAdvancementFilters, so it never blocks; for a division-aware
// phase the filter only counts as needed when its When condition is
// true for at least one configured division.  A phase with no filters
// is schedulable.
//
// All blockers are collected, not just the first, so the operator sees
// every unmet generation parameter at once.  A readiness check that
// errors is treated as not-ready (the error message becomes the
// reason) rather than failing the page: a phase list that explains
// itself is more useful mid-event than one that dies.
func (m *Module) phaseSchedulable(ctx context.Context, phase GamePhase, phases []GamePhase, phaseComplete map[uint]bool, teams []team.Team) (AdvancementReadiness, error) {
	verdict := AdvancementReadiness{CanRun: true}
	filterRows, err := gorm.G[GamePhaseAdvancementFilter](m.db.DB).
		Where(&GamePhaseAdvancementFilter{GamePhaseID: phase.ID}).
		Find(ctx)
	if err != nil {
		return verdict, err
	}
	if len(filterRows) == 0 {
		return verdict, nil
	}

	// Map lookup by ID: phase IDs follow the authored setup file and
	// are not guaranteed contiguous, so indexing the phase slice
	// positionally by ID would read the wrong row.
	byID := make(map[uint]GamePhase, len(phases))
	for _, p := range phases {
		byID[p.ID] = p
	}

	for _, filter := range filterRows {
		// Determine whether this filter is required at all right
		// now.  A filter gated by a When condition that is false is
		// skipped by runAdvancementFilters, so it must not keep the
		// phase from being scheduled.
		required := true
		if filter.When != "" {
			divisions := []string{""}
			if phase.DivisionAware {
				divisions, err = m.whenDivisions(ctx, phase)
				if err != nil {
					return verdict, err
				}
			}
			required = false
			for _, division := range divisions {
				runs, err := m.filterWhenSatisfied(ctx, phases, phaseComplete, teams, filter, division)
				if err != nil {
					return verdict, err
				}
				if runs {
					required = true
					break
				}
			}
			if !required {
				slog.Debug("Filter not required (When not satisfied), not blocking schedulability",
					"phase", phase.Name,
					"rule", filter.Rule,
					"source_id", filter.SelectFrom,
				)
				continue
			}
		}

		if filter.SelectFrom != 0 {
			src, ok := byID[filter.SelectFrom]
			if !ok {
				verdict.block(fmt.Sprintf("%s: source phase %d no longer exists", filter.Rule, filter.SelectFrom))
				continue
			}
			slog.Debug("Evaluating filter satisfaction",
				"phase", phase.Name,
				"rule", filter.Rule,
				"source_id", src.ID,
				"source_complete", phaseComplete[src.ID],
				"source_frozen", src.Frozen,
			)
			if !phaseComplete[src.ID] || !src.Frozen {
				verdict.block(fmt.Sprintf("%s: %s must be complete and frozen first", filter.Rule, src.Name))
				// The filter would read an empty scoreboard; its own
				// readiness answer would only repeat this one.
				continue
			}
		}

		f, exists := filters[filter.Filter]
		if !exists {
			// Registry correctness is LintConfig's job; an
			// unregistered filter simply has nothing to be asked.
			continue
		}

		// The whole-field board answers the data-availability
		// question readiness asks; which teams survive selection is
		// the filters' job, not this check's.
		rowData, err := m.filterScoreboard(ctx, filter.SelectFrom, "")
		if err != nil {
			return verdict, err
		}
		sctx := filterContext(teams, rowData, nil)
		ok, reason, err := filterCanRun(ctx, f, &sctx)
		if err != nil {
			slog.Error("Advancement filter readiness check failed",
				"phase", phase.Name,
				"filter", filter.Filter,
				"error", err,
			)
			verdict.block(fmt.Sprintf("%s: %v", filter.Rule, err))
			continue
		}
		if !ok {
			verdict.block(fmt.Sprintf("%s: %s", filter.Rule, reason))
		}
	}
	return verdict, nil
}

// phaseSequence returns the phases in the order their advancement
// dependencies imply: every phase comes after the phases its filters
// read from (its SelectFrom sources).  Ties break on phase ID, which
// matches the implicit authored order used elsewhere.  A cyclic
// configuration cannot progress, so on a cycle the remaining phases
// are appended in ID order and the situation is logged; LintConfig
// validates that SelectFrom references exist but not that the graph is
// acyclic, so this must never hang.
func (m *Module) phaseSequence(ctx context.Context, phases []GamePhase) ([]GamePhase, error) {
	filterRows, err := gorm.G[GamePhaseAdvancementFilter](m.db.DB).Find(ctx)
	if err != nil {
		return nil, err
	}
	deps := make(map[uint]map[uint]bool)
	for _, filter := range filterRows {
		if filter.SelectFrom == 0 {
			continue
		}
		if deps[filter.GamePhaseID] == nil {
			deps[filter.GamePhaseID] = make(map[uint]bool)
		}
		deps[filter.GamePhaseID][filter.SelectFrom] = true
	}

	byID := make(map[uint]GamePhase, len(phases))
	remaining := make(map[uint]bool, len(phases))
	for _, p := range phases {
		byID[p.ID] = p
		remaining[p.ID] = true
	}

	out := make([]GamePhase, 0, len(phases))
	for len(remaining) > 0 {
		// Pick the lowest remaining ID whose remaining dependencies
		// are all already emitted.
		next := uint(0)
		for id := range remaining {
			blocked := false
			for dep := range deps[id] {
				// A source outside the phase set cannot be waited on.
				if remaining[dep] {
					blocked = true
					break
				}
			}
			if blocked {
				continue
			}
			if next == 0 || id < next {
				next = id
			}
		}
		if next == 0 {
			// A cycle: no phase can proceed.  Fall back to ID order
			// so the page still renders deterministically.
			slog.Warn("Advancement filter dependency cycle, falling back to ID order",
				"remaining", len(remaining))
			nums := make([]uint, 0, len(remaining))
			for id := range remaining {
				nums = append(nums, id)
			}
			sort.Slice(nums, func(i, j int) bool { return nums[i] < nums[j] })
			for _, id := range nums {
				out = append(out, byID[id])
			}
			return out, nil
		}
		out = append(out, byID[next])
		delete(remaining, next)
	}
	return out, nil
}

// resolveTies strictly orders tied groups using the phase's configured
// tie-breaker and renumbers the board behind every reordered group, so
// a lower score can never out-rank a higher one.  Rows the tie-breaker
// separates get consecutive ranks; rows it leaves equal (teams from
// different divisions of a division aware phase, which are never
// compared against each other) keep sharing a rank.
func resolveTies(ctx context.Context, dbd *db.DB, rows *[]RankingRow, phase *GamePhase) {
	if len(*rows) == 0 {
		return
	}
	tb, ok := modules.GetTieBreaker(phase.TieBreaker)
	if !ok {
		slog.Warn("Tie-breaker not found", "name", phase.TieBreaker)
		return
	}

	// Count total matches for this phase (for tie-breaker context).
	var matchCount int64
	if err := dbd.Model(&MatchScore{}).Where("game_phase_id = ?", phase.ID).Count(&matchCount).Error; err != nil {
		slog.Warn("Error counting matches for tie-breaker context", "error", err)
	}

	// A tied block is a maximal run of rows sharing a rank.  When the
	// phase is division aware each block is subdivided by division and
	// only sub-groups of two or more teams are reordered.
	type tiedBlock struct {
		start int
		end   int
		// orders holds, per sub-group of two or more teams, the row
		// indices in tie-breaker order.
		orders [][]int
	}
	resolved := make([]bool, len(*rows))
	var blocks []tiedBlock
	start := 0
	for i := 1; i <= len(*rows); i++ {
		if i < len(*rows) && (*rows)[i].Rank == (*rows)[start].Rank {
			continue
		}
		// rows[start:i] share a rank.
		if i-1 > start {
			var subGroups [][]int
			if phase.DivisionAware {
				byDiv := make(map[string][]int)
				var names []string
				for idx := start; idx < i; idx++ {
					div := (*rows)[idx].Team.Division.Name
					if _, seen := byDiv[div]; !seen {
						names = append(names, div)
					}
					byDiv[div] = append(byDiv[div], idx)
				}
				for _, name := range names {
					subGroups = append(subGroups, byDiv[name])
				}
			} else {
				whole := make([]int, 0, i-start)
				for idx := start; idx < i; idx++ {
					whole = append(whole, idx)
				}
				subGroups = [][]int{whole}
			}

			block := tiedBlock{start: start, end: i - 1}
			for _, group := range subGroups {
				if len(group) < 2 {
					continue
				}
				ordered := orderTiedGroup(tb, *rows, group, int(matchCount))
				block.orders = append(block.orders, ordered)
				for _, idx := range ordered {
					resolved[idx] = true
				}
			}
			if len(block.orders) > 0 {
				blocks = append(blocks, block)
			}
		}
		start = i
	}

	if len(blocks) == 0 {
		return
	}

	// Rewrite the rows so each tied block lists its reordered members
	// first, in tie-breaker order, with any member the block did not
	// reorder following behind.
	out := make([]RankingRow, 0, len(*rows))
	outResolved := make([]bool, 0, len(*rows))
	next := 0
	for i := 0; i < len(*rows); i++ {
		for next < len(blocks) && i > blocks[next].end {
			next++
		}
		if next < len(blocks) && i == blocks[next].start {
			block := blocks[next]
			for _, ordered := range block.orders {
				for _, idx := range ordered {
					out = append(out, (*rows)[idx])
					outResolved = append(outResolved, true)
				}
			}
			for idx := block.start; idx <= block.end; idx++ {
				if !resolved[idx] {
					out = append(out, (*rows)[idx])
					outResolved = append(outResolved, false)
				}
			}
			i = block.end
			continue
		}
		out = append(out, (*rows)[i])
		outResolved = append(outResolved, false)
	}

	// Renumber the whole board: the rank advances past a row the
	// tie-breaker separated or a row with a different score, so every
	// row behind a block shifts down by that block's expansion while
	// rows that remain tied keep sharing one rank.
	rank := 1
	for i := range out {
		if i > 0 && (out[i-1].Score != out[i].Score || outResolved[i-1] || outResolved[i]) {
			rank++
		}
		out[i].Rank = rank
	}
	*rows = out
}

// orderTiedGroup asks the tie-breaker to strictly order the rows at the
// given indices and returns those indices in tie-breaker order.  The
// result is sanitized: team numbers the tie-breaker does not recognize
// or repeats are dropped, and any team it failed to return keeps its
// original relative position at the end of the group, so a
// misbehaving tie-breaker can never drop a team from the board.
func orderTiedGroup(tb modules.TieBreaker, rows []RankingRow, group []int, matchCount int) []int {
	numbers := make([]int, len(group))
	byNumber := make(map[int]int, len(group))
	for i, idx := range group {
		numbers[i] = rows[idx].Team.Number
		byNumber[rows[idx].Team.Number] = idx
	}

	out := make([]int, 0, len(group))
	seen := make(map[int]struct{}, len(group))
	for _, number := range tb(numbers, matchCount) {
		idx, ok := byNumber[number]
		if !ok {
			continue
		}
		if _, dup := seen[number]; dup {
			continue
		}
		seen[number] = struct{}{}
		out = append(out, idx)
	}
	for _, idx := range group {
		if _, ok := seen[rows[idx].Team.Number]; !ok {
			out = append(out, idx)
		}
	}
	return out
}
