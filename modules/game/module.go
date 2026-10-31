package game

import (
	"context"
	"embed"
	"errors"
	"io/fs"
	"path"
	"strings"
	"time"

	"github.com/flosch/pongo2/v6"
	"github.com/go-chi/chi/v5"
	"gorm.io/gorm"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/team"
	"github.com/gizmo-platform/gameday/pkg/cloud"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

const (
	CandidatePhase = 99

	ModuleName = "GAME"

	PermissionAdmin       = "ADMIN"
	PermissionSchedule    = "SCHEDULE"
	PermissionReferee     = "REFEREE"
	PermissionTimekeeper  = "TIMEKEEPER"
	PermissionSynchronize = "SYNCHRONIZE"
)

// MatchState is an enum of states that a match can be in, used for
// filtering.
type MatchState uint

const (
	_ = iota
	// MatchStateScheduled refers to a match that exists in the
	// schedule, and can be played.
	MatchStateScheduled MatchState = iota

	// MatchStateQueued is used when a team is present in queuing
	// and is ready to play.
	MatchStateQueued

	// MatchStateRunning is used to identify when the clock is
	// running on a given match, and is primarily used to drive
	// external automations.
	MatchStateRunning

	// MatchStateScorable is defined as a match that is able to be
	// scored and is not in an otherwise terminal state.
	MatchStateScorable

	// MatchStateComplete is used to identify a match that is done
	// and does not require further scorecard manipulation.
	MatchStateComplete

	// MatchStateNoShow is used to identify a match placement
	// where the team never made it to the match, and forces a
	// zero score.
	MatchStateNoShow

	// MatchStateDisqualified is used to identify a team that may
	// have made it to a match, potentially was scored, but was
	// then disqualified from earning any points during the match
	// in question.
	MatchStateDisqualified
)

var (
	MatchStates = []MatchState{
		MatchStateScheduled,
		MatchStateQueued,
		MatchStateRunning,
		MatchStateScorable,
		MatchStateComplete,
		MatchStateNoShow,
		MatchStateDisqualified,
	}
)

func (m MatchState) String() string {
	switch m {
	case MatchStateScheduled:
		return "scheduled"
	case MatchStateQueued:
		return "queued"
	case MatchStateRunning:
		return "running"
	case MatchStateScorable:
		return "scorable"
	case MatchStateComplete:
		return "complete"
	case MatchStateNoShow:
		return "no-show"
	case MatchStateDisqualified:
		return "disqualified"
	default:
		return "unknown"
	}
}

// Scored reports whether a match in this state has a definitive score
// to show, which is what the scorecard listings use to decide between
// a number and a dash. A scorecard submit writes its MatchScore row and
// flips the placement to MatchStateComplete in the same transaction, so
// Complete carries the submitted total; a no-show and a disqualification
// are settled results too, worth a forced zero. Every earlier state is
// still in flight, so its score is not known yet.
func (m MatchState) Scored() bool {
	switch m {
	case MatchStateComplete, MatchStateNoShow, MatchStateDisqualified:
		return true
	default:
		return false
	}
}

// GamePhaseAdvancementFilterMode is used to identify whether a filter
// is an include filter or an exclude filter.  Exclude filters are
// processed first to prune the list of teams that may be advanced,
// and then include filters are used to select from the teams that
// remain.
type GamePhaseAdvancementFilterMode uint8

const (
	// GamePhaseAdvancementFilterModeInclude is used to identify
	// an include filter that will match and return teams.
	GamePhaseAdvancementFilterModeInclude GamePhaseAdvancementFilterMode = iota

	// GamePhaseAdvancementFilterModeExclude is used to identify a
	// filter that is used to exclude teams from consideration for
	// advancement, possibly because they have already been
	// advanced via a parallel path.
	GamePhaseAdvancementFilterModeExclude
)

var (
	GamePhaseAdvancementFilterModes = []GamePhaseAdvancementFilterMode{
		GamePhaseAdvancementFilterModeInclude,
		GamePhaseAdvancementFilterModeExclude,
	}
)

func (gmpf GamePhaseAdvancementFilterMode) String() string {
	switch gmpf {
	case GamePhaseAdvancementFilterModeInclude:
		return "include"
	case GamePhaseAdvancementFilterModeExclude:
		return "exclude"
	default:
		return "unknown"
	}
}

func (gmpf GamePhaseAdvancementFilterMode) MarshalText() ([]byte, error) {
	return []byte(gmpf.String()), nil
}

func (gmpf *GamePhaseAdvancementFilterMode) UnmarshalText(b []byte) error {
	switch strings.ToLower(string(b)) {
	case "include":
		*gmpf = GamePhaseAdvancementFilterModeInclude
	case "exclude":
		*gmpf = GamePhaseAdvancementFilterModeExclude
	default:
		return errors.New("mode must be 'include' or 'exclude'")
	}
	return nil
}

//go:embed ui/*
var efs embed.FS

// Field represents a field of play, and has at least one position on
// which a team can be placed.
type Config struct {
	Field ConfigField `yaml:"Field"`
	Game  Game        `yaml:"Game"`

	// Clock is an optional top-level clock configuration section in
	// the game setup file.
	Clock ClockConfig `yaml:"Clock"`
}

// ClockConfig holds the configurable parameters of the match clock
// as declared in the game setup file.
type ClockConfig struct {
	// Duration is the total length of a clock run.
	Duration time.Duration `yaml:"Duration"`

	// Hurry is the window before the end of the clock that triggers
	// the hurry state.
	Hurry time.Duration `yaml:"Hurry"`

	// Transitions enables the clock driving match state transitions
	// (start/end/cancel) for the active phase.
	Transitions bool `yaml:"Transitions"`
}

type ConfigField struct {
	Positions []FieldPosition `yaml:"Positions"`
}

// A FieldPosition has a name and a pair of colors, one of which
// should be lighter than the other to allow banding in tables.
type FieldPosition struct {
	ID     uint   `yaml:"ID"`
	Name   string `yaml:"Name"`
	Color1 string `yaml:"Color1"`
	Color2 string `yaml:"Color2"`
}

// Field represents a single field that is available for scheduling.
type Field struct {
	ID        uint            `yaml:"ID"`
	Name      string          `yaml:"Name"`
	Divisions []team.Division `gorm:"many2many:field_divisions;" yaml:"Divisions"`
}

// Game defines the components related to a playable game.
type Game struct {
	Phases   []GamePhase   `yaml:"Phases"`
	Elements []GameElement `yaml:"Elements"`
}

// GamePhase is a single phase of play that is part of a larger
// schedule.
type GamePhase struct {
	ID                 uint                       `yaml:"ID"`
	Name               string                     `yaml:"Name"`
	Active             bool                       `yaml:"Active"`
	Frozen             bool                       `yaml:"Frozen"`
	AdvancementFilters []GamePhaseAdvancementFilter `yaml:"AdvancementFilters"`
	DivisionAware      bool                       `yaml:"DivisionAware"`
	ScheduleType       string                     `yaml:"ScheduleType"`
	ScoreSummation     string                     `yaml:"ScoreSummation"`
	HideScores         bool                       `yaml:"HideScores"`
	TieBreaker         string                     `yaml:"TieBreaker"`
	// Role declares the competitive purpose of the phase.  Valid
	// values are the registered phase roles: Exhibition, Seeding,
	// Semifinal and Final from the game module, plus any role another
	// module registers (the BEST module registers Wildcard).  Empty
	// means no role is assigned; LintConfig rejects an unregistered
	// value.  The column is named phase_role because role is quoted
	// inconsistently across SQL dialects in raw clauses.
	Role PhaseRole `gorm:"column:phase_role" yaml:"Role"`
	// Suppress is an optional boolean suppression condition: when it
	// evaluates to true the phase is suppressed (its Generate Schedule
	// button is hidden and schedule actions are rejected server-side).
	// An empty value means the phase is never suppressed.
	Suppress string `yaml:"Suppress"`
	// SuppressMsg is an optional message shown on the phase row while
	// the phase is suppressed.
	SuppressMsg string `yaml:"SuppressMsg"`
}

// GamePhaseAdvancementFilter captures the expressions that are used
// to count a number of teams to move phase to phase and the criteria
// on which to select them.
type GamePhaseAdvancementFilter struct {
	ID          uint                       `yaml:"ID"`
	GamePhaseID uint                       `yaml:"GamePhaseID"`
	Filter      string                     `yaml:"Filter"`
	Rule        string                     `yaml:"Rule"`
	Mode        GamePhaseAdvancementFilterMode `yaml:"Mode"`
	// SelectFrom is the ID of the phase whose scoreboard feeds this
	// filter.  A value of 0 means select from the full roster with no
	// source phase, which is used for the start of the schedule.
	SelectFrom uint   `yaml:"SelectFrom"`
	SliceExpr  string `yaml:"SliceExpr"`
	// When is an optional boolean expression gating whether this
	// filter runs at all.  It is evaluated with the same context as a
	// phase Suppress expression; an empty value means the filter always
	// runs, and a false value means the filter selects nothing.
	When string `yaml:"When"`
}

// GameElement represents a single game element that may be
// manipulated.
type GameElement struct {
	ID uint `yaml:"ID"`

	EID    string             `yaml:"EID"`
	Name   string             `yaml:"Name"`
	Desc   string             `yaml:"Desc"`
	Type   string             `yaml:"Type"`
	States []GameElementState `yaml:"States"`
}

// GameElementState represents a state that the game element may be
// in.
type GameElementState struct {
	ID            uint   `yaml:"ID"`
	GameElementID uint   `yaml:"GameElementID"`

	SID  string `yaml:"SID"`
	Name string `yaml:"Name"`
	Desc string `yaml:"Desc"`
	Each int    `yaml:"Each"`
	Max  int    `yaml:"Max"`

	Values []GameElementStateValue `yaml:"Values"`
}

// GameElementStateValue is a selectable value for game element states
// that do not use a linear scoring configuration.
type GameElementStateValue struct {
	ID                 uint   `yaml:"ID"`
	GameElementStateID uint   `yaml:"GameElementStateID"`

	Name    string `yaml:"Name"`
	VID     string `yaml:"VID"`
	Points  int    `yaml:"Points"`
	Default bool   `yaml:"Default"`
}

// ScorecardElement is a single line from a scorecard that uses the ID
// format from the scorecards.
type ScorecardElement struct {
	ID uint

	Element string
	Type    string
	Expr    string
}

// ScorecardValue is a single attribute from a scorecard that has been scored.
type ScorecardValue struct {
	ID uint

	MatchPlacement   MatchPlacement
	MatchPlacementID uint

	Element string
	Value   int
}

// MatchPlacement binds a team to a given field placement.
type MatchPlacement struct {
	ID uint

	Round   int
	Match   int
	Phase   GamePhase
	PhaseID uint

	Team       team.Team
	TeamID     uint
	Field      Field
	FieldID    uint
	Position   FieldPosition
	PositionID uint

	State MatchState

	// ContestID keys this placement to the contest it belongs to
	// (0 when no contest is selected on the onsite server).
	ContestID uint `gorm:"index"`
}

// MatchScore is used to store the actual numeric score as finalized
// from a MatchPlacement and collection of ScorecardValue elements.
type MatchScore struct {
	ID uint

	Score            int
	MatchPlacement   MatchPlacement
	MatchPlacementID uint

	GamePhase   GamePhase
	GamePhaseID uint

	Team   team.Team
	TeamID uint

	// ContestID mirrors the ContestID of the placement this score
	// belongs to (0 when no contest is selected).
	ContestID uint `gorm:"index"`
}

// CloudMatchPlacement is the cloud-side storage of a single match
// placement pushed from an onsite server, keyed by the contest ID.
// It mirrors the pkg/cloud wire shape: the cloud stores what the site
// sent, including the site's own IDs (phase, team, field, position),
// which are meaningful only within that contest.
type CloudMatchPlacement struct {
	ID uint

	ContestID  uint `gorm:"uniqueIndex:idx_cloud_placement_key,composite"`
	Round      int  `gorm:"uniqueIndex:idx_cloud_placement_key,composite"`
	Match      int  `gorm:"uniqueIndex:idx_cloud_placement_key,composite"`
	PhaseID    uint `gorm:"uniqueIndex:idx_cloud_placement_key,composite"`
	TeamID     uint
	TeamName   string
	FieldID    uint `gorm:"uniqueIndex:idx_cloud_placement_key,composite"`
	FieldName  string
	PositionID uint `gorm:"uniqueIndex:idx_cloud_placement_key,composite"`
	State      uint
	Score      int
}

// CloudScorecardValue is the cloud-side storage of a single scorecard
// element value for a pushed placement.
type CloudScorecardValue struct {
	ID uint

	CloudMatchPlacement   CloudMatchPlacement
	CloudMatchPlacementID uint

	Element string
	Value   int
}

type TeamModule interface {
	ListTeams(context.Context, team.Team) ([]team.Team, error)
}

type Module struct {
	r  chi.Router
	db *db.DB
	ws *web.Server

	scope string

	tm TeamModule

	cloudClient *cloud.Client

	// autoPush serializes and coalesces the best-effort cloud
	// pushes fired by scorecard submits (see scheduleCloudPush).
	autoPush autoPushState

	basePath string
}

func New(scope string, db *db.DB, ws *web.Server, deps modules.ModuleDeps) *Module {
	m := Module{
		r:  chi.NewRouter(),
		db: db,
		ws: ws,

		scope: scope,

		cloudClient: cloud.New(),
	}

	if tm, ok := deps["team"].(TeamModule); ok {
		m.tm = tm
	}

	if m.ws != nil {
		for _, p := range []string{PermissionAdmin, PermissionSchedule, PermissionReferee, PermissionTimekeeper, PermissionSynchronize} {
			if err := m.ws.InstallPermission(context.Background(), ModuleName, p); err != nil {
				return nil
			}
		}
	}

	pongo2.RegisterFilter("key", filterGetValueByKey)

	pAdmin := web.Permission{Module: ModuleName, Grant: PermissionAdmin}
	pSchedule := web.Permission{Module: ModuleName, Grant: PermissionSchedule}
	pReferee := web.Permission{Module: ModuleName, Grant: PermissionReferee}
	pSynchronize := web.Permission{Module: ModuleName, Grant: PermissionSynchronize}
	m.r.Route("/", func(r chi.Router) {
		// The configuration and setup screens are shared by both
		// scopes: the cloud instance is where the game design is
		// authored and maintained before it is pulled to the sites.
		r.Get("/active-configuration", m.ws.GuardRoute(pAdmin, m.uiViewGame))
		r.Get("/setup", m.ws.GuardRoute(pAdmin, m.uiViewSetupForm))
		r.Post("/setup", m.ws.GuardRoute(pAdmin, m.uiViewSetupSubmit))

		// The calculator loads a single scorecard with no
		// submit logic, it just handles the scorecard so that
		// it can be loaded to demonstrate the scores.
		r.Get("/calculator", m.uiViewCalculator)

		// Everything else, including the import UI and the live
		// operations routes (scoreboard, timekeeping, match clock),
		// is onsite-only.
		if scope != "cloud" {
			r.Get("/import-cloud", m.ws.GuardRoute(pAdmin, m.uiViewImportCloud))
			r.Post("/import-cloud", m.ws.GuardRoute(pAdmin, m.uiViewImportCloudSubmit))
			r.Get("/push-cloud", m.ws.GuardRoute(pAdmin, m.uiViewPushCloud))
			r.Post("/push-cloud", m.ws.GuardRoute(pAdmin, m.uiViewPushCloudSubmit))

			r.Route("/fields", func(r chi.Router) {
				r.Use(m.ws.RequirePermission(pSchedule))

				r.Get("/", m.uiViewFieldList)
				r.Get("/add", m.uiViewFieldForm)
				r.Post("/add", m.uiViewFieldSubmit)

				r.Get("/{id}/edit", m.uiViewFieldForm)
				r.Post("/{id}/edit", m.uiViewFieldSubmit)
			})

			r.Route("/schedule", func(r chi.Router) {
				r.Get("/", m.uiViewPhaseList)
				r.Get("/phases/{id}", m.uiViewPhaseSchedule)
				r.Get("/phases/{id}/stats", m.ws.GuardRoute(pAdmin, m.uiViewPhaseScoreStats))
				r.Get("/phases/{id}/rankings", m.ws.GuardLogin(m.uiViewPhaseRankings))
				r.Post("/phases/{id}/make-active", m.ws.GuardRoute(pSchedule, m.uiViewPhaseMakeActive))
				r.Post("/phases/{id}/make-frozen", m.ws.GuardRoute(pSchedule, m.uiViewPhaseMakeFrozen))
				r.Post("/phases/{id}/make-scores-hidden", m.ws.GuardRoute(pSchedule, m.uiViewPhaseToggleScoresVisibility))
				r.Post("/phases/{id}/make-scores-visible", m.ws.GuardRoute(pSchedule, m.uiViewPhaseToggleScoresVisibility))
				r.Get("/phases/{id}/select-teams", m.ws.GuardRoute(pSchedule, m.uiViewPhaseScheduleSelectTeams))
				r.Post("/phases/{id}/select-teams", m.ws.GuardRoute(pSchedule, m.uiViewPhaseSchedulePreview))
				r.Post("/phases/{id}/accept-schedule", m.ws.GuardRoute(pSchedule, m.uiViewPhaseScheduleAccept))
			})

			r.Route("/scorecard", func(r chi.Router) {
				r.Get("/", m.uiViewScorecardList)
				r.Get("/{phase}/{match}/{field}/{position}", m.uiViewScorecard)
				r.Post("/{phase}/{match}/{field}/{position}", m.ws.GuardRoute(pReferee, m.uiViewScorecardSubmit))
			})
			r.Route("/timekeeping", func(r chi.Router) {
				r.Use(m.ws.RequirePermission(web.Permission{Module: ModuleName, Grant: PermissionTimekeeper}))

				r.Get("/", m.uiViewTimekeeping)
				r.Post("/start", m.uiViewTimekeepingStart)
				r.Post("/cancel", m.uiViewTimekeepingCancel)
				r.Post("/transitions", m.uiViewTimekeepingTransitions)
				r.Post("/next-match", m.uiViewTimekeepingSetNextMatch)
			})
			r.Route("/scoreboard", func(r chi.Router) {
				r.Get("/", m.uiViewScoreboard)
				r.Get("/data", m.uiViewScoreboardData)
			})
			r.Get("/clock", m.uiViewClockDisplay)
		} else {
			// The cloud setup endpoint is public on purpose: the game file
			// is game design, not sensitive data, and is pulled by other
			// Gameday systems. The contest scores endpoint is guarded by
			// GAME:SYNCHRONIZE: it is the push target for onsite servers
			// and writes to the cloud's stored data.
			r.Get("/cloud/setup", m.cloudSetupData)
			r.Post("/cloud/contests/{id}/scores", m.ws.GuardRoute(pSynchronize, m.cloudContestScoresData))

			// The scorecards and schedules lookup pages are public,
			// so visitors can follow an event without logging in.
			r.Get("/scorecards", m.uiViewCloudScorecards)
			r.Get("/scorecards/{id}", m.uiViewCloudScorecard)
			r.Get("/schedules", m.uiViewCloudSchedules)
			r.Get("/schedules/{contest}/{phase}", m.uiViewCloudSchedule)
		}
	})

	return &m
}

func (m *Module) Router() chi.Router {
	return m.r
}

func (m *Module) Migrate() error {
	// Collapse any duplicate cloud placement rows before the unique
	// index on the natural key is created.
	if err := m.dedupeCloudPlacements(); err != nil {
		return err
	}

	// Parents must precede children: the Postgres dialector emits
	// foreign keys inline in CREATE TABLE and enforces them at DDL
	// time, so a child table cannot be created before its parent.
	if err := m.db.AutoMigrate(
		FieldPosition{},
		Field{},
		GameElement{},
		GameElementState{},
		GameElementStateValue{},
		GamePhase{},
		GamePhaseAdvancementFilter{},
		MatchPlacement{},
		MatchScore{},
		ScorecardElement{},
		ScorecardValue{},
		CloudMatchPlacement{},
		CloudScorecardValue{},
		ClockSettings{},
		ActiveClock{},
	); err != nil {
		return err
	}

	// Ensure the default field always exists.
	field, err := gorm.G[Field](m.db.DB).Where(&Field{Name: "Field 1"}).First(context.Background())
	if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return err
	}
	if field.ID == 0 {
		if err := m.db.Create(&Field{Name: "Field 1"}).Error; err != nil {
			return err
		}
	}
	return nil
}

