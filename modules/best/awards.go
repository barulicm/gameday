package best

import (
	"context"
	"net/http"
	"sort"

	"github.com/flosch/pongo2/v6"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/db"
)

// awardTieChain lists the BEST score category keys used to break ties
// between award candidates, in priority order (higher score first).
// The team number is the last-resort tie breaker and is applied after
// this chain.  This matches the canonical BEST tie-break order.
var awardTieChain = []string{"notebook", "marketing", "poster", "video"}

// overallGroupLabel is the group label used for a single ranking that
// spans every team rather than one division.
const overallGroupLabel = "Overall"

// defaultPodiumRanks is how many places an award shows inline before
// the rest of the ranking collapses into the expandable full ranking.
const defaultPodiumRanks = 3

// Scorer computes the award score for a set of teams.  A team absent
// from the returned map is not eligible for the award.
type Scorer interface {
	Score(ctx context.Context, teams []team.Team) (map[uint]float64, error)
}

// rankedTeam is one team's place in a ranking produced by a Ranker,
// whose ordering is already decided elsewhere (the finals scoreboard).
type rankedTeam struct {
	Team  team.Team
	Score float64
	Rank  int
}

// RankedGroup is a pre-ranked group of teams: one division, or the
// whole field for a ranking that is not split by division.
type RankedGroup struct {
	Label string
	Teams []rankedTeam
}

// Ranker produces an award's ranking directly instead of scoring teams
// and letting the award engine rank them.  It is used for awards whose
// places are decided by something other than a BEST score, such as the
// results of the Final phase.  Returning no groups means the award has
// no places yet.
type Ranker interface {
	Rank(ctx context.Context, teams []team.Team) ([]RankedGroup, error)
}

// Award is a declarative definition of a single award.  Adding a new
// award is a matter of appending one Award to the registry (see
// Module.awardDefinitions); a new metric requires a new Scorer, but the
// engine, handler, and template are shared by every award.
//
// An award takes its candidates from exactly one source: a Scorer, which
// the engine ranks by score with the canonical tie-break chain, or a
// Ranker, which arrives already ranked.
type Award struct {
	Key         string
	Name        string
	Description string
	Scorer      Scorer
	Ranker      Ranker

	// PodiumRanks is how many places the award shows inline; deeper
	// places are available in the expandable full ranking.  Zero means
	// defaultPodiumRanks.
	PodiumRanks int

	// DivisionAware, when true, awards one winner per division
	// instead of a single overall winner.  A Ranker names its own
	// groups and therefore ignores this flag.
	DivisionAware bool

	// Order controls the display order of the award on the page.
	Order int
}

// podiumSize returns the number of places the award shows inline.
func (a Award) podiumSize() int {
	if a.PodiumRanks <= 0 {
		return defaultPodiumRanks
	}
	return a.PodiumRanks
}

// bestCategoryScorer scores a team by its value in a single BEST score
// category (notebook, marketing, poster, or video).  A team is eligible
// only when it has a recorded value for the category.
type bestCategoryScorer struct {
	db  *db.DB
	key string
}

func (s bestCategoryScorer) Score(ctx context.Context, teams []team.Team) (map[uint]float64, error) {
	out := map[uint]float64{}
	if len(teams) == 0 {
		return out, nil
	}

	st, err := gorm.G[ScoreType](s.db.DB).Where(&ScoreType{Key: s.key}).First(ctx)
	if err != nil {
		return nil, err
	}

	teamIDs := make([]uint, 0, len(teams))
	for _, t := range teams {
		teamIDs = append(teamIDs, t.ID)
	}

	values, err := gorm.G[TeamScoreValue](s.db.DB).
		Where(&TeamScoreValue{ScoreTypeID: st.ID}).
		Where("team_id IN ?", teamIDs).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	for _, v := range values {
		out[v.TeamID] = float64(v.Value)
	}
	return out, nil
}

