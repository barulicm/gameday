package game

import (
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/expr-lang/expr"
	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/pkg/web"
)

func (m *Module) uiViewScorecardList(w http.ResponseWriter, r *http.Request) {
	// This function is quite complicated.  It needs to do 3 basic
	// things based on what the user selected.  It needs to fill
	// in data that will be used to fill in the filter form, it
	// needs to parse that form and optionally cookie the user,
	// and it needs to actually consume the filters to figure out
	// what scorecards to fetch.

	type filterParams struct {
		Phases    []uint
		Fields    []uint
		Positions []uint
		States    []MatchState
	}

	// Build up the elements for the filter form.
	phases, err := gorm.G[GamePhase](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	fields, err := m.ListFields(r.Context(), Field{})
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	positions, err := gorm.G[FieldPosition](m.db.DB).Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Work out what from the form was selected
	r.ParseForm()
	// The default filter must own a copy of the state list: json.Unmarshal
	// into a non-nil slice reuses its backing array, so unmarshalling a
	// saved-filter cookie straight into the package-level MatchStates
	// would permanently corrupt it (losing states from every later page)
	// for the life of the process.
	filter := filterParams{States: slices.Clone(MatchStates)}
	for _, phase := range phases {
		filter.Phases = append(filter.Phases, phase.ID)
	}
	for _, field := range fields {
		filter.Fields = append(filter.Fields, field.ID)
	}
	for _, position := range positions {
		filter.Positions = append(filter.Positions, position.ID)
	}

ParseFilter:
	switch r.FormValue("filter_action") {
	case "clear":
		http.SetCookie(w, &http.Cookie{
			Name:    "scorecard_filters",
			Value:   "",
			Expires: time.Now(),
			MaxAge:  0,
		})
	case "save":
		filter.Phases = m.ws.ParseUintSlice(r.Form["phase_id"])
		filter.Fields = m.ws.ParseUintSlice(r.Form["field_id"])
		filter.Positions = m.ws.ParseUintSlice(r.Form["position_id"])

		filter.States = []MatchState{}
		for _, state := range m.ws.ParseUintSlice(r.Form["state"]) {
			filter.States = append(filter.States, MatchState(state))
		}

		j, _ := json.Marshal(filter)
		http.SetCookie(w, &http.Cookie{
			Name:    "scorecard_filters",
			Value:   url.QueryEscape(string(j)),
			Expires: time.Now().Add(time.Hour * 24),
		})
	default:
		// Try to recover the filters from the cookie, if it exists.
		c, err := r.Cookie("scorecard_filters")
		if err == http.ErrNoCookie {
			break ParseFilter
		}
		v, _ := url.QueryUnescape(c.Value)
		if err := json.Unmarshal([]byte(v), &filter); err != nil {
			slog.Warn("Invalid but present filter cookie", "error", err)
		}
	}

	slog.Debug("Filter configuration", "filter", filter)

	// Finally select all MatchPlacements that match the given
	// filters, which in turn results in a list of scorecards
	// matching those match placements.
	placements, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Phase", nil).
		Preload("Team", nil).
		Preload("Field", nil).
		Preload("Position", nil).
		Where("phase_id in ?", filter.Phases).
		Where("field_id in ?", filter.Fields).
		Where("position_id in ?", filter.Positions).
		Where("state in ?", filter.States).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Attach each placement's finalized score, when it has one, so the
	// list shows the same Score column the cloud lookup page does. The
	// scores are loaded in one query and keyed by placement rather than
	// preloaded, because MatchScore points at the placement, not the
	// other way around. Whether a row has a score at all is decided by
	// the placement's state, not by this map, so both pages agree on
	// which matches are still unscored.
	scoreByID := make(map[uint]int)
	if len(placements) > 0 {
		placementIDs := make([]uint, 0, len(placements))
		for _, p := range placements {
			placementIDs = append(placementIDs, p.ID)
		}
		scores, err := gorm.G[MatchScore](m.db.DB).
			Where("match_placement_id IN ?", placementIDs).
			Find(r.Context())
		if err != nil {
			slog.Error("Error retreiving match scores", "error", err)
			m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
			return
		}
		for _, s := range scores {
			scoreByID[s.MatchPlacementID] = s.Score
		}
	}

	rows := make([]scorecardListRow, 0, len(placements))
	for _, p := range placements {
		rows = append(rows, scorecardListRow{
			Placement: p,
			Score:     scoreByID[p.ID],
			Scored:    p.State.Scored(),
		})
	}

	ctx := pongo2.Context{
		"available_phases":    phases,
		"available_fields":    fields,
		"available_positions": positions,
		"available_states":    MatchStates,
		"filter":              filter,
		"rows":                rows,
	}

	m.ws.DoTemplate(w, r, "views/game/scorecard_list.p2", ctx)
}

// scorecardListRow is one table row on the onsite scorecard list page:
// a match placement plus its finalized score. Score is only meaningful
// when Scored is true; the page shows a dash otherwise. Scored comes
// from the match state, exactly as the cloud lookup page decides it.
type scorecardListRow struct {
	Placement MatchPlacement
	Score     int
	Scored    bool
}

func (m *Module) uiViewScorecard(w http.ResponseWriter, r *http.Request) {
	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	placement, err := gorm.G[MatchPlacement](m.db.DB).
		Preload("Phase", nil).
		Preload("Team", nil).
		Preload("Field", nil).
		Preload("Position", nil).
		Where(placementFilter).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	elements, err := gorm.G[GameElement](m.db.DB).
		Preload("States", nil).
		Preload("States.Values", nil).
		Find(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	sce, err := gorm.G[ScorecardValue](m.db.DB).
		Where(&ScorecardValue{MatchPlacementID: placement.ID}).Find(r.Context())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		slog.Error("Error retreiving scorecard elements", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}
	scd := make(map[string]int)
	for _, e := range sce {
		scd[e.Element] = e.Value
	}

	slog.Debug("scorecard data", "mi", placement.Match, "data", scd)

	profile, _ := r.Context().Value(web.ProfileKey{}).(web.Profile)

	ctx := pongo2.Context{
		"MatchInfo":     placement,
		"Elements":      elements,
		"ScorecardData": scd,
		"edit_disable":  !profile.HasPermission(web.Permission{Module: ModuleName, Grant: PermissionReferee}),
	}

	m.ws.DoTemplate(w, r, "views/game/scorecard.p2", ctx)
}

func (m *Module) uiViewScorecardSubmit(w http.ResponseWriter, r *http.Request) {
	r.ParseForm()

	placementFilter := MatchPlacement{
		PhaseID:    m.ws.StrToUint(chi.URLParam(r, "phase")),
		Match:      int(m.ws.StrToUint(chi.URLParam(r, "match"))),
		FieldID:    m.ws.StrToUint(chi.URLParam(r, "field")),
		PositionID: m.ws.StrToUint(chi.URLParam(r, "position")),
	}
	placement, err := gorm.G[MatchPlacement](m.db.DB).
		Where(placementFilter).
		First(r.Context())
	if err != nil {
		slog.Error("Error retreiving scorecard", "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	err = m.db.Transaction(func(tx *gorm.DB) error {
		_, err := gorm.G[ScorecardValue](tx).
			Where(&ScorecardValue{MatchPlacementID: placement.ID}).
			Delete(r.Context())
		if err != nil {
			return fmt.Errorf("clearing previous scorecard values: %w", err)
		}

		_, err = gorm.G[MatchScore](tx).
			Where(&MatchScore{ID: placement.ID}).
			Delete(r.Context())
		if err != nil {
			return fmt.Errorf("clearing previous match score: %w", err)
		}

		vMap := make(map[string]int)
		for key := range r.Form {
			slog.Debug("form value", "key", key, "value", r.FormValue(key))

			v, err := strconv.Atoi(r.FormValue(key))
			if err != nil {
				return fmt.Errorf("invalid value %q for %s: %w", r.FormValue(key), key, err)
			}
			scv := ScorecardValue{
				MatchPlacementID: placement.ID,
				Element:          key,
				Value:            v,
			}
			vMap[key] = v

			if err := gorm.G[ScorecardValue](tx).Create(r.Context(), &scv); err != nil {
				return fmt.Errorf("saving scorecard value %s: %w", key, err)
			}
		}

		elements, err := gorm.G[ScorecardElement](tx).Find(r.Context())
		if err != nil {
			return fmt.Errorf("fetching scorecard elements: %w", err)
		}
		exprFragments := make([]string, len(elements))
		for i := range elements {
			exprFragments[i] = elements[i].Expr
		}

		result, err := expr.Eval(strings.Join(exprFragments, " + "), vMap)
		if err != nil {
			return fmt.Errorf("evaluating scorecard: %w", err)
		}
		score, ok := result.(int)
		if !ok {
			return fmt.Errorf("evaluating scorecard: score is %T (%v), want an integer", result, result)
		}
		err = gorm.G[MatchScore](tx).Create(r.Context(), &MatchScore{
			ID:               placement.ID,
			MatchPlacementID: placement.ID,
			GamePhaseID:      placement.PhaseID,
			TeamID:           placement.TeamID,
			Score:            score,
			ContestID:        placement.ContestID,
		})
		if err != nil {
			return fmt.Errorf("saving match score: %w", err)
		}

		_, err = gorm.G[MatchPlacement](tx).
			Where(&MatchPlacement{ID: placement.ID}).
			Update(r.Context(), "state", MatchStateComplete)
		if err != nil {
			return fmt.Errorf("finalizing match: %w", err)
		}

		slog.Info("Scorecard Evaluated", "score", score)
		return nil
	})
	if err != nil {
		// The transaction rolled back, so nothing was saved.  Show
		// the referee the error rather than redirecting as though
		// the scorecard had been accepted.
		slog.Error("Error saving scorecard", "placement", placement.ID, "error", err)
		m.ws.DoTemplate(w, r, "errors/internal.p2", pongo2.Context{"error": err})
		return
	}

	// Best effort: the score is saved locally either way, and the
	// push runs in the background so the referee is not kept
	// waiting on the cloud.
	m.scheduleCloudPush()

	http.Redirect(w, r, "../../../../scorecard", http.StatusSeeOther)
}