// dedupeCloudPlacements collapses duplicate cloud placement rows that
// share the same natural key, keeping the lowest ID in each group and
// re-pointing the scorecard values of the removed rows at the kept
// row. This is a one-time defensive step so the unique index on the
// natural key can be created on a live database.
func (m *Module) dedupeCloudPlacements() error {
	if !m.db.Migrator().HasTable(&CloudMatchPlacement{}) {
		return nil
	}
	type keyRow struct {
		ContestID  uint
		Round      int
		Match      int
		PhaseID    uint
		FieldID    uint
		PositionID uint
		MinID      uint
	}
	var keys []keyRow
	if err := m.db.DB.Raw(`
		SELECT contest_id, round, match, phase_id, field_id, position_id, MIN(id) AS min_id
		FROM cloud_match_placements
		GROUP BY contest_id, round, match, phase_id, field_id, position_id
		HAVING COUNT(*) > 1
	`).Scan(&keys).Error; err != nil {
		return err
	}
	if len(keys) == 0 {
		return nil
	}

	dupIDs := make([]uint, 0, len(keys))
	for _, k := range keys {
		var ids []uint
		if err := m.db.DB.Raw(`
			SELECT id FROM cloud_match_placements
			WHERE contest_id = ? AND round = ? AND match = ? AND phase_id = ?
			  AND field_id = ? AND position_id = ? AND id <> ?
		`, k.ContestID, k.Round, k.Match, k.PhaseID, k.FieldID, k.PositionID, k.MinID).
			Scan(&ids).Error; err != nil {
			return err
		}
		for _, id := range ids {
			dupIDs = append(dupIDs, id)
			if err := m.db.Model(&CloudScorecardValue{}).
				Where("cloud_match_placement_id = ?", id).
				Update("cloud_match_placement_id", k.MinID).Error; err != nil {
				return err
			}
		}
	}

	if len(dupIDs) > 0 {
		if err := m.db.Where("id IN ?", dupIDs).Delete(&CloudMatchPlacement{}).Error; err != nil {
			return err
		}
	}
	return nil
}

