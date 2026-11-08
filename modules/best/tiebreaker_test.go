package best

import (
	"context"
	"testing"

	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/modules/team"
	"gorm.io/gorm"
)

// TestUnifiedTieBreakerResolvesPhaseTies drives the full ranking path
// (scoreboardRankings → resolveTies → the registered
// BESTUnifiedTieBreaker) over a board with a three-way tie and checks
// the tie breaks strictly, the notebook order decides the tied group,
// and no row behind the group keeps a stale rank ahead of a
// higher-scoring team.
func TestUnifiedTieBreakerResolvesPhaseTies(t *testing.T) {
	m, d := newTestModule(t)
	ctx := context.Background()

	phase := game.GamePhase{
		Name:           "Seeding",
		Role:           game.RoleSeeding,
		ScoreSummation: "Total",
		TieBreaker:     TieBreakerBESTUnified,
		Frozen:         true,
	}
	if err := d.Create(&phase).Error; err != nil {
		t.Fatalf("create phase: %v", err)
	}

	// 102, 103, 104 tie at 90; their notebook scores order them
	// 103 > 104 > 102.  101 and 105 bracket the tie.
	scores := map[int]int{101: 100, 102: 90, 103: 90, 104: 90, 105: 80}
	seedBestScores(t, d, map[int]map[string]float32{
		102: {"notebook": 10},
		103: {"notebook": 30},
		104: {"notebook": 20},
	})
	for _, number := range []int{101, 105} {
		tm := team.Team{Name: "Extra", Number: number}
		if err := d.Create(&tm).Error; err != nil {
			t.Fatalf("create team %d: %v", number, err)
		}
	}

	match := 0
	for number, score := range scores {
		match++
		tm, err := gorm.G[team.Team](d.DB).Where(&team.Team{Number: number}).First(ctx)
		if err != nil {
			t.Fatalf("team %d: %v", number, err)
		}
		mp := game.MatchPlacement{Match: match, PhaseID: phase.ID, TeamID: tm.ID, FieldID: 1, State: game.MatchStateComplete}
		if err := d.Create(&mp).Error; err != nil {
			t.Fatalf("placement: %v", err)
		}
		ms := game.MatchScore{Score: score, GamePhaseID: phase.ID, TeamID: tm.ID, MatchPlacementID: mp.ID}
		if err := d.Create(&ms).Error; err != nil {
			t.Fatalf("score: %v", err)
		}
	}

	tables, err := m.gm.PhaseRankings(ctx, game.RoleSeeding, true)
	if err != nil {
		t.Fatalf("PhaseRankings: %v", err)
	}
	if len(tables) != 1 {
		t.Fatalf("tables = %d, want 1", len(tables))
	}
	rows := tables[0].Rows
	if len(rows) != 5 {
		t.Fatalf("rows = %d, want 5", len(rows))
	}

	wantOrder := []int{101, 103, 104, 102, 105}
	wantRanks := []int{1, 2, 3, 4, 5}
	for i, want := range wantOrder {
		if rows[i].Team.Number != want {
			t.Errorf("position %d: team %d, want %d (order %v)", i, rows[i].Team.Number, want, rowNumbers(rows))
		}
		if rows[i].Rank != wantRanks[i] {
			t.Errorf("position %d: rank %d, want %d (ranks %v)", i, rows[i].Rank, wantRanks[i], rowRanks(rows))
		}
	}
	// The 80-pointer must never carry a rank ahead of the 90s it was
	// pushed behind when the tie expanded.
	for i := 1; i < len(rows); i++ {
		if rows[i].Score > rows[i-1].Score {
			t.Errorf("score inversion at %d: %d above %d (ranks %v, order %v)",
				i, rows[i-1].Score, rows[i].Score, rowRanks(rows), rowNumbers(rows))
		}
	}
}

// TestRegisterTieBreakerIdempotent guards the dispatcher registration
// against duplicate-registration panics: module construction (and the
// lint command) may install it repeatedly within one process.
func TestRegisterTieBreakerIdempotent(t *testing.T) {
	registerTieBreaker()
	registerTieBreaker()
	if _, ok := modules.GetTieBreaker(TieBreakerBESTUnified); !ok {
		t.Fatalf("tie-breaker %q not registered", TieBreakerBESTUnified)
	}
}

func rowNumbers(rows []game.RankingRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Team.Number)
	}
	return out
}

func rowRanks(rows []game.RankingRow) []int {
	out := make([]int, 0, len(rows))
	for _, r := range rows {
		out = append(out, r.Rank)
	}
	return out
}