// awardDefinitions returns the registry of awards, in display order.
// To add a new award, append one Award here.  To add a new metric,
// implement a Scorer and reference it from an Award.  Whether an award
// is ranked per division or overall is driven by the game module:
// awards are division aware when any phase in the game is.  The BEST
// Award's Robot Performance points follow the same grouping.
func (m *Module) awardDefinitions() []Award {
	divisionAware := m.divisionAware()

	return []Award{
		{
			Key:           "best",
			Name:          "BEST Award",
			Description:   bestAwardDescription,
			Scorer:        &bestAwardScorer{db: m.db, perf: performanceFromSeeding{gm: m.gm, byDivision: divisionAware}},
			DivisionAware: divisionAware,
			Order:         0,
		},
		{
			Key:         "game",
			Name:        "Game Award",
			Description: "Awarded from the results of the final phase.",
			Ranker:      finalPhaseRanker{gm: m.gm},
			PodiumRanks: gameAwardPlaces,
			Order:       1,
		},
		{
			Key:           "notebook",
			Name:          "Best Notebook",
			Description:   "Highest notebook score.",
			Scorer:        bestCategoryScorer{db: m.db, key: "notebook"},
			DivisionAware: divisionAware,
			Order:         2,
		},
		{
			Key:           "marketing",
			Name:          "Best Marketing",
			Description:   "Highest marketing score.",
			Scorer:        bestCategoryScorer{db: m.db, key: "marketing"},
			DivisionAware: divisionAware,
			Order:         3,
		},
		{
			Key:           "poster",
			Name:          "Best Poster",
			Description:   "Highest poster score.",
			Scorer:        bestCategoryScorer{db: m.db, key: "poster"},
			DivisionAware: divisionAware,
			Order:         4,
		},
		{
			Key:           "video",
			Name:          "Best Video",
			Description:   "Highest video score.",
			Scorer:        bestCategoryScorer{db: m.db, key: "video"},
			DivisionAware: divisionAware,
			Order:         5,
		},
	}
}

// divisionAware reports whether the awards should be ranked per
// division, delegating to the game module.  When the game module is
// not available (it is optional in the module graph) the awards fall
// back to a single overall ranking.
func (m *Module) divisionAware() bool {
	return m.gm != nil && m.gm.DivisionAware()
}

// awardEntry is a single team's place in an award group's ranking.
// Co-winners (tied on the primary score and every tie-break value)
// share the same Rank.
type awardEntry struct {
	Team  team.Team
	Score float64
	Rank  int
}

// awardGroup is the ranking for one division (or the overall field when
// the award is not division-aware).  Podium holds the top three ranks
// (co-winners included); Complete holds the full ranking in order; More
// reports whether Complete extends beyond the podium.
type awardGroup struct {
	Label    string
	Podium   []awardEntry
	Complete []awardEntry
	More     bool
}

// awardResult is the computed winners for a single award.  Diagnosed
// reports whether Diagnosis holds an eligibility breakdown for the
// award; the scorer populates it when it can explain which candidates
// it excluded and why.
//
// The flag exists because pongo2 cannot test a nil pointer for truth: a
// pointer kind is neither a struct nor any other case IsTrue handles, so
// templates must not branch on Diagnosis directly.
type awardResult struct {
	Award     Award
	Groups    []awardGroup
	Diagnosed bool
	Diagnosis *awardDiagnosis
}

// awardRow is a candidate's ranking inputs: the primary award score,
// the tie-break chain values (aligned with awardTieChain), and the team
// for the last-resort tie breaker.
type awardRow struct {
	team    team.Team
	primary float64
	tie     []float64
}

// awardRowLess reports whether a ranks strictly above b: by primary
// score, then by each tie-break value (higher first), then by team
// number.
func awardRowLess(a, b awardRow) bool {
	if a.primary != b.primary {
		return a.primary > b.primary
	}
	for i := range awardTieChain {
		if a.tie[i] != b.tie[i] {
			return a.tie[i] > b.tie[i]
		}
	}
	return a.team.Number < b.team.Number
}

// awardRowRankEqual reports whether a and b are tied for the same rank
// (same primary and every tie-break value).  The team number is a
// last-resort ordering device only, so it does not break a rank tie.
func awardRowRankEqual(a, b awardRow) bool {
	if a.primary != b.primary {
		return false
	}
	for i := range awardTieChain {
		if a.tie[i] != b.tie[i] {
			return false
		}
	}
	return true
}

// tieBreakBuffer loads the per-category score values for the teams in
// scope, keyed by team ID and then by the index into awardTieChain.  A
// team with no value for a category has a zero for that index.
func tieBreakBuffer(ctx context.Context, d *db.DB, scope map[uint]team.Team) (map[uint][]float64, error) {
	buffer := map[uint][]float64{}
	if len(scope) == 0 {
		return buffer, nil
	}

	types, err := scoreTypes(ctx, d, awardTieChain)
	if err != nil {
		return nil, err
	}

	typeIdx := make(map[uint]int, len(types))
	for i, st := range types {
		typeIdx[st.ID] = i
	}

	teamIDs := make([]uint, 0, len(scope))
	for teamID := range scope {
		teamIDs = append(teamIDs, teamID)
	}

	values, err := gorm.G[TeamScoreValue](d.DB).
		Where("team_id IN ?", teamIDs).
		Find(ctx)
	if err != nil {
		return nil, err
	}

	for _, v := range values {
		idx, ok := typeIdx[v.ScoreTypeID]
		if !ok {
			continue
		}
		if buffer[v.TeamID] == nil {
			buffer[v.TeamID] = make([]float64, len(awardTieChain))
		}
		buffer[v.TeamID][idx] = float64(v.Value)
	}

	return buffer, nil
}

