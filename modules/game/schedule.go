package game

import (
	"context"
	"encoding/csv"
	"fmt"
	"log/slog"
	"net/http"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/schedgen"
)

func (m *Module) uiViewPhaseList(w http.ResponseWriter, r *http.Request) {
	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	scheduleAvailable := make(map[uint]bool)
	for _, phase := range phases {
		tmp, err := gorm.G[MatchPlacement](m.db.DB).
			Where("phase_id = ?", phase.ID).
			Find(r.Context())
		scheduleAvailable[phase.ID] = (len(tmp) > 0) && (err == nil)
	}

	phaseComplete, err := m.phaseCompletionStates(r.Context(), phases)
	if err != nil {
		slog.Error("Error determining phase completion", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	teams, err := m.tm.ListTeams(r.Context(), team.Team{})
	if err != nil {
		slog.Error("Error loading team roster", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// This has to be a second pass to ensure that all the phasing
	// information is populated.  This could be pushed down into
	// the database by a more skilled programmer.
	canSchedule := make(map[uint]bool)
	cannotScheduleMsg := make(map[uint]string)
	for _, phase := range phases {
		verdict, err := m.phaseSchedulable(r.Context(), phase, phases, phaseComplete, teams)
		if err != nil {
			slog.Debug("Could not load advancement filters for phase", "phase", phase.ID, "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
		canSchedule[phase.ID] = verdict.CanRun
		if !verdict.CanRun {
			// Joined in Go: pongo2 only autoescapes string-kinded
			// values, so the reasons travel as one flat string.
			cannotScheduleMsg[phase.ID] = strings.Join(verdict.Reasons, "; ")
		}
	}

	whenActive, whenMsg, err := m.whenStates(r.Context(), phases, phaseComplete, teams)
	if err != nil {
		slog.Error("Error evaluating phase When conditions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Only the next phase up gets a Generate Schedule control: a phase
	// still waiting behind an unfrozen source has nothing to explain,
	// because the source is itself the next thing to do.  Suppressed
	// phases are skipped too: they can never receive a control (the
	// template withholds it), so stopping at one would strand every
	// phase behind it with no button at all.
	nextPhase := make(map[uint]bool)
	sequence, err := m.phaseSequence(r.Context(), phases)
	if err != nil {
		slog.Error("Error ordering phases", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	for _, phase := range sequence {
		if scheduleAvailable[phase.ID] || phase.Frozen || !whenActive[phase.ID] {
			continue
		}
		nextPhase[phase.ID] = true
		break
	}

	ctx := pongo2.Context{
		"phases":            phases,
		"completed":         phaseComplete,
		"schedule":          scheduleAvailable,
		"canSchedule":       canSchedule,
		"cannotScheduleMsg": cannotScheduleMsg,
		"nextPhase":         nextPhase,
		"whenActive":        whenActive,
		"whenMsg":           whenMsg,
	}

	m.ws.DoTemplate(w, r, "views/game/phases.p2", ctx)
}

// scheduleGate enforces that a phase's advancement filters can run
// before allowing a schedule action, closing the vector of hitting the
// select, preview, or accept endpoints directly while the generation
// parameters are unmet.  The override parameter lets an operator
// through to the manual team picker when the automatic selection cannot
// run; it travels with the operator through the preview and accept
// posts, so it is read from the form as well as the query string.  It
// renders the blocked page and reports true when the request should be
// aborted; false when the action may proceed.
func (m *Module) scheduleGate(w http.ResponseWriter, r *http.Request, phase GamePhase, teams []team.Team) bool {
	if r.FormValue("override") != "" {
		return false
	}

	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading phases for schedule gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return true
	}
	phaseComplete, err := m.phaseCompletionStates(r.Context(), phases)
	if err != nil {
		slog.Error("Error determining phase completion for schedule gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return true
	}
	verdict, err := m.phaseSchedulable(r.Context(), phase, phases, phaseComplete, teams)
	if err != nil {
		slog.Error("Error evaluating advancement readiness", "phase", phase.ID, "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return true
	}
	if verdict.CanRun {
		return false
	}

	slog.Info("Generation parameters unmet, blocking schedule action",
		"phase", phase.ID,
		"reasons", verdict.Reasons,
	)
	m.ws.DoTemplate(w, r, "views/game/schedule_blocked.p2", pongo2.Context{
		"phase":   phase,
		"reasons": verdict.Reasons,
		// The gate fires on select-teams, preview, and accept alike,
		// so the escape hatch always returns to the start of the
		// flow: the team picker, with the manual selection unlocked.
		"backPhases": path.Join(m.basePath, "schedule"),
		"overrideLink": fmt.Sprintf("%s?st=%s&override=1",
			path.Join(m.basePath, "schedule", "phases", strconv.Itoa(int(phase.ID)), "select-teams"),
			phase.ScheduleType,
		),
	})
	return true
}

// scheduleRow is one match row of the schedule template
// (views/game/schedule.p2).  Placements is keyed "<fieldID>-<positionID>".
type scheduleRow struct {
	Round      int
	Match      int
	Placements map[string]team.Team
	Completed  bool
}

// scheduleSlot is one team's slot in a match, the input to
// buildScheduleRows.  Both the onsite schedule (MatchPlacement) and the
// cloud schedule (CloudMatchPlacement) map their rows to slots so they
// render identically.
type scheduleSlot struct {
	Round      int
	Match      int
	FieldID    uint
	PositionID uint
	Team       team.Team
	State      MatchState
}

// buildScheduleRows groups slots into one row per match, in match
// order.  Rounds are stored zero-based and displayed one-based.  A
// match is Completed when every one of its slots is in a terminal
// state (complete, no show, or disqualified).
func buildScheduleRows(slots []scheduleSlot) []scheduleRow {
	sorted := make([]scheduleSlot, len(slots))
	copy(sorted, slots)
	sort.SliceStable(sorted, func(i, j int) bool {
		a, b := sorted[i], sorted[j]
		if a.Match != b.Match {
			return a.Match < b.Match
		}
		if a.FieldID != b.FieldID {
			return a.FieldID < b.FieldID
		}
		return a.PositionID < b.PositionID
	})

	rows := []scheduleRow{}
	for _, s := range sorted {
		if n := len(rows); n == 0 || rows[n-1].Match != s.Match {
			rows = append(rows, scheduleRow{
				Round:      s.Round + 1,
				Match:      s.Match,
				Placements: make(map[string]team.Team),
				Completed:  true,
			})
		}
		row := &rows[len(rows)-1]
		row.Placements[fmt.Sprintf("%d-%d", s.FieldID, s.PositionID)] = s.Team
		if s.State != MatchStateComplete && s.State != MatchStateNoShow && s.State != MatchStateDisqualified {
			row.Completed = false
		}
	}
	return rows
}

func (m *Module) uiViewPhaseSchedule(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	fields, err := gorm.G[Field](m.db.DB).
		Preload("Divisions", nil).
		Where("id in (select distinct(field_id) from match_placements where phase_id = ?)", gPhase).
		Find(r.Context())
	if err != nil {
		slog.Error("Error loading fields", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].ID < fields[j].ID
	})

	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Team.Division", nil).
		Preload("Field", nil).
		Preload("Position", nil).
		Where(&MatchPlacement{PhaseID: gPhase}).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving match placements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	slots := make([]scheduleSlot, 0, len(placements))
	for _, p := range placements {
		slots = append(slots, scheduleSlot{
			Round:      p.Round,
			Match:      p.Match,
			FieldID:    p.FieldID,
			PositionID: p.PositionID,
			Team:       p.Team,
			State:      p.State,
		})
	}
	schedule := buildScheduleRows(slots)

	phase, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Filter completed matches from the schedule display unless ?all
	// is set. Only the active phase is filtered; frozen phases always
	// render their full schedule.
	showAll := strings.ToLower(r.URL.Query().Get("all")) != ""
	if !showAll && phase.Active {
		filtered := []scheduleRow{}
		for _, row := range schedule {
			if !row.Completed {
				filtered = append(filtered, row)
			}
		}
		schedule = filtered
	}

	ctx := pongo2.Context{
		"phase":     phase,
		"fields":    fields,
		"positions": positions,
		"schedule":  schedule,
	}

	switch r.URL.Query().Get("format") {
	case "csv":
		cw := csv.NewWriter(w)

		for _, row := range schedule {
			cFields := []string{}
			for _, field := range fields {
				for _, position := range positions {
					cFields = append(cFields, row.Placements[fmt.Sprintf("%d-%d", field.ID, position.ID)].Name)
				}
			}
			slog.Debug("Schedule CSV", "row", cFields)
			if err := cw.Write(cFields); err != nil {
				slog.Error("Error writing CSV", "error", err)
			}
		}
		cw.Flush()

	default:
		m.ws.DoTemplate(w, r, "views/game/schedule.p2", ctx)
	}
}

func (m *Module) uiViewPhaseScoreStats(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	phase, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	stats, err := m.PhaseScoreStats(r.Context(), gPhase)
	if err != nil {
		slog.Error("Error computing phase score stats", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"phase":        phase,
		"stats":        stats,
		"backSchedule": path.Join(m.basePath, "schedule", "phases", fmt.Sprint(gPhase)),
		"backPhases":   path.Join(m.basePath, "schedule"),
	}

	m.ws.DoTemplate(w, r, "views/game/phase_score_stats.p2", ctx)
}

// PhaseRanking is the ranked scoreboard of a single phase for a single
// division: the whole field for a phase that is not division aware
// (Division is then the empty string), or one division of a division
// aware phase.  Ranks restart at 1 in each division.
type PhaseRanking struct {
	PhaseID   uint
	PhaseName string
	Role      PhaseRole
	Division  string
	Rows      []RankingRow
}

// phaseRankingForPhase computes the ranking tables for one phase: a
// single table for a phase that is not division aware, or one table
// per division that has ranked teams for a division aware phase.
func (m *Module) phaseRankingForPhase(ctx context.Context, phase GamePhase) ([]PhaseRanking, error) {
	divisions, err := m.whenDivisions(ctx, phase)
	if err != nil {
		return nil, err
	}

	tables := []PhaseRanking{}
	for _, division := range divisions {
		rows, err := m.scoreboardRankings(ctx, phase.ID, division)
		if err != nil {
			return nil, err
		}
		if phase.DivisionAware && len(rows) == 0 {
			continue
		}
		tables = append(tables, PhaseRanking{
			PhaseID:   phase.ID,
			PhaseName: phase.Name,
			Role:      phase.Role,
			Division:  division,
			Rows:      rows,
		})
	}
	return tables, nil
}

// uiViewPhaseRankings shows the final scoreboard rankings of a frozen
// phase, ranked by the phase's configured score summation and
// tie-breaker, without pagination.  A division aware phase gets one
// table per division that has ranked teams.
func (m *Module) uiViewPhaseRankings(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	phase, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if !phase.Frozen {
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": fmt.Errorf("phase %q is not frozen", phase.Name)})
		return
	}

	tables, err := m.phaseRankingForPhase(r.Context(), phase)
	if err != nil {
		slog.Error("Error computing phase rankings", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"phase":        phase,
		"tables":       tables,
		"backSchedule": path.Join(m.basePath, "schedule", "phases", fmt.Sprint(gPhase)),
		"backPhases":   path.Join(m.basePath, "schedule"),
	}

	m.ws.DoTemplate(w, r, "views/game/phase_rankings.p2", ctx)
}

func (m *Module) uiViewPhaseMakeActive(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[GamePhase](tx).
			Where(&GamePhase{Active: true}).
			Update(r.Context(), "Active", false)
		if err != nil {
			slog.Error("Error deactivating all schedule phases", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		_, err = gorm.G[GamePhase](tx).
			Where(&GamePhase{ID: gPhase}).
			Update(r.Context(), "Active", true)
		if err != nil {
			slog.Error("Error activating schedule phase", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		return nil
	})
	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}

func (m *Module) uiViewPhaseMakeFrozen(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	_, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		Update(r.Context(), "Frozen", true)
	if err != nil {
		slog.Error("Error freezing schedule phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}

func (m *Module) uiViewPhaseToggleScoresVisibility(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	hide := strings.HasSuffix(r.URL.Path, "make-scores-hidden")

	_, err := gorm.G[GamePhase](m.db.DB).
		Where(&GamePhase{ID: gPhase}).
		Update(r.Context(), "HideScores", hide)
	if err != nil {
		slog.Error("Error toggling hide scores", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}

func (m *Module) uiViewPhaseScheduleSelectTeams(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))
	phase, err := gorm.G[GamePhase](m.db.DB).
		Preload("AdvancementFilters", nil).
		Where(&GamePhase{ID: gPhase}).
		First(r.Context())
	if err != nil {
		slog.Error("Error loading phase for schedule parameter selection", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	slog.Debug("Selected game phase", "phase", phase)

	teams, err := m.tm.ListTeams(r.Context(), team.Team{})
	if err != nil {
		slog.Error("Error retreiving field positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if m.whenGate(w, r, phase, teams) {
		return
	}
	if m.scheduleGate(w, r, phase, teams) {
		return
	}

	fields, err := m.ListFields(r.Context(), Field{})
	if err != nil {
		slog.Error("Error loading fields", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	scfg, err := schedgen.GetConfig(r.URL.Query().Get("st"))
	if err != nil {
		slog.Error("Error getting scheduler config", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	ctx := pongo2.Context{
		"phase":         phase,
		"teams":         teams,
		"manualEnabled": true,
		"fields":        fields,
		"scheduletype":  r.URL.Query().Get("st"),
		"schedulemax":   scfg.MaxRounds(),
		"scheduledef":   scfg.DefaultRounds(),
	}

	advancingTeams := make(map[uint]struct{})
	if len(phase.AdvancementFilters) > 0 {
		ctx["manualEnabled"] = false

		// A blank division will result in the advancement
		// filters being called with an empty filter, which
		// will select all teams across all divisions.
		divisionNames := []string{""}
		if phase.DivisionAware {
			var divisions []team.Division
			if res := m.db.Raw().Model(&team.Division{}).Find(&divisions); res.Error != nil {
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": res.Error})
				return
			}
			divisionNames = make([]string, len(divisions))
			for i, d := range divisions {
				divisionNames[i] = d.Name
			}
		}

		determinations := []AdvancementDeterminationResult{}

		// Phases and completion states feed the When context for
		// filter-level When expressions.
		phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
		if err != nil {
			slog.Error("Error loading phases for filter When", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
		phaseComplete, err := m.phaseCompletionStates(r.Context(), phases)
		if err != nil {
			slog.Error("Error determining phase completion for filter When", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}

		for _, division := range divisionNames {
			adv, dets, err := m.runAdvancementFilters(r.Context(), phase, phases, phaseComplete, division, teams)
			if err != nil {
				slog.Error("Error running advancement filters", "phase", phase.ID, "division", division, "error", err)
				m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
				return
			}
			for id := range adv {
				advancingTeams[id] = struct{}{}
			}
			determinations = append(determinations, dets...)
		}
		ctx["advancingTeams"] = advancingTeams
		ctx["advancementReasons"] = determinations
	}
	// The override parameter is the operator's escape hatch when the
	// automatic selection cannot run: it unlocks the team picker so a
	// schedule can be built by hand.
	if r.URL.Query().Get("override") != "" {
		ctx["manualEnabled"] = true
	}

	m.ws.DoTemplate(w, r, "views/game/schedule_select_teams.p2", ctx)
}

func (m *Module) uiViewPhaseSchedulePreview(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	phaseID := m.ws.StrToUint(chi.URLParam(r, "id"))
	phase, err := gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: phaseID}).First(r.Context())
	if err != nil {
		slog.Error("Error loading phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	gateTeams, err := m.tm.ListTeams(r.Context(), team.Team{})
	if err != nil {
		slog.Error("Error loading teams for When gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if m.whenGate(w, r, phase, gateTeams) {
		return
	}
	if m.scheduleGate(w, r, phase, gateTeams) {
		return
	}

	fields := []Field{}
	for _, f := range r.Form["fields"] {
		field, err := m.ListFields(r.Context(), Field{ID: m.ws.StrToUint(f)})
		if err != nil {
			continue
		}
		if len(field) > 0 {
			fields = append(fields, field[0])
		}
	}
	sort.Slice(fields, func(i, j int) bool {
		return fields[i].ID < fields[j].ID
	})

	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error loading positions", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	teams := []team.Team{}
	for _, t := range r.Form["selected_teams"] {
		team, err := m.tm.ListTeams(r.Context(), team.Team{ID: m.ws.StrToUint(t)})
		if err != nil {
			slog.Error("Error loading team", "error", err)
			continue
		}
		teams = append(teams, team...)
	}
	sort.Slice(teams, func(i, j int) bool {
		return teams[i].ID < teams[j].ID
	})

	c := schedgen.Config{
		Fields:    len(fields),
		Positions: len(positions),
		Teams:     len(teams),
		Rounds:    int(m.ws.StrToUint(r.FormValue("rounds"))),
	}

	var s *schedgen.Schedule
	if phase.DivisionAware {
		s, err = generateDivisionSchedule(r, m.db.DB, c, fields, teams, positions)
	} else {
		s, err = schedgen.GenerateSchedule(r.FormValue("schedule_type"), c)
	}
	if err != nil {
		slog.Error("Error generating schedule", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	s.Score()
	if err := s.Validate(); err != nil {
		slog.Error("Error generating schedule", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	if _, err := gorm.G[MatchPlacement](m.db.DB).Where(&MatchPlacement{PhaseID: CandidatePhase}).Delete(r.Context()); err != nil {
		slog.Error("Error clearing candidate match", "error", err)
	}

	cid := m.selectedContestID(r.Context())

	for rNum, round := range s.Rounds {
		for mNum, match := range round.Matches {
			for fNum, field := range fields {
				for pNum, position := range positions {
					tID := match.Team(fNum, pNum)
					if tID < 0 {
						continue
					}
					t := teams[tID]
					mp := MatchPlacement{
						Round:      rNum,
						Match:      mNum + rNum*len(round.Matches) + 1,
						PhaseID:    CandidatePhase,
						TeamID:     t.ID,
						FieldID:    field.ID,
						PositionID: position.ID,
						State:      MatchStateScheduled,
						ContestID:  cid,
					}

					if err := gorm.G[MatchPlacement](m.db.DB).Create(r.Context(), &mp); err != nil {
						slog.Error("Error saving match placement", "error", err)
						continue
					}
				}
			}
		}
	}

	ctx := pongo2.Context{
		"schedule":  s,
		"teams":     teams,
		"fields":    fields,
		"positions": positions,
		// The accept form posts to a relative URL, which drops the
		// query string, so the override has to travel as a form field
		// for it to reach the accept gate.
		"override": r.FormValue("override"),
	}

	m.ws.DoTemplate(w, r, "views/game/schedule_preview.p2", ctx)
}

func generateDivisionSchedule(r *http.Request, db *gorm.DB, c schedgen.Config, fields []Field, teams []team.Team, positions []FieldPosition) (*schedgen.Schedule, error) {
	_ = db
	_ = positions

	// Build divisionFields map: divisionID -> []0-based field indices
	divisionFields := make(map[uint][]int)
	for i, f := range fields {
		for _, div := range f.Divisions {
			divisionFields[div.ID] = append(divisionFields[div.ID], i)
		}
	}

	// Build divisionTeams map: divisionID -> []0-based team indices
	divisionTeams := make(map[uint][]int)
	for i, t := range teams {
		if t.DivisionID > 0 {
			divisionTeams[t.DivisionID] = append(divisionTeams[t.DivisionID], i)
		}
	}

	// Build divisionNoCompact map: divisionID -> NoCompact flag
	divisionNoCompact := make(map[uint]bool)
	for _, t := range teams {
		if t.DivisionID > 0 {
			divisionNoCompact[t.DivisionID] = t.Division.NoCompact
		}
	}

	// Build DivisionConfig slice for all divisions that have teams.
	// Fields may be empty (auto-assigned later) or explicitly pinned.
	divConfigs := make([]schedgen.DivisionConfig, 0, len(divisionTeams))
	for divID, teamIndices := range divisionTeams {
		divConfigs = append(divConfigs, schedgen.DivisionConfig{
			ID:          int(divID),
			Fields:      divisionFields[divID],
			Rounds:      0, // Use global rounds
			TeamIndices: teamIndices,
			NoCompact:   divisionNoCompact[divID],
		})
	}

	// Sort for deterministic output
	sort.Slice(divConfigs, func(i, j int) bool {
		return divConfigs[i].ID < divConfigs[j].ID
	})

	// Fallback to flat scheduling if no division configs
	if len(divConfigs) == 0 {
		return schedgen.GenerateSchedule(r.FormValue("schedule_type"), c)
	}

	return schedgen.GenerateDivisionSchedule(r.FormValue("schedule_type"), c, divConfigs)
}

func (m *Module) uiViewPhaseScheduleAccept(w http.ResponseWriter, r *http.Request) {
	gPhase := m.ws.StrToUint(chi.URLParam(r, "id"))

	phase, err := gorm.G[GamePhase](m.db.DB).Where(&GamePhase{ID: gPhase}).First(r.Context())
	if err != nil {
		slog.Error("Error loading phase", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	teams, err := m.tm.ListTeams(r.Context(), team.Team{})
	if err != nil {
		slog.Error("Error loading teams for When gate", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	if m.whenGate(w, r, phase, teams) {
		return
	}
	if m.scheduleGate(w, r, phase, teams) {
		return
	}

	m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[MatchPlacement](tx).
			Where(&MatchPlacement{PhaseID: CandidatePhase}).
			Update(r.Context(), "PhaseID", gPhase)
		if err != nil {
			slog.Error("Error updating schedule phase", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		_, err = gorm.G[GamePhase](tx).
			Where(&GamePhase{Active: true}).
			Update(r.Context(), "Active", false)
		if err != nil {
			slog.Error("Error deactivating all schedule phases", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		_, err = gorm.G[GamePhase](tx).
			Where(&GamePhase{ID: gPhase}).
			Update(r.Context(), "Active", true)
		if err != nil {
			slog.Error("Error activating schedule phase", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return err
		}

		return nil
	})

	http.Redirect(w, r, path.Join(m.basePath, "schedule"), http.StatusSeeOther)
}