// DivisionAware reports whether any phase in the game is division
// aware.  A missing or unreadable phase table is treated as not
// division aware.
func (m *Module) DivisionAware() bool {
	count, err := gorm.G[GamePhase](m.db.DB).Where(&GamePhase{DivisionAware: true}).Count(context.Background(), "*")
	if err != nil {
		return false
	}
	return count > 0
}

func (m *Module) ListFields(ctx context.Context, filter Field) ([]Field, error) {
	out, err := gorm.G[Field](m.db.DB).Preload("Divisions", nil).Where(filter).Find(ctx)
	return out, err
}

// onsiteContestRow is a minimal local view of the contest module's
// OnsiteContest table. The game module reads it directly by table
// name rather than importing the contest module, because the contest
// module already depends on the game module and a reverse import
// would create a dependency cycle.
type onsiteContestRow struct {
	ID uint
}

func (onsiteContestRow) TableName() string { return "onsite_contests" }

// selectedContestID returns the ID of the contest this onsite server
// is handling, or 0 when no contest is selected or the table is
// missing (e.g. a cloud-scope server that never ran the contest
// module's migration). It is best-effort: a read failure must not
// block schedule creation or score entry.
func (m *Module) selectedContestID(ctx context.Context) uint {
	row, err := gorm.G[onsiteContestRow](m.db.DB).First(ctx)
	if err != nil {
		return 0
	}
	return row.ID
}

