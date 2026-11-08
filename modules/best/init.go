package best

import (
	"github.com/gizmo-platform/gameday/modules"
	"github.com/gizmo-platform/gameday/modules/game"
	"github.com/gizmo-platform/gameday/pkg/db"
	"github.com/gizmo-platform/gameday/pkg/web"
)

func init() {
	modules.Register(modules.ModuleInfo{
		Name:         "best",
		Scopes:       []string{"onsite"},
		Dependencies: []string{"game"},
		Required:     false,
		Factory: func(scope string, db *db.DB, server *web.Server, deps modules.ModuleDeps) modules.Web {
			return New(scope, db, server, deps)
		},
	})

	// Register the wildcard phase role with the game module's role
	// registry so setup files can tag a phase as the wildcard phase.
	game.RegisterPhaseRole(RoleWildcard)
}