// computeAwards evaluates every award in the registry and returns the
// winners, in display order.
func (m *Module) computeAwards(ctx context.Context) ([]awardResult, error) {
	teams, err := gorm.G[team.Team](m.db.DB).
		Preload("Division", nil).
		Order("id ASC").
		Find(ctx)
	if err != nil {
		return nil, err
	}

	scope := make(map[uint]team.Team, len(teams))
	for _, t := range teams {
		scope[t.ID] = t
	}

	buffer, err := tieBreakBuffer(ctx, m.db, scope)
	if err != nil {
		return nil, err
	}

	defs := m.awardDefinitions()
	sort.Slice(defs, func(i, j int) bool {
		return defs[i].Order < defs[j].Order
	})

	out := make([]awardResult, 0, len(defs))
	for _, def := range defs {
		if def.Ranker != nil {
			grouped, err := def.Ranker.Rank(ctx, teams)
			if err != nil {
				return nil, err
			}
			result := awardResult{Award: def, Groups: []awardGroup{}}
			for _, ranked := range grouped {
				entries := make([]awardEntry, 0, len(ranked.Teams))
				for _, rt := range ranked.Teams {
					if _, ok := scope[rt.Team.ID]; !ok {
						continue
					}
					entries = append(entries, awardEntry{
						Team:  rt.Team,
						Score: rt.Score,
						Rank:  rt.Rank,
					})
				}
				if len(entries) == 0 {
					continue
				}
				result.Groups = append(result.Groups, newAwardGroup(def, ranked.Label, entries))
			}
			out = append(out, result)
			continue
		}

		scores, err := def.Scorer.Score(ctx, teams)
		if err != nil {
			return nil, err
		}

		var diag *awardDiagnosis
		if d, ok := def.Scorer.(awardDiagnoster); ok {
			diag = d.Diagnose()
		}

		rows := make([]awardRow, 0, len(scores))
		for teamID, primary := range scores {
			t, ok := scope[teamID]
			if !ok {
				continue
			}
			row := awardRow{team: t, primary: primary, tie: make([]float64, len(awardTieChain))}
			if byType, ok := buffer[teamID]; ok {
				for i := range awardTieChain {
					row.tie[i] = byType[i]
				}
			}
			rows = append(rows, row)
		}

		groups := map[string][]awardRow{}
		for _, row := range rows {
			key := ""
			if def.DivisionAware {
				key = row.team.Division.Name
			}
			groups[key] = append(groups[key], row)
		}

		divNames := make([]string, 0, len(groups))
		for name := range groups {
			divNames = append(divNames, name)
		}
		sort.Strings(divNames)

		result := awardResult{Award: def, Groups: []awardGroup{}, Diagnosis: diag, Diagnosed: diag != nil}
		for _, name := range divNames {
			groupRows := groups[name]
			sort.Slice(groupRows, func(i, j int) bool {
				return awardRowLess(groupRows[i], groupRows[j])
			})

			// Dense ranking: co-winners (tied on the primary score and
			// every tie-break value) share a rank, and the next distinct
			// rank is the previous rank plus one.
			complete := make([]awardEntry, 0, len(groupRows))
			rank := 0
			for i, row := range groupRows {
				if i == 0 || !awardRowRankEqual(groupRows[i-1], row) {
					rank++
				}
				complete = append(complete, awardEntry{
					Team:  row.team,
					Score: row.primary,
					Rank:  rank,
				})
			}

			label := overallGroupLabel
			if def.DivisionAware {
				label = name
				if label == "" {
					label = "Unassigned"
				}
			}
			result.Groups = append(result.Groups, newAwardGroup(def, label, complete))
		}
		out = append(out, result)
	}

	return out, nil
}

// newAwardGroup assembles an award group from a ranking already in
// order: Podium is the entries within the award's inline place count
// (co-winners included), Complete the full ranking, and More reports
// whether Complete extends beyond the podium.
func newAwardGroup(def Award, label string, complete []awardEntry) awardGroup {
	podiumSize := def.podiumSize()
	podium := []awardEntry{}
	for _, entry := range complete {
		if entry.Rank <= podiumSize {
			podium = append(podium, entry)
		}
	}
	return awardGroup{
		Label:    label,
		Podium:   podium,
		Complete: complete,
		More:     len(complete) > len(podium),
	}
}

func (m *Module) uiViewAwards(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	results, err := m.computeAwards(ctx)
	if err != nil {
		m.internalError(w, r, err)
		return
	}

	hasWinners := false
	for _, res := range results {
		for _, g := range res.Groups {
			if len(g.Complete) > 0 {
				hasWinners = true
				break
			}
		}
		if hasWinners {
			break
		}
	}

	m.ws.DoTemplate(w, r, "views/best/awards.p2", pongo2.Context{
		"awards":     results,
		"hasWinners": hasWinners,
	})
}