func (m *Module) TemplateLoader() pongo2.TemplateLoader {
	if m.ws != nil && m.ws.TemplateDebug() {
		return web.DebugTemplateLoader("modules/game/ui/p2", efs)
	}
	sub, _ := fs.Sub(efs, "ui/p2")
	return pongo2.NewFSLoader(sub)
}

func (m *Module) NavList(prefix string) []web.NavElement {
	m.basePath = prefix

	children := []web.NavChild{{
		Text:       "Configuration",
		Target:     path.Join(prefix, "/active-configuration"),
		Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
	}, {
		Text:       "Setup",
		Target:     path.Join(prefix, "/setup"),
		Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
	}, {
		Text: "Calculator",
		Target: path.Join(prefix, "calculator"),
	}}

	if m.scope != "cloud" {
		children = append(children,
			web.NavChild{
				Text:       "Cloud Import",
				Target:     path.Join(prefix, "/import-cloud"),
				Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
			},
			web.NavChild{
				Text:       "Cloud Push",
				Target:     path.Join(prefix, "/push-cloud"),
				Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
			},
			web.NavChild{
				Text:       "Fields",
				Target:     path.Join(prefix, "/fields"),
				Permission: web.Permission{Module: ModuleName, Grant: PermissionAdmin},
			},
			web.NavChild{
				Text:   "Schedule",
				Target: path.Join(prefix, "/schedule"),
			},
			web.NavChild{
				Text:       "Timekeeping",
				Target:     path.Join(prefix, "/timekeeping"),
				Permission: web.Permission{Module: ModuleName, Grant: PermissionTimekeeper},
			},
			web.NavChild{
				Text:   "Scorecards",
				Target: path.Join(prefix, "/scorecard"),
			},
			web.NavChild{
				Text:   "Scoreboard",
				Target: path.Join(prefix, "/scoreboard"),
			},
			web.NavChild{
				Text:   "Clock Display",
				Target: path.Join(prefix, "/clock"),
				Blank:  true,
			},
		)
	}

	if m.scope == "cloud" {
		children = append(children, web.NavChild{
			Text:   "Scorecards",
			Target: path.Join(prefix, "/scorecards"),
		}, web.NavChild{
			Text:   "Schedules",
			Target: path.Join(prefix, "/schedules"),
		})
	}

	return []web.NavElement{{
		Text:     "Game",
		Weight:   79,
		Children: children,
	}}
}
