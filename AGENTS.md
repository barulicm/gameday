# Gameday — AGENTS.md

AI agent reference for the Gameday codebase. Covers architecture, patterns, data models, and workflows.

## Project Overview

Gameday is a robotics competition management system. It provides an on-site web application for tournament operations (team management, match scheduling, scorekeeping) and an offline CLI for schedule generation.

- **Module**: `github.com/gizmo-platform/gameday`
- **Go version**: 1.25.0
- **Primary language**: Go

## Directory Structure

```
.
├── main.go                 # Entrypoint → cmd.Entrypoint()
├── go.mod
├── Dockerfile              # Multi-stage Alpine build, CGO + static SQLite
├── internal/
│   └── cmd/                # Cobra CLI commands
│       ├── root.go         # Root command, slog setup (LOG_LEVEL)
│       ├── onsite.go       # `gameday onsite` parent command
│       ├── onsite_serve.go # `gameday onsite serve` — boots DB, web server, modules
│       ├── onsite_sfx.go   # `gameday onsite sfx [sound]` — play one sound, or announce the clock
│       ├── onsite_tray.go  # `gameday onsite tray` — system tray applet (no webserver)
│       ├── cloud.go        # `gameday cloud` parent command
│       ├── cloud_serve.go  # `gameday cloud serve` — boots DB, web server, modules (cloud scope)
│       ├── bake.go         # `gameday bake` parent command
│       ├── bake_schedule.go# `gameday bake schedule` — offline schedule generation
│       ├── bake_schedules.go # `gameday bake schedules` — batch-anneal embeddable artifacts
│       ├── debug.go        # `gameday debug` parent command
│       ├── debug_game.go   # `gameday debug game`
│       ├── debug_game_genscores.go # Clear + regenerate random scores for a phase
│       ├── debug_game_lint.go # Lint a game setup file (parse, expr, references)
│       ├── debug_best.go   # `gameday debug best` parent command
│       ├── debug_best_genscores.go # Wipe + randomize BEST external scores
│       ├── version.go      # `gameday version`
│       └── util.go         # CLI utilities
├── modules/                # Pluggable web modules
│   ├── type.go             # modules.Web interface definition
│   ├── registry.go         # Module registry, ResolveModules, tie-breaker registry
│   ├── contestdata.go      # ContestDataRow + ContestDataClearer optional capability interface
│   ├── team/
│   │   ├── init.go         # Module registration ("team", required)
│   │   ├── team.go         # Team + Division models, Team CRUD, CSV import, cloud routes, embed ui/*
│   │   ├── cloud.go        # Cloud import handlers, cloud JSON endpoints, upsertTeamRows
│   │   └── ui/p2/views/team/         # list, form_single, form_bulk, import_cloud, divisions/*
│   ├── game/
│   │   ├── init.go         # Module registration ("game", depends on team)
│   │   ├── module.go       # Game state, MatchState enum, MatchPlacement, TeamModule
│   │   ├── schedule.go     # Schedule UI + division-aware generation
│   │   ├── clock.go        # Match clock: models, handlers, timers
│   │   ├── clock_match.go  # Clock-driven match state transitions
│   │   ├── scoreboard.go   # Scoreboard views
│   │   ├── scorecard.go    # Scorecard views
│   │   ├── setup.go        # Game setup (applyGameConfig shared with cloud import)
│   │   ├── cloud.go        # Cloud import handlers + public /cloud/setup YAML export
│   │   ├── export.go       # ExportContestData: portable contest schedule/scorecard export
│   │   ├── cleardata.go    # modules.ContestDataClearer: counts + delete of this module's contest-keyed rows
│   │   ├── push.go         # Cloud push: store endpoint, PushContestData, push UI
│   │   ├── cloud_scorecards.go # Cloud scorecards-by-team lookup page (cloud scope)
│   │   ├── advancement.go  # Team advancement logic
│   │   ├── lint.go         # LintConfig: setup-file validation (expr, references)
│   │   ├── filters.go      # Scoreboard filtering
│   │   ├── filter_scoreboard.go
│   │   ├── debug.go        # Module-level debug endpoints
│   │   ├── util.go
│   │   └── ui/p2/          # Embedded templates (views/game/* incl. import_cloud, partials/scorecard*)
│   ├── best/
│   │   ├── init.go         # Module registration ("best", optional, depends on game)
│   │   ├── module.go       # BEST:ADMIN permission, scores + awards routes, GameModule interface
│   │   ├── scores.go       # Scores view
│   │   ├── awards.go       # Award registry, Scorer, dense ranking engine, awards page
│   │   ├── performance.go  # Robot Performance points derived from the Seeding phase
│   │   ├── best_award.go   # BEST Award composite scorer + eligibility diagnosis
│   │   ├── type.go         # ScoreType + TeamScoreValue models
│   │   ├── debug.go        # DebugGenerateScores
│   │   └── ui/p2/views/best/   # scores.p2, awards.p2
│   └── contest/
│       ├── init.go         # Module registration ("contest", cloud+onsite, required)
│       ├── module.go       # Contest (+ frozen flag) and ContestRoster models, contest CRUD, freeze/unfreeze, Clear Data, roster routes, cloud JSON endpoints
│       ├── cloud.go        # Public cloud JSON endpoints (contest list, per-contest roster)
│       ├── onsite.go       # OnsiteModule: "My Contest" screen, selection, auto-import of roster + game
│       └── ui/p2/views/contest/    # list, form, roster, roster_add, my_contest
├── pkg/
│   ├── cloud/              # Client for the cloud instance (base URL, auth seam, per-feature files)
│   │   ├── client.go       # Client, New, Do/DoBytes/DoJSON, options (WithBaseURL/WithTimeout/WithAuthenticator/WithBasicAuth)
│   │   ├── auth.go         # Authenticator interface + ready BasicAuth
│   │   ├── teams.go        # RegionInfo/TeamInfo wire shapes, GetRegions/GetRegionTeams
│   │   ├── games.go        # GetGameFile (raw YAML bytes from the public game setup endpoint)
│   │   ├── contests.go     # ContestInfo wire shape, GetContests/GetContestRoster
│   │   ├── scores.go       # ContestPlacement wire shape, PushContestData
│   │   └── client_test.go
│   ├── db/
│   │   └── db.go           # GORM wrapper (options, SQLite default, Postgres via DSN), Raw(), InsertOrUpdate[T]
│   ├── web/
│   │   ├── server.go       # Server struct, Chi router, Pongo2, Authware, admin routes
│   │   ├── authz.go        # Permission struct, InstallPermission, RequirePermission, GuardRoute
│   │   ├── middleware.go   # ProfileKey context injection, Profile.HasPermission logic
│   │   ├── tmpl.go         # DoTemplate helper, AddTemplateLoader
│   │   ├── tmpl_debug.go   # DebugTemplateLoader (on-disk templates, embedded fallback)
│   │   ├── nav.go          # NavElement (Weight-sorted), NavChild, AddNavElement
│   │   ├── users.go        # User CRUD, authwareBackend (bcrypt gameday backend)
│   │   ├── tokens.go       # APIToken model, token generation, AuthToken (Bearer), token admin handlers
│   │   ├── admin.go        # Admin UI handlers (profile list, permission assignment)
│   │   ├── login.go        # Login/landing views
│   │   ├── option.go       # WithDB, WithTemplateDebug, EventBus options
│   │   ├── util.go         # StrToUint, PageParams, Paginate scope
│   │   ├── error_templates_test.go
│   │   ├── tmpl_debug_test.go
│   │   ├── eventbus_test.go
│   │   └── ui/             # Embedded core web assets
│   │       ├── p2/         # Core templates: base.p2, fullscreen.p2, login.p2,
│   │       │               # fragments/nav.p2, view/admin/*, errors/*
│   │       └── static/     # css/theme.css (print + theme toggle switch), css/scorecard.css, js/mustache.js, js/eventbus.js, js/theme.js, js/navbar.js
│   ├── event/              # Publish/subscribe event bus + websocket export
│   │   ├── event.go        # Event, Envelope, system type constants
│   │   ├── bus.go          # Bus, New, Options, Publish/Subscribe/Close, replay ring
│   │   ├── handler.go      # ServeWS + Handler (coder/websocket, JSON wire format)
│   │   ├── bus_test.go
│   │   └── handler_test.go
│   ├── announcer/          # Match clock events → sound playback
│   │   ├── announcer.go    # Announcer, New, Options (WithPlayer/WithBus/WithServer), Run, SoundFor, EventTypes
│   │   └── announcer_test.go
│   ├── sfx/                # One-shot sound effect playback (gopxl/beep)
│   │   ├── sfx.go          # Player, New, Options (WithFS/WithDir), Play/PlayAndWait/Duration/Stop/Close/Sounds
│   │   ├── speaker.go      # Process-wide audio sink (speaker.Init), open timeout
│   │   ├── embed.go        # Embedded sound library (all:sounds, fs.Sub to base names)
│   │   ├── sounds/         # Checked-in .wav files (embedded via embed.FS)
│   │   └── sfx_test.go
│   ├── schedgen/           # Offline schedule generation
│   │   ├── anneal.go       # Annealed generator: fixed-shape late-acceptance search
│   │   ├── baked.go        # BakedSchedule artifacts: embed.FS archive, lookup, materialize
│   │   ├── baked/          # Checked-in YAML artifacts (embedded via embed.FS)
│   │   ├── common.go       # Config, DivisionConfig, Schedule, Round, Match, Location
│   │   │                  # Generator interface, GeneratorFactory, GenerateSchedule
│   │   ├── division.go     # GenerateDivisionSchedule (per-division + interleave)
│   │   ├── bid.go          # Balanced Incomplete Block Design generator
│   │   ├── random.go       # Random seeding generator
│   │   ├── oneshot.go      # One-shot generator
│   │   ├── score.go        # Schedule scoring (ClosestReplay, diversity metrics)
│   │   ├── bid_test.go     # BIBD smoke + larger team tests
│   │   └── division_test.go
│   └── buildinfo/
│       └── buildinfo.go    # Version, Commit, BuildDate (ldflags injection)
└── test_data/              # Schedule fixtures (YAML) + team import CSVs
```

## Architecture

### High-Level Flow

```
CLI (cobra) → Modules (chi routers) → Web Server (pkg/web) → Database (gorm + sqlite)
                ↕
        Schedule Generation (pkg/schedgen, offline "bake" command)
```

### CLI Layer (`internal/cmd/`)

- Uses **Cobra** for command structure.
- `main.go` calls `cmd.Entrypoint()`.
- `root.go` configures `slog` based on `LOG_LEVEL` env var, then executes the root command.
- Five top-level commands:
  - **`onsite`** — Web server operations (`serve` subcommand boots the full app in the `onsite` scope; supports `--debug` template debug mode; `sfx` subcommand plays one named sound effect and exits, or with no argument announces a running server's match clock until interrupted; `tray` subcommand runs a standalone system tray applet — build info, clock-sound announcement, one test entry per sound, and Exit, no webserver, DB, or modules)
  - **`cloud`** — Web server operations for the cloud entrypoint (`serve` subcommand is almost identical to `onsite serve` but resolves modules in the `cloud` scope, e.g. excludes the onsite-only `best` module; supports `--debug` template debug mode)
  - **`bake`** — Offline schedule generation (`bake schedule <gen> <rounds> <fields> <positions> <teams>`; `bake schedules --teams 8-64 ...` batch-bakes embeddable annealed artifacts)
  - **`debug`** — Database manipulation tools (`debug game genscores <phase>`, `debug game lint <file>`, `debug best genscores`)
  - **`version`** — Print build info

### Web Server (`pkg/web/`)

- **Router**: Chi v5 (`github.com/go-chi/chi/v5`)
- **Templates**: Pongo2 v6 (`github.com/flosch/pongo2/v6`) loaded from embedded `pkg/web/ui/p2` directory; module template loaders are added at runtime via `AddTemplateLoader`
- **Static files**: Embedded `pkg/web/ui/static` served at `/static/*` via `http.FileServer`
- **Auth**: `github.com/the-maldridge/authware` with custom `gameday` bcrypt backend (htpasswd backend is also blank-imported in `onsite_serve.go`; `AUTHWARE_BASIC_MECHS` defaults to `"gameday"` if unset). The same `gameday` backend also implements `authware.TokenAuthenticator` for Bearer API tokens (`tokens.go`): `AUTHWARE_TOKEN_MECHS` defaults to `"gameday"` if unset, so `Authorization: Bearer <token>` requests are validated against the `api_tokens` table and resolve to the owning user's profile and permissions. Tokens are managed per-user under `/admin/user/{id}/tokens`.
- **Database**: `*gorm.DB` (via `*db.DB`) passed via `WithDB()` option

**Server bootstrap** (`server.go`):
1. Create Chi router
2. Set up Pongo2 with embedded templates (or `DebugTemplateLoader` when `--debug` is set)
3. Register template filters (`hasPermission`, `hasPermissionExact`)
4. Auto-migrate core tables (`Permission`, `Profile`, `User`, `APIToken`)
5. Install `CORE:ADMIN` permission (idempotent)
6. Create default admin user if none exist (uses `GAMEDAY_ADMIN_INIT_PW` or `"GameOn!"`)
7. Add Admin nav element (weight 99)
8. Initialize Authware with `gameday` backend, wire session + profile middleware
9. Mount static files and core routes (`/admin/*`, `/login`, `/logout`, `/ui`)

### Module System (`modules/`)

**Interface** (`modules/type.go`):
```go
type Web interface {
    Router() chi.Router
    Migrate() error
    NavList(string) []web.NavElement
    TemplateLoader() pongo2.TemplateLoader
}
```

**Registration** (`modules/registry.go`): Each module package registers itself from `init()`:
```go
modules.Register(modules.ModuleInfo{
    Name:         "game",
    Scopes:       []string{"onsite", "cloud"},
    Dependencies: []string{"team"},
    Required:     true,
    Factory: func(scope string, db *db.DB, server *web.Server, deps modules.ModuleDeps) modules.Web {
        return New(scope, db, server, deps)
    },
})
```

- `ModuleInfo` declares `Name`, `Scopes` (entrypoints the module supports), `Dependencies` (module names that must be created first), `Required` (always loaded when scope matches, regardless of env), and `Factory`.
- `Register` panics on duplicate names or if a required module depends on a non-required one.
- `ResolveModules(scope, db, server)` filters by scope, applies the `GAMEDAY_MODULES` env var (comma-separated whitelist of optional modules), validates the dependency graph, topologically sorts (deterministic via alphabetical queue tie-breaks), and instantiates modules in order (passing the scope to each factory), returning a name-keyed `ModuleDeps` map.
- **Scope-conditional routes**: the scope is threaded factory → `New(scope, ...)` and each module registers routes conditionally on it when building its single router. A process runs exactly one scope, so the route sets are mutually exclusive per instance: the cloud-scoped `team` module registers only the cloud JSON endpoints (not the import handlers) and the cloud-scoped `game` module registers the shared configuration/setup screens plus `GET /cloud/setup` (not the scoreboard, timekeeping, match clock, or import routes). Nav lists follow suit (e.g. the cloud-scoped game module emits only the "Configuration" and "Setup" children; the team module omits the "Cloud Import" child on cloud).

**Current module registry**:

| Name | Scopes | Required | Dependencies | Permissions |
|------|--------|----------|--------------|-------------|
| `team` | onsite, cloud | yes | — | `TEAM:ADMIN` |
| `game` | onsite, cloud | yes | team | `GAME:ADMIN`, `GAME:SCHEDULE`, `GAME:REFEREE`, `GAME:TIMEKEEPER`, `GAME:SYNCHRONIZE` |
| `best` | onsite | no | game | `BEST:ADMIN` |
| `contest` | onsite, cloud | yes | team, game | `CONTEST:ADMIN` |

**Mounting** (`onsite_serve.go`): For each resolved module:
1. `w.Mount("/ui/mod/<name>", handle.Router())`
2. `w.AddNavElement(handle.NavList(prefix)...)`
3. `w.AddTemplateLoader(handle.TemplateLoader())`
4. `handle.Migrate()`

**Startup seeding** (`Migrate`): beyond `AutoMigrate`, each module guarantees its built-in rows on every boot (idempotent get-or-create): the `team` module seeds its static divisions plus the `Open` division, and the `game` module seeds a `Field` named `Field 1`.

**Dependency injection**: Modules access other modules through the `deps modules.ModuleDeps` map in their constructor, e.g. `deps["team"].(game.TeamModule)` in `game/module.go:332`. There is no functional-options layer for modules; constructors take positional `(scope, db, server, deps)` arguments.

**Optional capabilities**: Beyond the required `Web` interface, a module may implement an optional capability interface, and consumers discover it by type-asserting each entry of `deps`. A module that does not implement one is simply skipped, never an error. `modules.ContestDataClearer` (`modules/contestdata.go`) is the example: it lets the contest screen erase everything tied to one contest without reaching into other modules' tables (see Clearing Contest Data below). Consumer-side interfaces normally live in the consumer package (cf. `contest.TeamModule`, `best.GameModule`), but this one lives in `modules` because its signature returns a type (`ContestDataRow`) the game module must reference, and `game` cannot import `contest` (dependency cycle): the same seam as `game`'s `TableName()` shadow structs, inverted.

**Tie-breakers**: `registry.go` also holds a global tie-breaker registry (`RegisterTieBreaker`/`GetTieBreaker`) for ranking tied teams. A module's tie-breaker is registered when its `New()` runs rather than from `init()`, so the name only resolves while the module is loaded (an unloaded module makes `LintConfig` report an unknown `TieBreaker` instead of silently resolving to a no-op); `best.registerTieBreaker()` is idempotent for processes that construct the module more than once.

**Phase roles**: `roles.go` holds a global phase-role registry (`RegisterPhaseRole`/`ValidPhaseRole`/`PhaseRoles`). A `PhaseRole` is a string kind naming the competitive purpose of a phase (`GamePhase.Role`, YAML `Role`, column `phase_role`); it is the stable cross-module handle for querying phases without knowing author-assigned IDs. The game module statically registers `Exhibition`, `Seeding`, `Semifinal`, `Final` from its package init; other modules register their own roles from `init()` (BEST registers `Wildcard`). `RegisterPhaseRole` panics on an empty or duplicate role. The empty role means "no role assigned": it is always valid in lint but never registerable. `LintConfig` rejects an unregistered `Role`.

### Navigation

`NavElement` carries a `Weight`; `AddNavElement` keeps the list sorted by descending weight. Current weights: Admin = 99, Team = 80, Game = 79. When adding a new module nav element, pick a weight that preserves the desired left-to-right order.

## Frontend (Bulma CSS)

The entire UI was ported to **Bulma CSS 1.0.4** (commit `616d610`):

- `pkg/web/ui/p2/base.p2` loads Bulma 1.0.4 from `/static/css/bulma.min.css` (not checked in; the Dockerfile downloads it from jsDelivr into `pkg/web/ui/static/css/` before building), followed by `/static/css/theme.css`, then `/static/js/theme.js` (dark-mode toggle, applied before first paint) and `/static/js/navbar.js` (`defer`, mobile burger toggle), then includes `fragments/nav.p2`.
- `theme.css` holds print rules plus the theme-toggle switch styles (Bulma 1.0.4 ships no `.switch` component, so the switch is hand-rolled in `theme.css` using Bulma CSS variables). Custom layout is done with Bulma utility classes in templates.
- **Dark mode**: the nav bar carries a toggle switch next to the login/logout button (`fragments/nav.p2`, `#theme-toggle`). `js/theme.js` flips Bulma's dark mode by setting `data-theme="light"` or `data-theme="dark"` on `<html>` (per the Bulma dark-mode docs; the attribute is always set, never removed, so the choice overrides the system's `prefers-color-scheme` in both directions) and persists the choice in `localStorage` (`gameday-theme`). The script is portable (no framework, no build step) and is loaded in `<head>` of `base.p2` and `login.p2` so the persisted theme applies before first paint. The switch shows an inline sun SVG (light) or moon SVG (dark).
- **Schedule printing** (`@media print` in `theme.css`; schedule templates `views/game/schedule.p2`, used onsite and by the cloud Schedules pages, and `schedule_preview.p2`):
  - **Light mode:** schedules always print light. The print block re-applies Bulma's light-theme value for each of the 35 variables the dark theme changes, plus `color-scheme: light`. It does this under `:root:has(.gameday-schedule)`, which outranks both `[data-theme=dark]` and the `prefers-color-scheme: dark` `:root` rule, and sits on `<html>` so Bulma's derived colours recompute. The values come from `bulma.min.css` 1.0.4's `[data-theme=light]` block; re-derive them if Bulma is upgraded.
  - **Black text:** position-coloured cells set `color: black;` so text stays readable on the pastel backgrounds in dark mode on screen.
  - **Packed layout to save paper:** the `.section` padding, the box's padding, shadow and radius, and the flex centering are removed. The grid (`table.schedule-table`) is full-width with `table-layout: fixed`. R/M columns are pinned to `2.4em` by the templates' `<colgroup><col class="schedule-rm">`; fixed layout otherwise takes widths from the colspan header row. Cells are compact (9pt, `0.15em 0.3em` padding, `overflow-wrap: anywhere` so a long name wraps rather than overflowing). `thead` repeats on every page, rows never split across pages, and `@page` margins are `0.4in`. The preview's Accept button is `noprint`.
  - **Landscape:** schedules with 2+ fields get `schedule-wide` on their outer box (`{% if fields|length > 1 %}`), which maps to the landscape named page `@page wide` (Chrome/Edge 85+, Firefox 110+, Safari 18+; older browsers print portrait). Single-field schedules stay portrait, which fits more match rows per page.
  - **Measured on letter paper:** 21 matches 2 → 1 page; 40 matches 3 → 2; a 10-match 2-field schedule 2 portrait → 1 landscape.
  - The old `#content` print rule was dead (the layout element is `#container`, which nothing styles) and was removed.
- `scorecard.css` remains for scorecard-specific styling (large score total, fieldset layout).
- The nav fragment (`fragments/nav.p2`) uses Bulma's collapsible `navbar` structure.
  - **Structure:** a `nav.navbar.noprint` holds the `navbar-brand` (logomark, then a `<button class="navbar-burger">` as its last child; a real button so it is keyboard-reachable, and Bulma resets its native styling). Next comes a `div.navbar-menu#gameday-navbar-menu` wrapping `navbar-start` and `navbar-end`.
  - **Mobile:** below 1024px Bulma hides the menu until it has `is-active`. `static/js/navbar.js` (loaded with `defer` in `base.p2`) toggles `is-active` on the burger, which turns into a cross, and on the menu named by its `data-target`. It mirrors the state into `aria-expanded`. In the open menu each module's dropdown items are listed under their heading (Bulma's touch behaviour).
- Page layout uses `section` > `#container` > `{% block content %}`.
- **Footer** (`base.p2`): every page built on `base.p2` ends with a Bulma `footer.footer.gameday-footer.noprint` reading "Gameday by Gizmo Mechatronics · <version>" (`buildVersion`, injected by `DoTemplate` from `buildinfo.Version`).
  - **Docked:** `<body class="has-docked-footer">` is a flex column with `min-height: 100vh`, and its `.section` has `flex: 1 0 auto`. The footer therefore sits at the bottom of the viewport on short pages and below the content on tall ones (scrolled into view); it never overlays content.
  - **Low contrast on purpose:** `--bulma-footer-padding` is shrunk to `0.75rem 1.5rem` (Bulma's default is `3rem 1.5rem 6rem`), with `0.75rem` text in `--bulma-text-weak` at `opacity: 0.7`.
  - **Not printed** (`noprint`).
  - `fullscreen.p2` and `login.p2` have their own layouts and no footer.
- `fullscreen.p2` is an alternate layout used by the scoreboard (no nav).
- **Landing page** (`/ui`, `uiViewLanding` in `login.go`, `landing.p2`): varies by the server's scope, set with `web.WithScope` by `onsite serve` / `cloud serve`.
  - **Onsite:** a welcome box with a login notice. Logged out, it says login is required and shows a Login button; logged in, it shows "Logged in as <user>". It also lists the URLs other computers on the event network can use (`advertisedURLs`, `lan.go`).
    - If `GAMEDAY_ADVERTISE_ADDR` is set, only that address is shown, with the listen port appended when it has none. This is the only correct answer behind Docker/NAT or a proxy.
    - Otherwise every IPv4 address of interfaces that are up is listed, computed per request. Loopback, link-local (`169.254/16`) and IPv6 addresses are skipped, as are interfaces named `docker*`, `br-*`, `veth*` and `virbr*`. Enumeration goes through the `listInterfaceAddrs` variable so tests can stub it.
    - The port comes from the address passed to `Serve`, falling back to `GAMEDAY_PORT`/`8080`.
    - With no usable address the box tells the operator to set `GAMEDAY_ADVERTISE_ADDR`.
  - **Cloud:** a box showing `GAMEDAY_CLOUD_WELCOME_MSG`, read per request. It is plain text, rendered as `escape | linebreaksbr | safe`: the operator's text is escaped exactly once and newlines become `<br />`. Logged-out users also get a hint linking to Game → Scorecards. Without a message the box shows a "Welcome to Gameday" heading.
  - **No scope set** (e.g. tests): empty content.
  - pongo2 `{# #}` comments are single-line only; use `{% comment %}` for multi-line comments.
- Standard error templates live at `pkg/web/ui/p2/errors/` (`internal.p2`, `unauthorized.p2`) and are rendered directly by handlers on failures.

**Template debug mode**: `gameday onsite serve --debug` enables `web.WithTemplateDebug`. In debug mode:
- Core templates load from the on-disk source tree (`pkg/web/ui/p2`) with a fallback to embedded templates, via `web.DebugTemplateLoader(dir, embedded)` (`pkg/web/tmpl_debug.go`), and the `TemplateSet` is in `Debug` mode so templates are re-ingested on every render.
- Each module's `TemplateLoader()` checks `ws.TemplateDebug()` and returns a `DebugTemplateLoader` rooted at its own `ui/p2` directory.

## Authentication & Authorization

### Library
- `github.com/the-maldridge/authware` — auth framework
- `golang.org/x/crypto/bcrypt` — password hashing

### Data Models

**`User`** (`pkg/web/users.go`):
- `ID uint`
- `Login string` (unique)
- `Secret []byte` (bcrypt hash)
- `ProfileID uint` → links to `Profile`

**`Profile`** (`pkg/web/middleware.go`):
- `ID uint`
- `Username string`
- `Permissions []Permission` (many-to-many via `profile_permissions` join table)

**`Permission`** (`pkg/web/authz.go`):
- `ID uint`
- `Module string` (e.g., `CORE`, `GAME`, `TEAM`, `BEST`)
- `Grant string` (e.g., `ADMIN`, `SCHEDULE`, `REFEREE`, `TIMEKEEPER`)

**`APIToken`** (`pkg/web/tokens.go`):
- `ID uint`
- `Label string` (operator-chosen display name)
- `Prefix string` (first 8 chars of the plaintext, for display)
- `Hash string` (unique; SHA-256 hex digest of the plaintext — the plaintext is never stored)
- `CreatedAt time.Time`
- `UserID uint` → links to `User`

A token authenticates as its owner: `AuthToken` resolves the digest to the owning `User`, and the existing profile middleware then applies that user's permissions to the request.

### Permission Hierarchy (`middleware.go:HasPermission`)

Three-tier check:
1. **Exact match** — `Module:Grant` exactly matches test
2. **Module admin** — `Module:ADMIN` covers any grant in that module
3. **Core admin** — `CORE:ADMIN` is universal access

### Template Filters

- `hasPermission(module, grant)` — hierarchical check (respects admin escalation)
- `hasPermissionExact(module, grant)` — exact match only

### Middleware

- `ProfileKey` context key: injects `*Profile` into request context
- `RequirePermission(permission)` — HTTP middleware for route protection
- `GuardRoute(permission, handler)` — wraps a single handler with permission checking (most common pattern in module routes)
- `GuardLogin(handler)` — wraps a single handler so only a logged-in user (an `authware.User` in the context) reaches it, regardless of permissions; anonymous requests get the same `401` HTML/JSON denial as the permission guards (the HTML page omits the "required permission" sentence when no permission is involved)
- **Denial status codes**: both guards deny with an explicit status — `401` when the request has no `authware.User` (not logged in) and `403` when a user is present but lacks the permission. This matters for API consumers (e.g. the `pkg/cloud` client treats any 2xx as success, so a 200 denial would silently "succeed").
- **Denial body content negotiation**: when the request's `Accept` header includes `application/json` (the `pkg/cloud` client always sends it), the denial body is a JSON object `{"error": "<message>"}` with `Content-Type: application/json` (message: "Please log in to access this resource." for 401, "You do not have permission to access this resource." for 403). Otherwise the guards render the HTML `errors/unauthorized.p2` page. The cloud client's `NewErrorFromBody` parses a JSON `{"error": ...}` body into `Error.Message` (falling back to the trimmed raw body), so the push page's danger notification shows the clean message rather than a block of HTML.

### Admin UI

- User management: `/admin/user/` (list, create, password reset, delete)
- API tokens: `/admin/user/{id}/tokens` (list, create, delete). A token is a random `gd_`-prefixed value; only its SHA-256 hex digest is stored, the plaintext is shown exactly once on the create response (rendered directly, not a redirect), and a short prefix is kept for display. Deleting a user cascades to its tokens (deleted before the user for FK ordering on Postgres).
- Profile permissions: `/admin/profile/` → `/admin/profile/{id}/permissions`
- The profile permissions page renders one `<fieldset>` per module, its legend being the module name through pongo2's `title` filter (`GAME` → `Game`). Each fieldset holds a checkbox per grant, also `title`-cased (`TIMEKEEPER` → `Timekeeper`), checked when the profile holds that exact grant. `groupPermissions` (`admin.go`) groups and sorts modules and grants alphabetically in Go rather than SQL, because `grant` is a reserved word in Postgres.
- Permission assignment uses GORM `Association("Permissions").Replace()` with the checked boxes (unchecking all clears the profile's permissions)

## Key Patterns

### Module Constructors

Module constructors are positional, not option-based; the first argument is the entrypoint scope (`"onsite"` or `"cloud"`), which the module uses to register scope-conditional routes:
```go
func New(scope string, db *db.DB, ws *web.Server, deps modules.ModuleDeps) *Module
```

`web.Server` still uses functional options: `WithDB(d *db.DB)`, `WithTemplateDebug(bool)`, `WithScope(string)` (the entrypoint scope, exposed as `Server.Scope()`, used by the landing page).

### Generic DB Helpers

`db.New(opts ...Option)` opens the connection. Options: `db.WithDriver(name)` selects the database type (`db.DriverSQLite` is the default; `db.DriverPostgres` is also supported), `db.WithPath(path)` and `db.WithEnvPath(name)` override the SQLite file path, and `db.WithDSN(dsn)` / `db.WithEnvDSN(name)` override the PostgreSQL DSN. Without options the default is SQLite at `GAMEDAY_DB` (default `gameday.db`). The driver is selected centrally by `GAMEDAY_DB_DRIVER` (default `sqlite`); when set to `postgres` the DSN is read from `GAMEDAY_DB_DSN`, so switching backends is a pure env-var change with no entrypoint or code changes (path-based options are ignored under Postgres). Entrypoints pick their own default path: `onsite serve` uses `db.WithEnvPath("GAMEDAY_ONSITE_DB")` (falls back to `GAMEDAY_DB`), while `cloud serve` defaults to `gameday_cloud.db`, overridable via `GAMEDAY_CLOUD_DB`.

`gorm.G[T](db)` used throughout for type-safe queries:
```go
teams, err := gorm.G[Team](db).Find(ctx)
```

`db.InsertOrUpdate[T](ctx, db, data)` performs upsert via `clause.OnConflict{UpdateAll: true}`.

`*db.DB` embeds `*gorm.DB`; use `.Raw()` to get the bare `*gorm.DB`.

### Embedded Assets

Each module embeds its own UI assets:
```go
//go:embed ui/*
var efs embed.FS
```

Templates live at `ui/p2/views/<module>/*.p2` (and `ui/p2/partials/` for shared fragments); the core server embeds `pkg/web/ui/*`.

### Template Injection (`tmpl.go:DoTemplate`)

Every template call via `DoTemplate` automatically injects:
- `user` — current `authware.User`
- `profile` — current `Profile`
- `navElements` — permission-filtered navigation
- `pageBase` — current request path
- `buildVersion` — `buildinfo.Version` (shown in the footer)

**Errors in templates**: pass errors to templates as `error` values (`pongo2.Context{"error": err}`), not `err.Error()` strings. pongo2 renders a value through `fmt.Stringer` and otherwise prints a `<T Value>` placeholder, which is what a plain `error` produced. `DoTemplate` runs `adaptErrors`, which replaces every top-level context value that is an `error` with its message as a **plain string**. It must be a string: pongo2 only autoescapes string-kinded values, and error messages often carry user input (form values, cloud response bodies), so a `Stringer` wrapper struct would render unescaped (XSS). An empty message becomes `"unknown error"` so `{% if error %}` stays true. A typed-nil error becomes `nil`. Values nested inside structs or slices are not adapted. JSON response bodies still use `err.Error()`.

**Map lookups in templates**: pongo2 cannot index a `map[uint]T`. A literal index (`{{ m.1.Name }}`) fails with "can't access an index on type map", and a variable key panics inside `reflect.Value.MapIndex` (string is not assignable to uint). Attach per-row state to the row struct via embedding instead — promoted fields keep resolving by bare name, so `{{ row.ID }}` and `{{ row.Name }}` still work while `{{ row.Clear.Total }}` reaches the new data (cf. `contestListItem`, used so the contest list never passes a `map[uint]X` to a template). Slice indexing (`{{ lst.0.Name }}`) does work; a two-variable `{% for k, v in list %}` over a slice is unreliable and should be avoided.

### Pagination (`util.go:Paginate`)

GORM scope: `db.Scopes(Paginate(r))` reads `?page=N&page_size=M` from query params (default: page=1, size=10, max=100). `PageParams(r)` returns the same clamped `(page, pageSize)` pair for use outside a GORM scope.

**gorm v1.31 generic gotcha**: `gorm.G[T](...)` re-sessions with `NewDB: true` internally, so WHERE/OFFSET/LIMIT applied to a raw `*gorm.DB` **before** `G[T]` are silently dropped. With the generic API, apply filters and paging through the chain itself: `gorm.G[T](db).Where(...).Offset((page-1)*size).Limit(size).Find(ctx)`.

**Admin team list** (`uiViewListTeams`, `modules/team`): server-side search + facet filters + pagination in one GET form. Query params: `?q=` (name/number `LIKE`), `division_id=`, `region_id=`, `page=`, `page_size=`. The `Count` (total) and paged `Find` share the same chain so they always agree; rows are ordered by `number ASC`. `filtersQS` is the full query minus `page` and is re-emitted on pager links (note: pongo2 HTML-escapes `&` in it, so the href shows `&amp;`). `q` is intentionally a simple `LIKE` (Option A) so it can be swapped for FTS5/typeahead later without a contract change; the roster query `ListTeams` does **not** use these filters.

### Schedule Generator Registry (`schedgen/common.go`)

Generators register themselves at init time:
```go
var generators map[string]GeneratorFactory
func RegisterGenerator(name string, factory GeneratorFactory)
func GenerateSchedule(gen string, cfg Config) (*Schedule, error)
```

`GeneratorConfig` providers (via `RegisterGeneratorConfig`/`GetConfig`) expose `MaxRounds`/`DefaultRounds`/`RoundsDynamic` for UI templating.

## Data Models

### Core (`pkg/web/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `User` | ID, Login (unique), Secret ([]byte), ProfileID | Authentication |
| `Profile` | ID, Username, Permissions ([]Permission) | Display name + permissions |
| `Permission` | ID, Module (string), Grant (string) | Module:Grant access control |
| `APIToken` | ID, Label, Prefix, Hash (unique, SHA-256 hex), CreatedAt, UserID (→ User) | Bearer token for API calls; plaintext never stored |

### Teams (`modules/team/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `Team` | ID, Name, Number (unique), DivisionID (→ Division), RegionID (→ Region), Disabled | Team record |
| `Division` | ID, Name (unique), Static, NoCompact | Team grouping; `Static` divisions are built-in, others start at ID 100 (`DynamicDivisionStartID`) |
| `Region` | ID, Name (unique) | Geographic grouping of teams |

### Game (`modules/game/`)

| Model | Fields | Purpose |
|-------|--------|---------|
| `MatchPlacement` | Round, Match, Phase, Team, Field, Position, State, ContestID | Team assigned to a match slot |
| `MatchScore` | Score, MatchPlacement, GamePhase, Team, ContestID | Score record |
| `Config` | Field (ConfigField), Game (Game) | Tournament configuration |
| `ClockSettings` | ID (fixed 1), Duration, Hurry (time.Duration), Transitions (bool), NextMatch (int), NextMatchPhase (uint) | Match clock config singleton (file-sourced `Duration`/`Hurry`/`Transitions` + runtime next-match pointer) |
| `ActiveClock` | ID (fixed 1), StartTime, EndTime, HurryTime (time.Time) | Running clock state singleton; `HurryTime = EndTime − Hurry` |

**MatchState enum**: `Scheduled`, `Queued`, `Running`, `Scorable`, `Complete`, `NoShow`, `Disqualified`

#### Match Clock (`clock.go`, `clock_match.go`)

The match clock's timing is **self-contained**: it owns exactly two singleton rows (`ClockSettings`, `ActiveClock`, both fixed ID=1), and its start/cancel/auto-stop logic does not depend on `MatchPlacement` or schedules. Optionally, when `ClockSettings.Transitions` is enabled, the clock drives the `MatchState` of the match in the active phase (see **Match transitions** below); with the flag off it behaves exactly as before.

- **Config**: the setup file's top-level `clock:` section (`duration`, `hurry`, both `time.Duration` YAML strings, e.g. `"2m30s"`) is persisted to `ClockSettings` on setup submit (`setup.go`). A setup file without `clock:` leaves settings untouched.
- **Routes**: `/ui/mod/game/timekeeping` (`GET` page; `POST start`/`POST cancel`/`POST transitions`/`POST next-match`), all behind `RequirePermission(GAME:TIMEKEEPER)`. Nav entry "Timekeeping" (weight 79, between Schedule and Scorecards). The `/clock` URL is intentionally unused.
- **Start**: valid only when configured (`Duration > 0`, `Hurry < Duration`) and no clock is running. When transitions are enabled the start first resolves a target match and applies the start transitions (below), so a failure there leaves no running clock behind. On success it sets `StartTime = now`, `EndTime = StartTime + Duration`, `HurryTime = EndTime − Hurry`, upserts the row, publishes `game.clock.start`.
- **Match transitions** (`clock_match.go`, behind `Transitions`): the clock drives the match states of the **active phase** (the `GamePhase` with `Active=true`; no active phase → start is rejected, transitions are a no-op). Only `Scheduled`/`Queued`/`Running` are driven; the post-run `Scorable` and the terminal `Complete`/`NoShow`/`Disqualified` are never touched.
  - **Target selection** (`clockMatchTarget`): the current match is the operator's pointer (`NextMatch`, applied only when `NextMatchPhase` matches the active phase) if that match still has a non-terminal placement, otherwise the lowest non-terminal match number; the next match is the lowest non-terminal match after the current one (0 when none).
  - **Start** (`applyClockMatchStart`): current → `Running`, next → `Queued`, and every other stale `Running`/`Queued` placement in the phase → `Scheduled` (one transaction). The pointer then advances to `next` (cleared when there is no match after the current one).
  - **End** (`clockEndMatchTransition`, fired from the end-time timer): the match currently `Running` in the active phase → `Scorable`. Errors are logged and never fail the caller, matching the publish-and-never-fail posture of the auto-stop path.
  - **Cancel** (`clockCancelMatchTransitions`): every `Running`/`Queued` placement in the active phase → `Scheduled`; a missing active phase is a no-op.
  - **Setup file wins**: a setup file's `clock:` section is the source of truth for `Duration`/`Hurry`/`Transitions` on submit (`setup.go`); a re-upload resets those file-sourced fields but preserves the runtime `NextMatch`/`NextMatchPhase` override (set from the timekeeping page). `POST transitions` toggles the flag; `POST next-match` sets or clears (`0`) the pointer.
- **Auto-stop**: on start, two one-shot goroutine timers arm for `HurryTime` and `EndTime`. Each re-checks `activeClockStill` (re-read the row, `StartTime.Equal`) before acting, so a cancelled or replaced run never fires. At `HurryTime` → publish `game.clock.hurry`. At `EndTime` → conditional delete keyed on `EndTime` (auto-stop) + publish `game.clock.end`. Cancel does a conditional delete keyed on `StartTime` + publishes `game.clock.cancel` (payload adds `cancel_time`).
- **Expired = idle**: everywhere the clock is considered running only while `now.Before(EndTime)`, so a stale row left over from a crash or restart is harmless.
- **Events**: all four carry `start_time`/`end_time`/`hurry_time` (RFC3339 JSON); `game.clock.cancel` additionally carries `cancel_time`. Published via `ws.Bus()` when configured (nil bus → no-op).
- **UI**: `partials/clock.p2` is an includable ticking fragment (JS ticks at 100 ms). Contract: `running` (bool), `duration_ms` (duration in **milliseconds**), `hurry_ms`/`end_ms` (epoch **milliseconds**). Display: `M:SS` over one minute, tenths under one minute (`59.8`); red hurry styling once `now >= hurry`; on expiry the readout holds `0.0` for 5 s, then resets to the configured duration. `views/game/timekeeping.p2` renders the fragment plus a Start button (or Cancel button + confirmation modal) gated on `profile | hasPermission:"GAME:TIMEKEEPER"`, and (when configured) a "Clock match transitions" settings panel with the `Transitions` checkbox and the next-match number input. The input shows the match the next run will actually act on (resolved the same way a start resolves it: operator pointer, then lowest non-terminal match via `displayClockNextMatch`), not the raw stored pointer; it shows `0` when the clock has run off the end of the schedule.

#### Phase Score Stats (`schedule.go`, `advancement.go`)

A dedicated per-phase page surfaces the `PhaseScoreStats` values (min, max, average, and the min/max placements with their team, match, and field).

- **Route**: `GET /ui/mod/game/schedule/phases/{id}/stats` (onsite-only, inside the `/schedule` group), guarded by `GAME:ADMIN` via `GuardRoute`.
- **Handler**: `uiViewPhaseScoreStats` loads the phase, calls `PhaseScoreStats`, and renders `views/game/phase_score_stats.p2` with `phase`, `stats`, and absolute back-link paths (`backSchedule` → the per-phase schedule, `backPhases` → the phase list, both built from `m.basePath`).
- **Columns**: the table shows Statistic, Score, Team, Match, and Field. The min and max rows show the source placement's match number and field name (preloaded via `PhaseScoreStats`); the average row shows `-` for Team, Match, and Field.
- **Link**: the phase list page (`phases.p2`) shows a "Stats" button in the Actions cell for each phase that has a schedule, gated on `profile | hasPermission:"GAME:ADMIN"`.
- **Empty phase**: a phase with no scores renders `-` for each row (the template guards on `stats.MinPlacement.Team.ID`).

#### Phase Rankings (`schedule.go`)

A per-phase page showing the final scoreboard rankings of a frozen phase.

- **Route**: `GET /ui/mod/game/schedule/phases/{id}/rankings` (onsite-only, inside the `/schedule` group), guarded by `GuardLogin`: any logged-in user, no permission required.
- **Handler**: `uiViewPhaseRankings` refuses a phase that is not `Frozen`. Otherwise it calls `phaseRankingForPhase`, which calls `scoreboardRankings` once per division from `whenDivisions`: a single call with `""` for a phase that isn't division aware, one call per `team.Division` for a division-aware phase. Rankings therefore use the phase's configured `ScoreSummation` and `TieBreaker`, and ranks restart at 1 in each division. It renders `views/game/phase_rankings.p2` with `tables` (`[]PhaseRanking{PhaseID, PhaseName, Role, Division, Rows}`; divisions with no ranked teams are dropped) and the same `backSchedule`/`backPhases` links as the stats page.
- **Programmatic access** (`advancement.go`): `Module.PhaseRankings(ctx, role PhaseRole, frozenOnly bool) ([]PhaseRanking, error)` returns the rankings of every phase carrying the given role (via `phaseRankingForPhase`), ordered by phase ID. It queries with the string form `Where("phase_role = ?", role)` because GORM drops zero-value struct conditions, which would make the empty-role query match every phase. `frozenOnly` silently skips unfrozen phases (unlike the page handler, which hard-rejects). An unknown role yields an empty slice, not an error. `scoreboardRankings` and `resolveTies` stay unexported so callers cannot fetch rows that skipped tie resolution.
- **Tie resolution** (`resolveTies`): tied groups are ordered by the phase's `TieBreaker` and the whole board behind each reordered group is **renumbered**, so ranks stay strictly ordered by score: a resolved group of k teams expands the board by k−1 places and every later row shifts down by that amount (accumulating across groups). Rows a group did not reorder (cross-division singletons of a division aware phase, which are never compared) still share ranks with each other. `orderTiedGroup` sanitizes the breaker's answer — unknown, repeated, or omitted team numbers are dropped or appended — so a misbehaving breaker cannot drop a row from the board.
- **Columns**: Rank, Team (`number - name`), Score, Matches (score count). There is no pagination. A division-aware phase gets an `<h3>` division heading above each table.
- **Link**: the phase list page (`phases.p2`) shows a "Rankings" button right after "Stats" for each frozen phase, gated on `user.Identity` (logged in).

#### Contest Keying (`module.go`, `export.go`)

Schedule and scorecard rows are keyed to the contest this onsite server is handling, so a site's data can be pushed to the cloud under the right contest (see Cloud Game Push below).

- **Columns**: `MatchPlacement.ContestID` and `MatchScore.ContestID` (both `gorm:"index"`, auto-added by the existing `Migrate()` AutoMigrate). `ContestID = 0` means no contest is selected on the onsite server. `ScorecardValue` carries no `ContestID` of its own; it is reached through `MatchPlacementID` and inherits the placement's contest scope.
- **Source of truth**: the contest module's `OnsiteContest` row (the locally-selected contest). The game module reads it **directly by table name** through a minimal local struct (`onsiteContestRow`, `TableName() = "onsite_contests"`) rather than importing the contest module, because the contest module already depends on the game module and a reverse import would create a dependency cycle. `selectedContestID` is best-effort: it returns the lowest-ID row, or `0` on any error (e.g. a cloud-scope server that never ran the contest module's migration), so a missing table never blocks schedule creation or score entry.
- **Stamping sites** (all three read the placement's or the selected contest's ID at creation time): the schedule preview (`uiViewPhaseSchedulePreview`, `schedule.go`) stamps `ContestID` on every candidate `MatchPlacement`; the scorecard submit (`uiViewScorecardSubmit`, `scorecard.go`) mirrors `placement.ContestID` onto the new `MatchScore`; the debug score generator (`DebugGenerateScores`, `debug.go`) does the same.
- **Export** (`ExportContestData(ctx, contestID)`, `export.go`): returns the contest's placements as portable `ContestPlacement` rows (round, match, phase, team ID + name, field, position, state, score, and scorecard `Values`), ordered by round, match, field, position. Placements without a score or values carry zero values and empty slices; an unknown contest ID yields an empty slice, not an error. This is the source for the cloud push (see Cloud Game Push below).

#### Division Awareness (`module.go`)

`Module.DivisionAware()` reports whether any `GamePhase` row has `DivisionAware: true` (a missing or unreadable phase table is treated as not division aware). It is the single source of truth for whether the game is division aware and drives the BEST module's awards (see BEST below): awards are ranked per division when any phase is division aware, otherwise as a single overall ranking.

#### Clearing Contest Data (`cleardata.go`)

The game module implements `modules.ContestDataClearer` for the contest screen's Clear Data action (see Clearing Contest Data below); the cloud push tables are the only contest-keyed data it owns.

- `ContestDataCounts(ctx)` answers for **every** contest in two grouped `Raw(...).Scan(...)` queries (the generic `gorm.G[T]` cannot scan non-model rows), one over `cloud_match_placements` and one joining `cloud_scorecard_values` through their placement, because values carry no contest key of their own. Labels are `match placements` and `scorecard values`.
- `ClearContestData(ctx, tx, contestID)` deletes the values first and the placements second — Postgres enforces the foreign key and SQLite does not, so the ordering is only observable on Postgres — through the caller's `tx` so the cross-module wipe is atomic, and returns the number of rows removed. Contest 0 ("no contest selected") is refused with `errNoContest`.
- Both methods sit behind a `Migrator().HasTable(&CloudMatchPlacement{})` guard, so a database that has not reached the cloud tables' migration contributes no rows and no error rather than failing the contest list page.

#### Advancement Filters (`advancement.go`, `filters.go`)

`runAdvancementFilters` runs a phase's filters in order against one shared candidate pool. An exclude filter's removals join a blocklist that is stripped from the field every later filter sees. Each filter gets an `AdvancementFilterContext`:
- `Roster` is always the **full** enabled roster, so `len(Roster)` in a `SliceExpr` means the same thing as in a phase `Suppress` or filter `When` expression.
- `Remaining` is the roster minus the blocklist, and `Scoreboard` is filtered the same way.
- Filters that select from the roster use `sctx.Pool()` (which returns `Remaining`, or `Roster` when `Remaining` is unset), never `Roster` directly, so a blocked team cannot be re-advanced.

A phase only counts as complete once it has at least one finished placement, so a phase that would schedule zero teams must be suppressed (`Suppress`). Any later filter that reads from it must also be gated off with a matching `When`; otherwise the downstream phase can never be scheduled. `test_data/byte2bite.yml` does this for the Wildcard (`Suppress: "len(Roster) < 8"` paired with `When: "len(Roster) >= 8"` on the Semifinals `WildcardWinner` filter).

**Readiness** (`filters.go`, `advancement.go`): `AdvancementFilterReadiness` is an optional interface (the `awardDiagnoster` pattern) an `AdvancementFilter` may implement to answer "can I produce a correct answer right now" — never "will I select anyone". `CanRun(ctx, *AdvancementFilterContext) (bool, string, error)` returns a verdict plus an operator-facing reason; a filter that does not implement it is always ready, and a `CanRun` error is treated as not-ready with the error message as the reason. `phaseSchedulable` builds the verdict `AdvancementReadiness{CanRun, Reasons}`: it collects **all** blockers (a missing/unfrozen source phase names the source by name; a filter's own not-ready verdict names the rule), skips `When`-false filters entirely, and never asks `CanRun` when the source phase already blocks (the filter would read an empty board). Readiness is measured with `filterContext(teams, board, nil)` — an empty blocklist, whole-field board (`division == ""`) — via the same `filterScoreboard`/`filterContext` builders `runAdvancementFilters` uses, so measured field and applied field are built identically. The BEST `NotebookAdvancement` implements it: not ready until any in-scope team (scoped by the shared `notebookScope`) has a `notebook` (index 0) score.

**Enforcement** (`schedule.go`): `scheduleGate` runs inside the select-teams, preview, and accept handlers and renders `views/game/schedule_blocked.p2` (reason list + "Generate manually anyway" link) when the verdict is negative. The operator escape hatch is `override` (query or form field, read via `r.FormValue`): it skips the gate and unlocks the manual team picker (`manualEnabled`); the preview template re-emits it as a hidden field in the accept form so it survives the relative-URL post. `phaseSequence` is a Kahn topological sort over the `SelectFrom` dependency graph (ties on phase ID; a cycle logs a warning and falls back to ID order, never hangs) and drives `uiViewPhaseList`'s `nextPhase` map: **only the next phase up** gets a Generate Schedule control — enabled when ready, or a `<span class="button is-disabled" aria-disabled="true">` with the joined reasons in `title=` and a `<p class="help">` line when not (no `pointer-events: none`, which would suppress the native tooltip; the CSS lives in `theme.css` because Bulma 1.0.4 ships no `button.is-disabled` and no tooltip). The sequence is presentation-only — readiness, not sequence, is what the server enforces.

### BEST (`modules/best/`)

Onsite-only, optional module. Tracks non-match "BEST" scores per team and computes awards from them.

| Model | Fields | Purpose |
|-------|--------|---------|
| `ScoreType` | ID, Key (unique), Name, Max, Order | Score category (table `best_score_types`) |
| `TeamScoreValue` | ID, TeamID, ScoreTypeID, Value, SetAt | One team's value in one category; unique on (TeamID, ScoreTypeID) (table `best_score_values`) |

`Migrate()` seeds the four default categories (idempotent get-or-create, updates drifted Name/Max/Order): notebook (Max 300, Order 1), marketing (250, 2), poster (100, 3), video (100, 4).

**Scores page** (`scores.go`, `GET /ui/mod/best/scores`): a whole category judged below 33% of its maximum is almost always scores recorded on the wrong scale, so the page warns about it. `lowScoreCategories(rows)` returns a `lowScoreWarning` (Name, Max, Threshold, Scored) for each category that has at least one entered score whose highest score is under 33% of `Max`; `belowLowScore` compares by cross-multiplication so a score of exactly 33% is never flagged. Categories are keyed by column index seeded from the widest row (with `nil` placeholders for unusable maxima), never from the scored cells alone — otherwise a team whose only score sits in a later category lends that category's identity to the earlier columns it left blank, duplicating one warning and dropping another. Categories with no scores yet stay silent, so the warning cannot appear before judging starts. `uiViewScores` passes them as `low_scores` and `scores.p2` renders one `notification is-warning is-light` per category above the table, with both numbers (`Max`, `Threshold`) through `floatformat:2` so raw float32 output never leaks into the text.

**Awards** (`awards.go`, `GET /ui/mod/best/awards`, `BEST:ADMIN`-guarded; "Awards" nav child):
- **Registry**: `awardDefinitions()` returns the `[]Award` (best, game, notebook, marketing, poster, video, in display order). An `Award` is declarative: `Key`, `Name`, `Description`, a candidate source, `DivisionAware`, `Order`, and `PodiumRanks` (how many places show inline before the rest collapse into the expandable full ranking; zero means the default 3). Adding an award is appending one `Award`; a new metric is a new `Scorer` (`Score(ctx, teams) (map[uint]float64, error)`, a team absent from the map is ineligible). The engine, handler, and template are shared by every award. The category scorer, `bestCategoryScorer`, scores a team by its `TeamScoreValue` in one category.
- **BEST Award** (`best_award.go`, key `best`, `Order` 0): the 1000-point composite of rules §5 — notebook (300) + marketing (250) + poster (100) + video (100) + Robot Performance (250). `bestAwardScorer` sums the judged values through `scoreBuffer` and adds the derived Robot Performance points. All five components are required for consideration: a team is eligible only when every component is *present* (an absent value, not `0`), so a team missing any one is absent from the map no matter how many points it earned; `Disabled` teams are never eligible. The award rides the shared engine, so division grouping, the canonical tie-break chain, and the podium split all apply unchanged.
- **Robot Performance** (`performance.go`): the fifth component is never a `ScoreType` — it has no row, no migration, no scores-page cell, and no CSV column, because it is recomputed from live results on every read. `performanceFromSeeding.Points` reads `GameModule.PhaseRankings(ctx, game.RoleSeeding, true)` and maps each team's rank linearly from `performanceMax` 250 (rank 1) to `performanceFloor` 25 (last place), rounded to whole points (`performancePoints`; a one-team field earns 250). The phase's own `ScoreSummation`/`TieBreaker` decide the ranking, and the first `Seeding`-role phase by ID wins. `performanceTable.State` distinguishes `ready` / `notFrozen` / `noPhase` / `noGameModule` (an empty frozen-only result is re-queried with `frozenOnly=false` to tell "not final yet" from "no such phase"), which the awards page turns into an explanatory note. **Grouping follows the award**: Robot Performance is ranked the same way the BEST Award is given out. `awardDefinitions` passes the award's `DivisionAware` (the game module's `DivisionAware()`) as `performanceFromSeeding.byDivision`. When the award is overall (no phase is division aware) every team is one field and only the contest's top team earns 250. When the award is per division, `derivePerformance` regroups the rows by `row.Team.Division.Name`, because a `Seeding` phase that is not `DivisionAware` yields one field-wide table (see `whenDivisions`), which would rank a small division's best team behind the whole field. The regroup is a no-op for a division aware phase. In both cases the local rank advances only when the phase's rank changes — no tie is ever invented or broken — and points use the group's own N. `SplitByDivision` records when a flat phase was split and is surfaced as a note.
- **Eligibility diagnosis** (`awards.go`): `awardDiagnoster` is an optional interface a `Scorer` may implement to explain which candidates it excluded and why. `computeAwards` calls `Diagnose()` after a `Scorer` returns and stores the result on `awardResult.Diagnosis`, plus `Diagnosed` as the template-visible flag (pongo2 cannot test a nil pointer for truth: a pointer kind is not one `IsTrue` handles, so templates must branch on `Diagnosed`, never on `Diagnosis`). `awardDiagnosis` carries `Notes` (rendered as warning notifications), `Components` (one `diagnosisColumn` per component, `Header` like `Notebook /300`), and one `candidateRow` per non-disabled team with its `Cells` (2dp, `—` when absent), `Missing` names, and `Ready`. `awards.p2` renders it as an expandable Eligibility table and shows "No team has completed all five components yet." when nothing is rankable; `scores.p2` carries a footnote that its Total covers the judged categories only.
- **Game Award** (`game_award.go`, key `game`, `Order` 1, `PodiumRanks` 4): the second candidate source, the `Ranker` interface (`Rank(ctx, teams) ([]RankedGroup, error)`), for awards whose places are decided elsewhere rather than by a BEST score. `finalPhaseRanker` reads the frozen phase carrying `game.RoleFinal` through `GameModule.PhaseRankings`, so places are the finals' own ranks (that phase's `ScoreSummation`/`TieBreaker`) and are never re-ranked with the BEST tie-break chain. The award is simply 1st through 4th place, rendered with plain numeric ranks like every other award; all four show inline. A division aware final yields one group per division that has ranked teams, following the phase's own division split rather than the contest-wide `DivisionAware()` flag; an unfrozen or absent final yields no groups, and a `Ranker` ignores `DivisionAware` because it names its own groups. `computeAwards` feeds a `Ranker`'s groups straight into the shared `awardGroup` shape (entries within `PodiumRanks` become the podium).
- **Division-awareness**: every award's `DivisionAware` is driven by the game module's `DivisionAware()` method (see Division Awareness above): awards are ranked per division when any `GamePhase` is division aware, otherwise a single overall ranking. The best module reaches the game module through a local `GameModule` interface type-asserted from `deps` (nil-safe: a missing game module falls back to overall ranking), and declares `Dependencies: ["game"]` in `init.go`.
- **Ranking** (`computeAwards`): candidates are grouped by division name when division aware (teams with no division land in an "Unassigned" group; the non-division-aware path is one "Overall" group). Within a group, teams sort by primary score, then the canonical tie-break chain notebook → marketing → poster → video (higher first), then team number (ordering only, never breaks a rank tie). Ranking is dense: co-winners (tied on primary and every tie-break value) share a rank and the next distinct rank is previous + 1. Each group exposes `Podium` (ranks ≤ 3, co-winners included), `Complete` (full ranking), and `More` (Complete extends past the podium).

### Contest (`modules/contest/`)

Dual-scope, required module. The cloud side manages contests (named, associated with a region, carrying a roster of teams); the onsite side exposes a "My Contest" screen where the operator picks which cloud contest this onsite server is handling.

| Model | Fields | Purpose |
|-------|--------|---------|
| `Contest` | ID, Name, Frozen, RegionID (→ team.Region) | Contest record (cloud) |
| `ContestRoster` | ID, ContestID (→ Contest), TeamID (→ team.Team) | Roster row; unique on (ContestID, TeamID) (cloud) |
| `OnsiteContest` | ID (the cloud contest ID), Name, Region (plain strings) | Local record of the selected contest (onsite) |

**Frozen-ness**: a frozen contest is finished. `Contest.Frozen` is flipped by `POST /{id}/freeze` and `POST /{id}/unfreeze` (both `CONTEST:ADMIN`-guarded; the path suffix decides the new state, mirroring the game module's score-visibility toggle, and each transition is logged with the acting user's identity). While frozen, the contest record can no longer be edited or deleted and its roster can no longer be changed: `uiViewUpsert` (edit only), `uiViewDelete`, `uiViewRosterInit`, `uiViewRosterAdd` and `uiViewRosterRemove` run through `frozenGuard`, which renders the error page naming `ErrContestFrozen` ("contest is frozen") as the reason. The roster and contest stay readable. Freezing is reversible, so an operator who freezes too early can unfreeze and continue. The cloud's score upload endpoint refuses frozen contests with `409 Conflict` (see Cloud Game Push). The contest list page shows a Frozen/Open tag per contest and hides the Edit/Delete actions behind the disabled styling when frozen; the roster page shows a warning notification and disables its mutation controls.

**Roster** (`/{id}/roster` routes, read page public, mutations `CONTEST:ADMIN`-guarded):
- The roster is independent of the region: `POST /{id}/roster/init` seeds it from the contest's region (enabled teams only; idempotent, existing rows are skipped) and afterwards add/remove edits it directly.
- `GET|POST /{id}/roster/add` adds any team not already on the roster; `POST /{id}/roster/{team_id}/delete` removes one.
- Deleting a contest cascades to its roster rows.

**Clearing Contest Data** (`module.go`, `POST /{id}/clear`, `CONTEST:ADMIN`-guarded, cloud scope only): erases every row the other modules hold for one contest, which is the only operator recovery path when a site pushed results under the wrong contest ID or re-scheduled after pushing (the push upsert keys on the natural match key, so rows from a stale schedule are never evicted on their own).
- **Discovery**: the module collects `modules.ContestDataClearer` implementations from `deps` at construction (`clearersFrom`, name-sorted so the wipe and its audit log are deterministic); a module that does not implement it is skipped, so a module built with `nil` deps simply clears nothing and reports zero rows.
- **Counts**: the list page calls `ContestDataCounts(ctx)` once per clearer (a grouped query per table, never a query per contest) and merges the results; a clearer's error is `slog.Error`'d and skipped so the list page stays usable. The state travels on each row as an embedded struct (`contestListItem{Contest; Clear}`), because **pongo2 cannot index a `map[uint]T`** (`{{ m.1.X }}` is a parse/exec error and a variable key panics on the type conversion) — attach per-row data via embedding, which promotes the original fields so existing template lookups keep working.
- **Refusals and confirmation**: a frozen contest is refused through `frozenGuard` (see Frozen-ness). The operator must type the contest's name exactly; the check is server-side (case-sensitive, whitespace-trimmed) and the modal's disabled submit button is friction only. A mismatch redirects back with `?clear_error=name` and deletes nothing.
- **Atomicity and audit**: every clearer runs inside one `m.db.Transaction`, so a failure part-way through leaves the contest's data untouched; per-module `slog.Info("Contest data cleared", ...)` lines (contest, name, module, rows, operator) are written only after the commit, so a rollback never claims deletion. Success redirects with `?cleared=<rows>&cleared_for=<id>`; the list page resolves the ID back to a name for the notification (empty when the contest is gone).
- **What is and is not touched**: only the cloud push records (`cloud_match_placements` and, through them, `cloud_scorecard_values`). The contest record, its roster, teams, divisions, regions, the game configuration, and BEST scores all survive. Erasure is not reversible from the UI, but pushes are never blocked by it, so the next correct site push repopulates the cloud Schedules and Scorecards pages.
- **UI**: a `Clear Data` button sits in each row's Actions cell, `is-disabled` + `aria-disabled="true"` when the contest has no stored rows or is frozen (with the reason in `title=`; never `pointer-events:none`, which would swallow the tooltip). Contests with data and not frozen get a per-row Bulma modal (`list.p2`, modeled on `my_contest.p2`) itemizing the labelled counts and total, stating explicitly what the wipe does not touch and that a re-push recovers, and gating its submit on the typed name matching the input's `data-confirm`.

**Cloud endpoints** (public, served by the cloud `Module`):
- `GET /ui/mod/contest/cloud/contests` → `[]cloud.ContestInfo` (cloud ID, name, region, `frozen`; ordered by name). The ID is carried on purpose: an onsite server adopts the cloud contest ID as its own local contest ID.
- `GET /ui/mod/contest/cloud/contests/{id}/roster` → `[]cloud.TeamInfo` (number, name, division, region) for that contest's roster.

**My Contest (onsite)** (`onsite.go`, `CONTEST:ADMIN`-guarded):
- `GET|POST /ui/mod/contest/` lists the cloud contests (fetched up front so cloud failures surface on the page) and persists the selection: the local `OnsiteContest` row's ID is the chosen cloud contest ID; re-selecting replaces the previous row.
- Frozen is cloud state, not local state: the page annotates each contest option with "frozen" and, when the currently selected contest is frozen on the cloud, shows a warning notification (the flag is read from the freshly fetched cloud list into `selectedFrozen`, since `OnsiteContest` stores no flag of its own).
- **Auto-import on selection** (`importContestData`, best-effort, log-and-continue so a transient cloud failure never blocks the selection): (1) if `ListTeams()` shows the local team list is empty, the selected contest's roster is pulled via `Client.GetContestRoster` and upserted through `team.Module.ImportTeams` (the same upsert the cloud/CSV imports use; blank division defaults to `Open`); (2) if no game is loaded (zero `GamePhase` rows), the cloud game file is pulled via `game.Module.ImportGame` (the same `applyGameConfig` as a local upload). The onsite module reaches team/game through local `TeamModule`/`GameModule` interfaces type-asserted from `deps` (nil-safe, mirroring the game module's `TeamModule` pattern), which is why `init.go` declares `Dependencies: ["team", "game"]`.

### Schedule Generation (`pkg/schedgen/`)

| Type | Fields | Purpose |
|------|--------|---------|
| `Config` | Fields, Positions, Teams, Rounds | Scheduler inputs |
| `DivisionConfig` | ID, Fields (pinned, empty = auto), Rounds (0 = global), TeamIndices, NoCompact | Division-aware scheduling inputs |
| `Schedule` | Config, Rounds, score metrics, RoundsDynamic, Interleaved | Generated schedule |
| `Round` | Matches ([]Match), TeamAppearances | Single round |
| `Match` | Placements (map[Location]int), DivisionID | Match with team assignments |
| `Location` | Field (int), Position (int) | Field + position pair |

### Build Info (`pkg/buildinfo/`)

| Variable | Default | Injected via |
|----------|---------|--------------|
| `Version` | `"dev"` | `-ldflags -X ...Version=<ver>` |
| `Commit` | `"UNKNOWN"` | `-ldflags -X ...Commit=<hash>` |
| `BuildDate` | `"UNKNOWN"` | `-ldflags -X ...BuildDate=<date>` |

## Scheduling

### Generators

| Name | Algorithm | Notes |
|------|-----------|-------|
| `Annealed` | Late-acceptance local search + baked archive | High quality; prefers embedded baked artifacts |
| `BID` | Balanced Incomplete Block Design | Pairwise coverage; `RoundsDynamic=true` |
| `RandomSeeding` | Random team assignment | Simple random placement |
| `OneShot` | One-shot generation | Single-pass algorithm |

### Annealed Generator (`anneal.go`, `baked.go`)

The quality-first generator. Registered as `Annealed`, it works in two modes:

1. **Baked (preferred)**: `Generate()` first consults the embedded artifact
   archive (`LookupBaked`). Artifacts live in `pkg/schedgen/baked/` and are
   compiled in via `embed.FS` (`//go:embed all:baked`). A lookup matches on
   (teams, fields, positions) and any artifact with `rounds >= requested`; the
   smallest sufficient round cap wins so truncation discards as little design
   as possible. `Materialize` truncates the design to the requested rounds
   (uniform — each team plays exactly once per round, so the cap stays exact
   and replay gaps can only widen) and applies a fresh random team labeling
   from `Options.Seed` (0 = "no preference": a fresh labeling seed is drawn
   per call, so two events of the same shape never receive identical team
   assignments; a non-zero seed reproduces the labeling exactly). All
   schedule metrics are relabeling-invariant, so one baked design serves
   every event of its shape.
2. **Live search**: with no covering artifact, `AnnealSchedule` runs late-
   acceptance hill climbing (LAHC, history 64) bounded by
   `liveAnnealCeiling` (1M moves) so HTTP requests stay fast.

**Fixed shape = structural round cap.** The search permutes teams into
(round, wave, slot) positions where each round contains every team exactly
once, plus ghost entities (ids ≥ Teams) for the partially-filled last wave
when `teams % (fields×positions) != 0`. Ghost appearances are byes. Every
move class (within-round slot swap, per-round wave rotation, transposition of
two teams over a round subset) is a bijection on this structure, so
`Config.Rounds` is honored with equality in every intermediate state — a cap
violation is impossible, and `Schedule.Validate()` never rejects a search
result.

**Cost** (weighted, all sums so single swaps produce gradients; per-team
shortfalls are squared so the least-served teams dominate and variance
collapses): replay shortfall below one round of spacing ×200 (quadratic in
the shortfall), distinct-opponent shortfall below
`min(rounds×(positions−1), teams−1)` ×1200, and per-(team, location) usage²
×50. Measurements (40 teams / 2 fields / 4 positions / 6 rounds):
`RandomSeeding` achieves closest-replay 2 / 13 distinct opponents; the
annealer achieves 3–4 / 18, which is the theoretical maximum.

**Options** (`AnnealOptions`): `Iterations` (0 = scaled, capped for live use;
bake default 5M — measured convergence point up to ~100 teams), `Restarts`,
`History`, `Seed` (0 = fresh seed per call so live requests vary; a fixed
seed ⇒ reproducible results, and bake passes one), `SkipBaked` (bake
sets this so it never re-emits an existing artifact). `BakeOptions()` returns
the bake defaults. Division-aware scheduling is unchanged: each division is
generated (baked or annealed) independently before interleave.

### Baked Schedule Artifacts (`baked/`, `baked.go`)

YAML files named `teams<T>-fields<F>-positions<P>-rounds<R>.yml` where `R`
is the round cap. `grid` holds one flow-style row per round; entries are
zero-based team indexes, `-1` = empty slot. Header records the search
metadata (`iterations`, `restarts`, `seed`, `cost`) for reviewability.
Adding a shape = run `bake schedules`, commit the files, rebuild.

**API**: `LookupBaked(Config) (*BakedSchedule, error)` (nil, nil when no
cover), `BakedSchedule.Materialize(Config, seed)`, `BakedSchedule.Validate()`
(grid is a per-round permutation of teams + correct empty slots),
`BakeSchedule(Config, opts)` (always searches, never reads the archive),
`BakedShapes()`, `BakedShapesInDir(dir)`, `ParseBaked(data)`,
`BakedSchedule.Marshal()` (flow rows, parse-compatible with `ParseBaked`),
`BakedFileName(t,f,p,r)`.

**CLI** — `gameday bake schedules` (`internal/cmd/bake_schedules.go`):
batch-bakes a cross product of `--teams/--fields/--positions/--rounds`
ranges, writing artifacts straight into the embed directory (default
`pkg/schedgen/baked`). Range syntax: comma lists and inclusive spans with
optional step — `"8-12,16,24-40:4"`. Flags: `-o/--out` directory,
`--iterations`, `--restarts`, `--seed`, `-w/--workers` (shapes annealed
concurrently), `--force` (re-bake existing), `--dry-run` (print names
only), `--list` (show shapes covered by `--out`). Existing artifacts are
skipped unless `--force`, so a large bake can be interrupted and resumed.
Each bake round-trips through `Materialize` + `Score` before writing, so
artifacts are validated on production and the report shows live-visible
metrics. Example:

```bash
# One weekend bake for typical club-event sizes.
gameday bake schedules --teams 8-64 --fields 1-3 --positions 2-4 --rounds 3-8 -w 8
```

### Division-Aware Scheduling

`GenerateDivisionSchedule(generator, cfg, divisions)` (`division.go`):
1. Generates an independent schedule per division.
2. Pins divisions to fields (`DivisionConfig.Fields`) or auto-assigns remaining fields evenly (scarce fields are shared across divisions).
3. Interleaves division schedules round-by-round, remapping local field indices to global pinned fields and tagging each match with its `DivisionID`.
4. Greedily compacts matches that use non-overlapping fields unless the division's `NoCompact` is set.

The result is flagged `Interleaved: true`; per-team round-count validation happens pre-interleave per division. The game module's schedule UI (`schedule.go`) builds `DivisionConfig` values from `team.Division` rows (including per-division `NoCompact`) and calls this.

### CLI Usage

```bash
gameday bake schedule <generator> <rounds> <fields> <positions> <teams>
```

Output rendered via `github.com/jedib0t/go-pretty/v6/table`.

### Scoring Metrics (`score.go`)

`Schedule.Score()` evaluates:
- **ClosestReplay** — minimum match gap between any team's appearances
- **WorstLocationDiversity** — distinct (field, position) pairs per team
- **WorstCompetitorDiversity** — distinct opponent teams per team
- **TeamAvgScore** — aggregated team quality score

`Schedule.Validate()` ensures every team plays the expected number of rounds.

## Event Bus (`pkg/event/`)

`pkg/event` implements a framework-agnostic publish/subscribe event bus used for real-time updates. It has no web/HTTP dependency in the core (only stdlib + `github.com/coder/websocket` for the websocket export).

**Core types** (`event.go`):
- `Event{Type string, Data any}` — what producers publish. Types are dot-separated strings, e.g. `game.match.state` (convention: `<module>.<noun>.<fact>`). Reserved system types: `subscribed`, `close`.
- `Envelope{Seq uint64, TS time.Time, Type string, Data json.RawMessage, Replay bool}` — what consumers receive and the JSON wire format. `Seq` is a monotonically increasing per-bus sequence number; consumers can detect gaps (dropped events) from it.

**Producer API** (`bus.go`):
```go
if ws.Bus() != nil {
    ws.Bus().Publish(ctx, event.Event{Type: "game.match.state", Data: payload})
}
```
`Publish` marshals `Data` to JSON eagerly (fail fast) and never blocks on slow consumers. Returns `event.ErrClosed` if the bus is shut down, `event.ErrBadType` for empty/reserved types.

**Consumer API (Go)**:
```go
ch, _ := ws.Bus().Subscribe([]string{"game.match.state"}) // nil = all types
```
Each subscriber gets a buffered channel (default 256). If a subscriber's buffer is full, events are dropped for that subscriber only (logged via slog); producers and the dispatcher are never blocked.

**Websocket export** (`handler.go`): mounted at `/ui/events` (open by default, like the public scoreboard data endpoint; wrap in `RequirePermission` to restrict). Query params:
- `types` — comma-separated filter (empty = all)
- `replay` — number of recent envelopes to replay on connect (each flagged `replay: true`); backed by an all-types ring buffer (default 256, `event.WithReplaySize(0)` disables)

Every connection receives a `subscribed` envelope first (`{"types":[...],"seq":N}`), then replay (if requested), then live events. On bus shutdown a `close` envelope is sent and the connection closes cleanly.

**Server wiring**: `web.EventBus(bus)` option attaches the bus; `Server.Bus()` returns it (nil if not configured — modules must nil-check). `onsite_serve.go` creates the bus, passes the option, handles `/ui/events` via `bus.Handler()`, and calls `bus.Close()` on shutdown. Modules reach the bus through the `*web.Server` they already hold; no module needs to import `pkg/event` unless it consumes events.

**Browser client** (`pkg/web/ui/static/js/eventbus.js`, served at `/static/js/eventbus.js`):
```js
const bus = createEventBus({ url: '/ui/events', types: ['game.match.state'], replay: 100, onEvent, onStatus });
bus.close();
```
Vanilla WebSocket; pings every 15s, gap-detects via `seq`, reconnects with exponential backoff + jitter.

## Sound Effects (`pkg/sfx/`)

`pkg/sfx` plays the short sounds an event runs on through the machine's audio output, using `github.com/gopxl/beep/v2` (WAV decoding + `speaker` playback; the audio device itself is opened by `oto`, which needs no CGO).

- **Player**: `sfx.New(opts ...Option) *Player`. `Play(name)` starts a sound and returns once playback is queued; `PlayAndWait(name)` blocks until it finishes, which is what a play-and-exit command needs so the sound is not truncated by process exit; `Duration(name)` reports how long a sound runs; `Stop()` silences; `Close()` releases the device (player unusable afterwards, `ErrClosed`); `Sounds()` lists the available names (sorted, extension stripped). Safe for concurrent use.
- **One sound at a time**: `Play` cuts off whatever was already playing (the speaker is cleared first), because an announcement should interrupt the previous sound rather than queue behind it. `Stop` does not unblock a `PlayAndWait` (beep's `speaker` has no cancellable wait), so the two are not combined.
- **Names**: base names without extension — the constants `StartOfMatch`, `MatchAlmostOver`, `MatchFault`, `EndOfMatch`. A name may be given with its `.wav` extension too; an unknown name is `ErrNotFound`, resolved *before* the audio device is opened.
- **Embedded library**: `//go:embed all:sounds` in `embed.go`, rooted via `fs.Sub` at the files themselves so callers address `start-of-match.wav`, not `sounds/...`.
- **Source replacement**: `GAMEDAY_SFX` (read once by `New`) points the player at a directory on disk instead; anything that is not a readable directory is warned about and falls back to the embedded library. `WithDir(dir)` does the same explicitly, and `WithFS(fsys)` supplies any `fs.FS` (used by tests). Precedence: `WithFS`/`WithDir` > `GAMEDAY_SFX` > embedded.
- **Decoding**: sounds are decoded on first `Play` and their samples cached (`beep.Buffer`), so repeats are cheap and decode errors surface on the call that hit them (a corrected file becomes playable). Sounds are resampled to 48 kHz at quality 6, since the library mixes 11 kHz whistles with a 44.1 kHz voice recording.
- **Failure policy** (`speakerSink`, process-wide — the audio library allows one device per process): a device that reports it is unusable (headless server, no sound card) is logged once and remembered, so a machine without speakers never becomes an event-day problem. A device that merely fails to answer within `openTimeout` (5 s) yields `errOpenTimeout` for that call and is retried for the next sound; the attempt is never retried in parallel.
- **Not wired into the server**: no module or serve path constructs a `Player`; sound playback lives in the `onsite sfx` and `onsite tray` commands, which run on the machine connected to the speakers (see Announcer below).

## Announcer (`pkg/announcer/`)

`pkg/announcer` turns the match clock's events into sounds. It subscribes to the clock and plays the matching sound, so the arena hears the clock while operators run it from the timekeeping page.

- **Mapping** (the wire contract, spelled out rather than imported from `modules/game` because an announcer runs against a server it does not share a build with; `SoundFor` and `EventTypes` expose it): `game.clock.start` → `start-of-match`, `game.clock.hurry` → `match-almost-over`, `game.clock.end` → `end-of-match`, `game.clock.cancel` → `match-fault` (teams who hear a cancelled run must stop either way, and silence would leave them waiting for a start that is not coming). Only the envelope's `Type` is read; the clock's payload is ignored.
- **Two modes**: in-process (`WithBus`, announces a bus the same program created) and remote (the default — dials `<server>/ui/events?types=...`, the websocket `onsite serve` already mounts) so the box with speakers need not run the server.
- **Server address**: `WithServer(base)` or `GAMEDAY_SERVER` (e.g. `http://192.168.1.10:8080`); otherwise `DefaultServer()` builds `http://localhost:<port>` from `GAMEDAY_PORT` (default `8080`). A base URL may carry its own path and query, which are preserved with `types` appended. `Validate()` reports a malformed address to the caller that supplied it, rather than to a background retry loop; both commands validate up front.
- **Reconnect**: remote mode retries with capped exponential backoff plus jitter (500 ms → 15 s, 250 ms jitter, mirroring `eventbus.js`), resetting the attempt counter after the server's `subscribed` envelope, so an announcer started before the server still works. `Run(ctx)` blocks until the context ends, returning its error (`onsite sfx` treats `context.Canceled` as an ordinary exit).
- **Replayed events are skipped**: an envelope flagged `Replay` makes no sound, so a reconnecting announcer cannot announce a match that ended minutes ago.
- **A failing sound never ends the run**: playback errors are logged and the subscription continues, so a machine whose audio drops mid-event stays useful.
- **`onsite sfx [sound]`** (`internal/cmd/onsite_sfx.go`): with one argument, plays just that sound via `PlayAndWait` and exits; with none, announces the clock until `SIGINT`/`SIGTERM`. `--list` prints the playable names. Names are matched leniently (`normalizeSoundName` trims space and `.wav`, lowercases, and splits camelCase/underscores, so `StartOfMatch`, `start_of_match` and ` START-OF-MATCH.wav ` all resolve); an unknown name exits 2 with the available list in the log.
- **`onsite tray`** (`internal/cmd/onsite_tray.go`): a `clockPlayer` switch owns the announcement (on at startup, matching the checkbox), toggled by a `Play clock sounds` checkbox and turned off when the applet exits. Checkbox state is gameday's, not `systray`'s — the library never flips a checkbox itself, so the callback writes back `SetChecked(clock.toggle())`. Playback happens on goroutines because menu callbacks run on the platform event loop. Every sound in the library gets a top-level `Play <Sound>` entry (`titleCase` for the name: `start-of-match` → `Start Of Match`) that plays it immediately, so levels and output selection can be checked without starting a match. **Test entries must not be a submenu**: whether a submenu ever receives its children is the tray host's choice, and hosts that expand lazily ask `AboutToShow` before fetching — `systray` v0.3.0 answers `false` unconditionally (`internal/platform_linux.go`), so the children are never fetched and the submenu opens empty. An unusable `GAMEDAY_SERVER` makes the tray indicator-only: the checkbox is omitted and the reason logged, rather than hidden in a retry loop.
- **Testability**: `Announcer.player` is an unexported one-method interface (tests assign it directly, since `WithPlayer` takes the concrete `*sfx.Player`); `Announcer.dial` stands in for the websocket; `startClockPlayer(ctx, listen)` is the tray's seam. Remote tests run against `httptest.NewServer(bus.Handler())`, and because a subscription sees only post-subscribe events they republish until a sound arrives (`publishUntilPlayed`).

## Cloud Client (`pkg/cloud/`)

`pkg/cloud` is the HTTP client for talking to the cloud instance. It is stdlib-only (no new `go.mod` dependencies) and is the single place onsite→cloud communication happens; modules never issue outbound HTTP directly.

- **Base URL**: `New(opts ...Option)` reads `GAMEDAY_CLOUD` (fallback `DefaultBaseURL = "https://gameday.gizmoplatform.org"`) and trims trailing slashes. `WithBaseURL` overrides the env var; `WithTimeout` overrides the 10 s default. `Client.BaseURL()` exposes the resolved base.
- **Auth seam**: `Authenticator` is a single-method interface (`Apply(ctx, *http.Request) error`) installed via `WithAuthenticator`, or the `WithBasicAuth` / `WithBearerToken` shortcuts which wrap the ready `BasicAuth{Username, Password}` (sets `Authorization: Basic ...`) and `BearerAuth{Token}` (sets `Authorization: Bearer ...`) types. When no authenticator option is given, `New` falls back to `GAMEDAY_CLOUD_TOKEN`: if set, every request carries `Authorization: Bearer <token>`; an explicit option always wins over the env var.
- **Wire**: `Do(ctx, method, path, query, out)` builds `base + path + query`, sets `Accept: application/json`, applies the authenticator when set, and decodes the JSON body into `out` (`nil` → discarded). `DoBytes(ctx, method, path, query)` returns the full raw body untouched, for non-JSON payload types (the game setup YAML). `DoJSON(ctx, method, path, query, body, out)` is the body-capable variant: it marshals `body` to JSON, sets `Content-Type: application/json` (only when `body` is non-nil), and decodes the response into `out`. Non-2xx responses return `*cloud.Error` (status + trimmed first 512 bytes of body).
- **Per-feature convention**: each cloud feature owns a sibling file in the package with its wire types and methods (e.g. `teams.go` with `RegionInfo`/`TeamInfo` + `GetRegions`/`GetRegionTeams`; `games.go` with `GetGameFile`; `scores.go` with `ContestPlacement` + `PushContestData`). The game feature deliberately has no client-side wire types for the setup file: the raw YAML setup file is the single source of truth and is applied by the same parser/upsert as a local file upload. Add the next feature as a new file, not by growing `client.go`.

### Team Cloud Endpoints (served by a cloud instance)

The `team` module exposes two public read-only JSON endpoints, keyed to the `pkg/cloud` wire shapes:

- `GET /ui/mod/team/cloud/regions` → `[]RegionInfo` (name + team count; includes disabled teams and zero-team regions; ordered by name).
- `GET /ui/mod/team/cloud/teams?region=X` → `[]TeamInfo` (number, name, division, region; unknown region → 404 `{"error":"region not found"}`; omit `region` for every team; ordered by number).

### Cloud Team Import (onsite)

`GET/POST /ui/mod/team/import-cloud` (both `TEAM:ADMIN`-guarded; a "Cloud Import" child sits in the Team nav next to "Bulk Import") lists the cloud regions with per-region counts, and a per-region POST pulls the roster via `Client.GetRegionTeams` and applies it through `upsertTeamRows` — the same upsert the CSV bulk import uses. The upserts on team number, get-or-creates the divisions/regions the rows reference, defaults a blank division to `Open`, and never writes `disabled` (a re-import cannot resurrect a manually-disabled team). Success → 303 to `?imported=N`; a cloud failure renders the import page with an error notification and writes nothing.

Note: `GAMEDAY_CLOUD` is the **base URL of the cloud instance the client dials**; `GAMEDAY_CLOUD_DB` is the **local SQLite file path** `cloud serve` stores its database at. They are distinct — do not conflate.

Also note route registration is scope-exclusive (see Scope-conditional routes above): the factory receives the entrypoint scope and the cloud-scoped module registers the shared management screens plus the cloud pull endpoints, so the cloud JSON endpoints do not exist on onsite instances and the import UI does not exist on cloud instances. The same applies to the game module's cloud endpoints and import UI.

### Game Cloud Screens (served by a cloud instance)

The cloud-scoped `game` module also serves the shared management screens: `GET /ui/mod/game/active-configuration` (Configuration, `GAME:ADMIN`-guarded) displays the active game config, and `GET/POST /ui/mod/game/setup` (Setup, `GAME:ADMIN`-guarded) uploads a game setup file through the same `applyGameConfig` as the onsite local upload. The cloud instance is where the game design is authored and maintained before it is pulled to the sites.

### Game Cloud Endpoints (served by a cloud instance)

The `game` module exposes two cloud endpoints. `GET /ui/mod/game/cloud/setup` returns the configured game as a `text/yaml` document in the same shape the local setup upload accepts (`Field.Positions`, `Game.Phases` with `AdvancementFilters`, `Game.Elements` with `States`/`Values`, and an optional `Clock` section). It is public on purpose: the game file is game design, not sensitive data, and is pulled by other Gameday systems. Runtime state is stripped so a re-import carries the design, not live operations: phase `Active`/`Frozen`/`HideScores` are zeroed, and the clock's runtime `NextMatch`/`NextMatchPhase` pointer is never exported (the clock section is omitted entirely when no clock is configured). `POST /ui/mod/game/cloud/contests/{id}/scores` stores a site's schedule and scorecard data for one contest (see Cloud Game Push below); it is guarded by `GAME:SYNCHRONIZE` (satisfied by `GAME:ADMIN` via module-admin escalation) since it writes to the cloud's stored data. It refuses a frozen contest with `409 Conflict` and a JSON `"contest <id> is frozen and no longer accepts uploads"` body (see Frozen contest uploads below).

### Cloud Game Import (onsite)

`GET/POST /ui/mod/game/import-cloud` (both `GAME:ADMIN`-guarded; a "Cloud Import" child sits in the Game nav under "Setup") fetches the YAML via `Client.GetGameFile` (the GET pre-fetches so cloud failures surface on the page) and applies it through `applyGameConfig` — the same upsert the local setup file upload uses (positions, phases + advancement filters, elements + states, and the clock when the file carries a `Clock` section). `ScorecardElement` rows are not in the file and are regenerated via `exprForElementState`. Success → 303 to `?imported=1`; a cloud or parse/apply failure renders the import page (or `errors/internal.p2` for parse/apply) with an error and writes nothing.

Note: `applyGameConfig` inserts each phase **before** its advancement filters. Postgres enforces the `game_phase_id` foreign key on `game_phase_advancement_filters`, so inserting a filter before its phase fails (SQLite does not enforce FKs, which is why the original ordering bug only surfaced on Postgres). Filter IDs are assigned sequentially across phases in YAML order, so re-imports stay idempotent.

Note: `Active`/`Frozen`/`HideScores` are runtime operations state owned by the schedule page, not design state. Because the cloud export zeroes them (and any re-upload would otherwise clobber them), `applyGameConfig` re-reads each phase whose ID already exists in the database and inherits its stored flags; design fields from the file are still applied. A phase that is new to the database keeps the file's flags, so a game file can still introduce an active phase.

### Cloud Game Push (onsite)

`GET/POST /ui/mod/game/push-cloud` (both `GAME:ADMIN`-guarded; a "Cloud Push" child sits in the Game nav) pushes the selected contest's schedule and scorecard data to the cloud instance. The push is a single `POST /ui/mod/game/cloud/contests/{id}/scores` (via `Client.PushContestData`) with a bare `[]cloud.ContestPlacement` array in the body; the contest ID in the URL is authoritative. The cloud endpoint is guarded by `GAME:SYNCHRONIZE` (see Game Cloud Endpoints above), so the cloud instance must have a profile with that grant (or `GAME:ADMIN`) to accept pushes. The cloud endpoint stores the data via delete-then-create in a transaction keyed by the URL contest ID, so a re-push replaces the stored data in place. The onsite push guards on `selectedContestID != 0` (no contest selected → error, nothing sent). Success → 303 to `?pushed=1`; a cloud failure renders the push page with an error notification and is logged via `slog.Error` (with the contest ID). The cloud endpoint writes a JSON `{"error": ...}` body on each failure path (bad contest ID, decode error, transaction error, each also logged via `slog.Error`), so the message round-trips through `cloud.Error.Message` to the onsite push page. The cloud side is store-only this phase (no read-back endpoint).

**Frozen contest uploads** (`contestFrozen`, `push.go`): the upload endpoint asks the contest module's table whether the contest is frozen before storing anything, through the shared minimal local struct `contestRow` (`TableName() = "contests"`, carrying `ID`, `Name`, `Frozen` — the same no-import table-read pattern as `onsiteContestRow`, used for both names and the frozen flag). A frozen contest is refused with `409 Conflict` and a JSON error body, which the onsite push page and the background push already surface through `cloud.Error.Message`. The check is deliberately permissive in its failure modes: a contest with no row, or a cloud whose `contests` table does not exist yet, counts as not frozen, so the refusal only ever blocks an explicit operator freeze and never silently drops pushes.

**Scorecard submit errors** (`uiViewScorecardSubmit`, `scorecard.go`): every failure inside the submit transaction (non-numeric form value, store errors, an expression that fails or yields a non-integer score) is returned as a wrapped error. The transaction rolls back, so the previous score and values survive. The handler then renders `errors/internal.p2` with the error and does **not** redirect, and no cloud push is scheduled. Only a committed submit redirects back to the scorecard list.

**Onsite scorecard list** (`uiViewScorecardList`, `scorecard.go`, `views/game/scorecard_list.p2`): columns are Phase, Match, Field, Position, Team, State, Score, and a View button, matching the cloud lookup page's columns. The finalized score comes from `MatchScore`, which points at its placement (`MatchScore.MatchPlacementID`) rather than the other way round, so it cannot be `Preload`ed: the handler collects the page's placement IDs, loads those scores in one `match_placement_id IN ?` query, and hands the template `[]scorecardListRow{Placement, Score, Scored}`. **The dash is keyed on match state, not on score-row presence**: `Scored` is `MatchState.Scored()`, true exactly for the terminal states `Complete`, `NoShow` and `Disqualified` (the same terminal set `buildScheduleRows` uses for `Completed`). A submit writes its `MatchScore` and flips the placement to `Complete` in one transaction, so a terminal state always carries a definitive score, while `Scheduled`/`Queued`/`Running`/`Scorable` are still in flight and render `&mdash;`. The cloud lookup page (`cloud_scorecards.go`, `views/game/cloud_scorecards.p2`) applies the identical rule to `CloudMatchPlacement.State`, which is why no wire change was needed: a pushed `Score` of `0` cannot distinguish unscored from a real zero, but `state` is already carried in the push and refreshed on re-push (`push.go` `DoUpdates`), so both pages agree with no version skew between onsite and cloud builds.

**Position row banding** (`bandScorecardRows`, `scorecard.go`): a `FieldPosition` carries two colors (`Color1` light, `Color2` dark) so that two rows **of the same position** stacked one above another can still be told apart. The scorecard list therefore paints the Position cell with `scorecardListRow.BandColor`, which is `Color1` by default and `Color2` only for a row whose immediately preceding row has the same `PositionID` and is itself still on `Color1` (so three or more stacked rows of one position alternate). Alternating on row index instead shades every other row of a single-position listing, which reads as two interleaved positions. Because the list is `Order("phase_id, match, field_id, position_id")`, adjacent rows normally differ in position and the whole table sits on the primary colors; the alternate color only appears once the operator filters down to one position (or hides the other position's rows). The band color must be computed in Go for this reason, since pongo2 cannot see the previous iteration. The template keeps the `color: black;` convention position-colored cells use for dark-mode readability.

**Automatic push on scorecard submit** (`scheduleCloudPush`, `push.go`): after a scorecard submit's transaction succeeds, `uiViewScorecardSubmit` requests a best-effort push of the same data. The push runs in a background goroutine, so the referee's redirect never waits on the cloud.
- A server with no contest selected is skipped (debug log).
- Failures are logged at `Warn` and never surface to the referee; the manual Cloud Push page remains the way to retry.
- Each push is bounded by `autoPushTimeout` (30 s).
- Pushes are serialized and coalesced through `Module.autoPush` (`autoPushState`): at most one runs at a time, and any submits that arrive meanwhile collapse into exactly one follow-up push that re-exports the latest state. Serializing matters because each push replaces the contest's full data on the cloud, so an older export must never land after a newer one.
- Test modules built by `newTestModuleWithDB` default `GAMEDAY_CLOUD` to an unreachable local port (so tests never dial the real cloud) and wait on `autoPush.wg` during cleanup.

The wire types live in `pkg/cloud/scores.go` (`ContestPlacement` with `FieldName` (`field_name,omitempty`) carrying the site's field name, since fields are created per site and the cloud cannot name them from the ID; it is stored on `CloudMatchPlacement.FieldName` and refreshed on re-push via the upsert's `DoUpdates`; `State` as a plain `uint`, since the wire type cannot reference the game module's `MatchState` enum; `ContestScorecardValue`). The onsite `PushContestData` method converts `[]game.ContestPlacement` (from `ExportContestData`) to `[]cloud.ContestPlacement` and calls the client. The cloud storage models (`CloudMatchPlacement`, `CloudScorecardValue`) live in the game module and are migrated in both scopes (empty on onsite, harmless).

### Cloud Schedules (cloud)

`GET /ui/mod/game/schedules` (cloud scope only; a public "Schedules" child sits in the Game nav after "Scorecards") lists the pushed schedules, one row per (contest, phase) of `CloudMatchPlacement`.
- **Query:** `contest_id, phase_id, COUNT(DISTINCT match), COUNT(DISTINCT team_id)` grouped by contest and phase, with an optional `contest_id` facet (`cloudContestOptions`).
- **Paging:** in Go after grouping (the groups are few) with `web.PageParams`, `pageWindow` and `filtersQS`, like the scorecards page.
- **Columns:** Contest, Phase, Matches, Teams, and a View button.
- **Template:** `views/game/cloud_schedules.p2`.

`GET /ui/mod/game/schedules/{contest}/{phase}` (public) renders one pushed schedule with the **onsite** template `views/game/schedule.p2`, so cloud and onsite tables are identical.
- **Fields:** the pushed `FieldName` (`cloudFieldName`, `Field <id>` fallback).
- **Positions:** the cloud's own `FieldPosition` rows, whose IDs match the sites' because both come from the shared game file. A pushed position the cloud lacks gets a `Position <id>` column (colour `cccccc`) so its teams are not dropped.
- **Phase name:** `cloudPhaseNames`.
- **Rows:** built by the shared `buildScheduleRows`. There is no completed-match filtering (the cloud has no active phase).
- **Contest name:** passed as `contestName` and shown as a subtitle. The template renders it only when set, so onsite pages are unchanged.
- **No placements:** the internal error page.

**Shared schedule rows** (`schedule.go`): `scheduleRow` (`Round` one-based, `Match`, `Placements` keyed `"<fieldID>-<positionID>"`, `Completed`) and `buildScheduleRows([]scheduleSlot)` are package-level. Both the onsite `uiViewPhaseSchedule` and the cloud detail page map their placements to `scheduleSlot`s. `buildScheduleRows` sorts by match/field/position itself (the onsite query has no `ORDER BY`), so rows never depend on insertion order.

### Cloud Scorecards Lookup (cloud)

`GET /ui/mod/game/scorecards` (cloud scope only; a "Scorecards" child sits in the Game nav) is a read-only lookup of the pushed scorecards (`CloudMatchPlacement` + `CloudScorecardValue`), keyed by team. The page is **unguarded** and public: its route and its "Scorecards" nav child carry no permission, so logged-out visitors can use it (the cloud landing page points them to it) and uses the same filtering mechanism as the team list page: a single `cond`/`args` pair shared by the `Count` and paged `Find` on the `gorm.G[CloudMatchPlacement]` chain, `web.PageParams` paging, a radius-2 `pageWindow`, and `filtersQS` (query minus `page`) preserved in the pager links.

- **Filters** (query params, all optional, combined with AND): `q` searches `team_name LIKE %q% OR CAST(team_id AS TEXT) LIKE %q%`; `contest_id` is an exact facet; `state` is an exact facet on the stored `uint` state.
- **Facet options**: the contest dropdown lists distinct `contest_id` values from the placements, with names read best-effort from the `contests` table via a local `contestNameRow` struct (`TableName() == "contests"`, so the game module never imports `modules/contest`); a missing table or row falls back to `Contest <id>`. The state dropdown lists the package-level `MatchStates` slice.
- **Columns**: Team, Contest (best-effort name from the `contests` table, `Contest <id>` fallback), Phase (best-effort name from the cloud's own `GamePhase` rows by the stored site phase ID, loaded once per page, `Phase <id>` fallback), Round (stored zero-indexed, displayed with `|add:1` in the template, matching the onsite schedule page's one-based display), Match, State (pre-computed `MatchState(p.State).String()`, since pongo2 cannot do the conversion), Score, and a View button linking to the detail page. The per-element scorecard values are deliberately not shown here; they live on the detail page.
- **Detail page**: `GET /ui/mod/game/scorecards/{id}` (also unguarded) renders one pushed scorecard read-only using the existing onsite scorecard template (`views/game/scorecard.p2` → `partials/scorecard.p2`) with `edit_disable: true` (inputs disabled, no Submit button). The cloud stores only the site's own IDs, so the `MatchInfo` the template expects is synthesized from the `CloudMatchPlacement`. The field name is the pushed `FieldName` (`cloudFieldName`, `Field <id>` fallback; the cloud's own `Field` rows are never consulted, because fields are created per site and the IDs don't correspond). Phase and position names use best-effort lookups against the cloud's own game design (`GamePhase`/`FieldPosition` by the stored ID); a miss falls back to `Phase <id>` or `Position <id>` (default `Color2` `cccccc`). The element design is the cloud's `GameElement` (preloaded `States`/`States.Values`) and the values are the placement's `CloudScorecardValue` rows keyed `EID_SID` into `ScorecardData`; a pushed element key not in the current design simply renders no widget. A missing placement renders `errors/internal.p2` (HTTP 200, like every `DoTemplate` error path).
- **Chain gotcha**: `gorm.G[T]().Where()` returns `ChainInterface[T]`, not `Interface[T]`, so the shared chain is seeded with a no-op `Scopes(func(*gorm.Statement) {})` to obtain a `ChainInterface[T]` base (`CloudMatchPlacement` has no relations, so `Preload` is not a safe substitute).
- **Template**: `views/game/cloud_scorecards.p2` mirrors `team/list.p2` (GET filter form with no `page` field, total line, empty state, inline Bulma pager).

## Dependencies

| Package | Purpose |
|---------|---------|
| `github.com/go-chi/chi/v5` | HTTP router |
| `github.com/spf13/cobra` | CLI framework |
| `github.com/flosch/pongo2/v6` | Templating engine |
| `gorm.io/gorm` + `gorm.io/driver/sqlite` + `gorm.io/driver/postgres` | ORM + SQLite/PostgreSQL drivers |
| `github.com/the-maldridge/authware` | Authentication framework |
| `github.com/coder/websocket` | Websocket server (event bus export) |
| `github.com/gopxl/beep/v2` | Sound effect decoding and playback (pkg/sfx; audio device via `oto`, no CGO needed; pkg/announcer drives it) |
| `github.com/jedib0t/go-pretty/v6` | Table rendering (CLI) |
| `golang.org/x/crypto/bcrypt` | Password hashing |
| `github.com/gogpu/systray` | System tray applet (`onsite tray`, pure Go / zero CGO) |
| Bulma CSS 1.0.4 (CDN) | Frontend UI framework |

## Build & Deploy

### Dockerfile

- **Stages**:
  1. `busybox` downloads Bulma 1.0.4 (`bulma.min.css`, git-ignored) into `pkg/web/ui/static/css/`.
  2. `golang:1.25-alpine` builds the binary.
  3. `scratch` holds the final image.
- **CGO**: Enabled for SQLite (`CGO_ENABLED=1`; `gorm.io/driver/sqlite` wraps the cgo `mattn/go-sqlite3`)
- **Static linking**: `-extldflags '-static'` (required for the `scratch` image)
- **Build info**: build args `VERSION` (default `dev`), `COMMIT` (default `UNKNOWN`) and `BUILD_DATE` (default `UNKNOWN`) are passed to `pkg/buildinfo` via `-X` ldflags. A plain `docker build .` therefore reports `dev`.
- **Entrypoint**: `tini-static` init system
- **Data dir**: `ENV GAMEDAY_DB=/data/gameday.db`
- **Volume**: Expected mount at `/data`

### Releases (`.github/workflows/release.yml`, `.goreleaser.yaml`)

Pushing a `v*` tag runs two jobs:
- **`goreleaser`** (`release --clean`) builds linux/windows amd64/arm64 archives with the buildinfo ldflags (`{{.Version}}`, `{{.Commit}}`, `{{.Date}}`).
- **`docker`** builds and pushes `ghcr.io/gizmo-platform/gameday:<tag>`. It passes the build args so the image reports the same values as the goreleaser binaries:
  - `VERSION` is the tag without its leading `v`;
  - `COMMIT` is `github.sha` (full SHA);
  - `BUILD_DATE` is RFC 3339 UTC.

Snapshot builds (`goreleaser build --snapshot`) use `snapshot.version_template: "{{ .ShortCommit }}"`, so their version is the short commit hash.

### Build Flags

```bash
ldflags="-X github.com/gizmo-platform/gameday/pkg/buildinfo.Version=1.0.0 \
         -X github.com/gizmo-platform/gameday/pkg/buildinfo.Commit=abc1234 \
         -X github.com/gizmo-platform/gameday/pkg/buildinfo.BuildDate=2026-01-01"
```

## Environment Variables

| Variable | Default | Purpose |
|----------|---------|---------|
| `GAMEDAY_DB` | `gameday.db` | SQLite database path (fallback default for all entrypoints) |
| `GAMEDAY_DB_DRIVER` | `sqlite` | Database driver: `sqlite` or `postgres` (see Generic DB Helpers) |
| `GAMEDAY_DB_DSN` | — | PostgreSQL DSN, used when `GAMEDAY_DB_DRIVER=postgres` (e.g. `postgres://user:pass@host:5432/db?sslmode=...`) |
| `GAMEDAY_ONSITE_DB` | — | SQLite path for `onsite serve` (overrides `GAMEDAY_DB` when set) |
| `GAMEDAY_CLOUD_DB` | `gameday_cloud.db` | SQLite path for `cloud serve` (local DB file; distinct from `GAMEDAY_CLOUD`, which is the base URL the `pkg/cloud` client dials) |
| `GAMEDAY_CLOUD` | `https://gameday.gizmoplatform.org` | Base URL of the cloud instance for the `pkg/cloud` client (see Cloud Client) |
| `GAMEDAY_CLOUD_TOKEN` | — | Bearer token attached to every `pkg/cloud` client request when set; an explicit `WithAuthenticator`/`WithBasicAuth`/`WithBearerToken` option takes precedence |
| `GAMEDAY_SFX` | — | Directory of sound effect files replacing the embedded `pkg/sfx` library (read once by `sfx.New`; a non-directory falls back to the embedded sounds) |
| `GAMEDAY_SERVER` | `http://localhost:$GAMEDAY_PORT` | Base URL of the gameday server whose event stream `onsite sfx` and `onsite tray` announce (e.g. `http://192.168.1.10:8080`); see Announcer |
| `GAMEDAY_ADVERTISE_ADDR` | — | Onsite landing page: the address other computers should use to reach this server (`ip`, `ip:port`, or `http://host:port/`; the listen port is appended when absent). Overrides interface enumeration; needed behind Docker/NAT |
| `GAMEDAY_CLOUD_WELCOME_MSG` | — | Cloud landing page welcome message (plain text, HTML-escaped, newlines kept, read per request) |
| `GAMEDAY_PORT` | `8080` | Port for `onsite serve` / `cloud serve`; also the port `onsite sfx` / `onsite tray` announce by default when `GAMEDAY_SERVER` is unset |
| `GAMEDAY_MODULES` | — | Comma-separated whitelist of optional modules to load in addition to required ones |
| `LOG_LEVEL` | — | `slog` level (DEBUG, INFO, WARN, ERROR) |
| `GAMEDAY_ADMIN_INIT_PW` | `"GameOn!"` | Default admin password on first boot |
| `AUTHWARE_BASIC_MECHS` | `gameday` | Authware backend selection (set automatically if unset) |
| `AUTHWARE_TOKEN_MECHS` | `gameday` | Authware token (Bearer) backend selection (set automatically if unset) |

## Testing

- **Location**: `pkg/cloud/client_test.go`, `pkg/db/db_test.go`, `pkg/schedgen/bid_test.go`, `pkg/schedgen/division_test.go`, `pkg/schedgen/anneal_test.go`, `pkg/schedgen/baked_test.go`, `internal/cmd/bake_schedules_test.go`, `pkg/event/bus_test.go`, `pkg/event/handler_test.go`, `pkg/announcer/announcer_test.go`, `pkg/sfx/sfx_test.go`, `internal/cmd/onsite_sfx_test.go`, `internal/cmd/onsite_tray_test.go`, `pkg/web/error_templates_test.go`, `pkg/web/tmpl_debug_test.go`, `pkg/web/eventbus_test.go`, `pkg/web/users_test.go`, `pkg/web/tokens_test.go`, `pkg/web/admin_test.go`, `pkg/web/landing_test.go`, `modules/game/lint_test.go`, `modules/game/advancement_test.go`, `modules/game/clock_test.go`, `modules/game/clock_match_test.go`, `modules/game/cloud_test.go`, `modules/game/cloud_scorecards_test.go`, `modules/game/contest_key_test.go`, `modules/game/push_test.go`, `modules/game/scorecard_test.go`, `modules/game/cloud_schedules_test.go`, `modules/game/autopush_test.go`, `modules/game/division_aware_test.go`, `modules/game/phase_rankings_test.go`, `modules/game/byte2bite_test.go`, `modules/game/schedule_gate_test.go`, `modules/game/cleardata_test.go`, `modules/team/team_test.go`, `modules/team/region_test.go`, `modules/contest/contest_test.go`, `modules/contest/onsite_test.go`, `modules/contest/cleardata_test.go`, `modules/best/scores_test.go`, `modules/best/awards_test.go`, `modules/best/performance_test.go`, `modules/best/best_award_test.go`, `modules/best/game_award_test.go`, `modules/best/advancement_test.go`, `modules/best/final_test.go`, `modules/best/semifinal_test.go`, `modules/best/tiebreaker_test.go`
- **Pattern**: Table-driven / config-driven tests using `schedgen.Config`; in-DB tests build a module via `newTestModule(t, opts...)` (a fresh temp-dir SQLite DB + a `web.Server`, optionally with `web.EventBus`) and exercise handlers or bus events directly
- **Coverage**: BIBD smoke test (v=7, k=3 Steiner Triple System), larger team test (13 teams), degenerate case (teams < block size), division interleaving, mobile navbar (burger as the last brand child targeting the `navbar-menu` that wraps start/end, toggle script loaded), footer (present on base pages in every scope with the credit line and build version, `noprint`, docked-footer body class), landing page (interface filtering keeps real IPv4 interfaces and drops loopback/link-local/IPv6/down/docker/bridge/veth/virbr, `GAMEDAY_ADVERTISE_ADDR` override with port appending, onsite logged-out login notice + URLs, logged-in user line, no-address hint, cloud welcome message escaped exactly once with line breaks kept, scorecards hint only when logged out, blank message fallback, no box without a scope), profile permissions page (`groupPermissions` module/grant ordering, one title-cased fieldset per module, title-cased grant checkboxes checked only for held grants, checkbox submit replaces the profile's permissions), error template rendering (incl. `DoTemplate` rendering `error` values as their HTML-escaped message for plain, wrapped, and pointer-receiver errors, a `<script>` payload escaped, typed-nil errors rendering empty, an empty message rendering "unknown error", and adapted errors staying true in `{% if %}`), debug template loading, advancement filters (roster/scoreboard sourcing, exclude-then-include, tie-breaking, `SliceExpr` `len(Roster)` sees the full roster after an exclude, a `Roster` include does not re-advance blocked teams), byte2bite phase flow (6 teams: Wildcard suppressed and Semifinals schedulable straight from Seeding with all 6; 10 teams: Wildcard takes the 3 non-top-7 teams and Semifinals take 8; 40 teams: Wildcard takes 8 and Semifinals take 16; uses a stub `BESTNotebook` filter), schedule readiness gate (blocking `CanRun` verdict lands the rule-named reason in `AdvancementReadiness.Reasons`, a `CanRun` error degrades to not-ready without failing the call, multiple blockers collect in filter order, an unready source phase blocks once without the filter's own `CanRun` being asked, a `When: false` filter never invokes `CanRun`, filters without the readiness interface are always ready, non-contiguous source phase IDs resolve the correct source (regression), `phaseSequence` dependency order with ID tie-breaks and cycle fallback, select-teams/preview/accept all render the blocked page and create nothing while blocked, `?override=1` unlocks the non-inert manual picker and the accept carries the override, phase list renders the disabled control with title + help line for a blocked next-up phase and no control for phases behind it, BEST `NotebookAdvancement.CanRun` verdicts for no scores / tie-breaker-only scores / one notebook score / empty scope, `notebookScope` scoreboard-over-pool scoping), phase score stats (min/max/avg with the MatchPlacement reported for the extremes, empty-phase zero result, lowest-ID tie-break; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set), phase score stats page (admin render with min/max team names, scores, and source Match/Field cells, GAME:ADMIN guard blocks non-admins, back links resolve), phase rankings page (frozen phase renders every team unpaginated in score order, division-aware phase renders one table per division with ranks restarting at 1, anonymous users get 401 with no permission named, unfrozen phase refused, phase list shows the Rankings button after Stats only for frozen phases and logged-in users), default field seeding (Migrate guarantees a `Field 1` row and is idempotent on re-run), match clock (YAML parse, settings round-trip, start/cancel, auto-stop with hurry/end events, guard against double-start/cancel-when-idle, page rendering), clock-driven match transitions (target selection, start/end/auto-stop/cancel state flow, operator next-match override, flag-off no-op, Scorable-left-alone, stale Running/Queued reset, start rejection paths, transitions panel round-trip), admin team list (pagination, division/region facets, combined filters, filtered pagination, filter state preserved in pager links, no-match empty state), db driver selection (postgres-without-DSN error, unknown-driver error, env-gated Postgres integration test on `GAMEDAY_TEST_PG_DSN`: migrate + upsert round-trip), user deletion (deletes the user's API tokens, then the user, clears the profile's `profile_permissions` join rows, then deletes the profile; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set, where the old profile-first ordering fails with an FK violation), API tokens (token authenticator resolves a valid token to the owning user's login and rejects unknown tokens, user delete cascades to the user's tokens, token create stores only the hash and renders the one-time plaintext, end-to-end Bearer auth through the full router: a valid token for the bootstrap admin renders the CORE:ADMIN-guarded user list, a bogus token gets 401, and no credentials get the unauthorized page), game cloud import Postgres FK (multi-phase `byte2bite.yml` fixture with advancement filters imports cleanly on Postgres, where the `game_phase_id` FK is enforced; runs on SQLite by default and on Postgres when `GAMEDAY_TEST_PG_DSN` is set), cloud client (JSON decode, query encoding, error vs network-failure distinction, env/default base URL, basic-auth header, bearer-token header, env-var bearer token (GAMEDAY_CLOUD_TOKEN) with explicit-option precedence, regions/teams round-trips, raw-bytes game file round-trip, JSON error body parsing (the `error` field is extracted into `Error.Message`, non-JSON bodies fall back to the trimmed raw body)), team cloud import (public regions/teams JSON endpoints incl. unknown-region 404, end-to-end region import with division defaulting, idempotent re-import that updates in place without duplicating and preserves `disabled`, cloud-failure path that writes nothing), game cloud import (end-to-end import via shared `applyGameConfig` incl. advancement filter, regenerated scorecard exprs, and clock section, idempotent re-import, cloud-failure path that writes nothing, public YAML export with runtime state stripped and round-trip parse, cloud-scoped configuration/setup screens incl. setup upload round-trip and onsite-only routes staying unregistered), setup runtime-flag preservation (re-upload keeps a stored phase's Active/Frozen/HideScores while applying design edits, a brand-new phase keeps the file's flags), contest (cloud contest list + per-contest roster JSON endpoints, My Contest selection storing the cloud ID and re-select replacing the row, CONTEST:ADMIN guard, selection auto-import: roster imported when the local team list is empty and skipped when teams exist, cloud game imported when no `GamePhase` rows exist and skipped when a game is loaded, all against a fake cloud server), contest frozen-ness (freeze/unfreeze round-trip with the list page showing the state and offering the opposite action, CONTEST:ADMIN guard on both transitions leaving the flag untouched, a frozen contest refusing edit/delete and every roster mutation with the reason named and nothing written while staying readable, unfreezing restoring mutations, the public contest list carrying `frozen`, My Contest annotating a cloud-frozen contest in the option list and warning about the selected contest), cloud upload frozen-ness (frozen contest refused with 409 plus a JSON reason while another contest still accepts, unfrozen contest accepted, a cloud with no contests table still accepting), clearing contest data — game side (`ContestDataCounts` grouping per contest with labelled placement/value counts and an absent contest absent from the map, counts and clear both empty-without-error when the cloud tables do not exist, `ClearContestData` removing exactly the contest's placements and values and returning their count, another contest's rows and no orphaned `cloud_scorecard_values` surviving, contest 0 refused with `errNoContest`, a clearer failing inside a transaction rolling the delete back) — and contest side (cross-module clear through real `game` deps erasing both tables for the target contest only while the contest record survives and the redirect carries the row total and ID, a name mismatch of any kind (wrong, wrong case, missing, partial) redirecting with `clear_error=name` and deleting nothing, a name with spaces and punctuation clearing when typed correctly, a frozen contest refused with the reason named and rows unchanged, an ungranted request denied with nothing removed, an unknown contest ID landing on the error page, a `nil`-deps module clearing zero rows successfully, a second clear reporting `cleared=0`, the list page rendering the itemized modal and an enabled button only for the contest with stored rows and `is-disabled` otherwise, and the success notification naming the contest with an unknown `cleared_for` ID still rendering), contest keying (schedule preview candidate placements carry the selected contest ID, zero when no contest is selected, scorecard submit mirrors the placement's ContestID onto the MatchScore, ExportContestData returns portable placements with team name, score, and scorecard values for a contest and an empty slice for an unknown contest ID), cloud game push (end-to-end push with fake cloud server verifying path, payload fields, and success redirect, cloud endpoint stores placements + scorecard values keyed by contest ID, idempotent re-push replaces in place, GAME:SYNCHRONIZE guard rejects ungranted and unauthenticated profiles without writing (JSON `{"error": ...}` denial body for `Accept: application/json` requests, HTML page otherwise), no-contest guard shows warning/danger and sends nothing, cloud-failure path shows danger with the cloud's error message, unauthenticated push against a real cloud instance (no token) surfaces the guard's 401 as a clean danger error (the parsed JSON message, not the HTML denial page) instead of a false success, cloud endpoint writes a JSON error body on bad contest ID and malformed body without storing, DoJSON round-trip with Content-Type, nil-body sends no Content-Type, PushContestData client round-trip), automatic cloud push on scorecard submit (submit pushes the contest's data with the new score and values, no contest selected → no push, a failing cloud still saves the score and redirects, overlapping submits during an in-flight push coalesce into a single follow-up push carrying the latest score; race-detector clean), scorecard submit errors (a non-numeric value and a non-integer expression result each render the error page without redirecting, roll back to the previous score and values, and push nothing), cloud schedules (list with one row per contest/phase and match/team counts, anonymous access, contest facet with names, empty state, pagination keeping the contest filter, detail page rendering the onsite schedule template with pushed field names, cloud position names and colours, a `Position <id>` placeholder column, one-based rounds and matches in order, missing schedule error page, routes absent onsite), shared `buildScheduleRows` (match ordering regardless of input order, one-based rounds, placement keys, Completed only when all slots are terminal) and the onsite schedule page rendering out-of-order placements in order, field name push (payload carries `field_name`, stored and updated in place on re-push, scorecard detail shows the pushed name and falls back to `Field <id>` rather than the cloud's own field), cloud scorecards lookup (empty state, list with team/contest/placement columns and a View button, phase column with name from the cloud game design and `Phase <id>` fallback, contest facet name from the contests table with fallback, team name/number search, contest and state facets, combined filters, pagination with pager links, filter state preserved in pager links, unguarded page accessible to a bare profile, detail page rendering the existing scorecard template read-only with the pushed values and disabled inputs, detail page for a missing placement rendering the internal error page), game division awareness (`DivisionAware()` false with no phases and with only non-division-aware phases, true once a `DivisionAware: true` phase exists), phase roles (built-in roles valid, empty role valid, `RegisterPhaseRole` panics on empty/duplicate, `PhaseRoles()` sorted without the empty role, lint rejects an unknown `Role` and accepts built-ins plus BEST's `Wildcard`), `PhaseRankings` by role (role filter with identity fields populated, `frozenOnly` skipping unfrozen phases, same-role phases in ID order, empty role matching only unassigned phases, unknown role yielding an empty slice, division aware phase yielding one entry per division with teams and none for empty divisions), BEST scores page low-category warning (`lowScoreCategories` verdicts: no teams, unscored categories silent, a whole category under 33% flagged once with its entered count and threshold, a highest score of exactly 33% clearing it, partially scored categories judged on entered scores only, warning rendered naming the low category but not a healthy one), BEST awards (per-division ranking when a division-aware phase exists, single "Overall" ranking when none does, dense ranking with co-winners sharing a rank, canonical tie-break chain notebook → marketing → poster → video with team number as ordering-only last resort, unassigned-division group, podium/complete split, awards page render and BEST:ADMIN guard), BEST Game Award (ranks 1 through 4 from the frozen `Final`-role phase and shows all four places inline with plain numeric ranks like the other awards, `More` false once every awarded place is inline and true when a team falls outside them, no groups when no phase carries the role or when the final is unfrozen, one group per division with ranks restarting at 1 for a division aware final, Game Award rendering ahead of the category awards, no special place titles rendered), BEST Robot Performance (linear rank→points mapping 250→5 with whole-point rounding and edge cases for one- and two-team fields and out-of-range ranks, flat seeding phase ranked as one field when the award is overall (a two-division field earns 250→25 across every team, ties across divisions preserved) and regrouped per division when the award is per division so a small division's top team still earns 250, end to end through `computeAwards` with and without a division aware phase, ties in the phase preserved as identical points, division aware phase unchanged by the regroup, undivided teams bucketed together, derivation state `noGameModule`/`noPhase`/`notFrozen`/`ready` with the phase name surfaced, error propagation, end-to-end derivation from a frozen `Seeding`-role phase), BEST Award composite (all-five-components eligibility gate counting an absent value rather than 0, exact 1000-point total, disabled teams neither ranked nor diagnosed, diagnosis notes for every unavailable Robot Performance state plus the per-division split note, award registry order best→game→notebook→marketing→poster→video, `Diagnosed` flag set only for the BEST Award with five component column headers like `Notebook /300` and `Robot Performance /250`, end-to-end ranking from a frozen seeding phase, awards page eligibility panel and incomplete-eligibility notice, scores page Total footnote), scorecard list filters (saved-filter cookie applies its phase/field/position/state scopes, unmarshalling the cookie never corrupts the package-level `MatchStates` list the filter form renders, all seven state checkboxes survive a saved filter, no-cookie requests fall back to all states, clear action expires the cookie and restores the full view), scorecard list score column (Score column header present, a `Complete` placement renders its score in its own row, a `Scorable` placement renders the `&mdash;` placeholder rather than a bare 0, proving the dash is keyed on state), cloud scorecards list score column (a pushed `Complete` placement renders its score, a pushed `Scheduled` placement with a zero score renders `&mdash;` and never `<td>0</td>`), scorecard position row banding (`bandScorecardRows` unit cases: two interleaved positions never band, a single position alternates primary/alternate/primary, a new position group restarts on the primary color, empty list is safe, and end-to-end that a both-positions listing renders only the primary colors while a saved single-position filter bands its stacked rows)
, sfx (embedded library lists and plays every shipped sound, `GAMEDAY_SFX` directory replaces the library with unusable values falling back to embedded, `WithDir`/`WithFS` precedence over the environment, playback resamples a foreign sample rate to the output rate, a second `Play` replaces the first streamer, unknown names are `ErrNotFound` without opening the device, names with extensions resolve, decoded samples are cached and re-read nothing, a broken file is reported and a repaired file becomes playable, an unusable audio device is opened once and remembered process-wide, a device that fails to answer in time yields a retryable timeout and succeeds once it answers, sink close is idempotent and never closes an unopened device, player `Stop`/`Close` semantics, concurrent `Play` is race-clean, `Play` returns immediately while `PlayAndWait` is held until playback is silenced and reports `ErrNotFound` without opening the device, `Duration` reports a sound's length and every shipped sound is a short positive length), announcer (event→sound mapping and `EventTypes`, unmapped and replayed envelopes make no sound, a failing player does not stop the run, bus mode plays all four clock sounds and ignores other types, closing the bus ends the run cleanly, remote mode plays clock sounds over a real websocket handler, a dropped connection keeps being retried, a server that ignores the `types` filter has its junk frames skipped, `eventsURL` base handling with preserved query params and rejected addresses, `Validate`, `Run` reporting an unusable server, `DefaultServer` and the `GAMEDAY_SERVER` fallback, reconnect delay growth and cap), `onsite sfx` (name normalization across the spellings an operator types, sound resolution and unknown names, `--list`, command argument shape, every announcer sound exists in the library), tray clock switch (starts on, toggle stops and restarts the listener, no-listener switch stays off, applet cancellation stops the listener, `stop` waits for it, unusable `GAMEDAY_SERVER` yields an indicator-only switch, `titleCase` labels are readable, one test entry per library sound and none for an empty library)
- **Run**: `go test ./...`

### Test Fixtures

`test_data/*.yml` — YAML schedule test fixtures for 2-player and 4-player configurations, with/without objectives and divisions, plus `byte2bite.yml` (a 4-phase production game setup file with advancement filters and a Wildcard suppression for events under 8 teams, used by the Postgres FK import test and the byte2bite phase-flow tests). `test_data/*.csv` — team import fixtures (12 and 24-team datasets).

## Conventions

- **Naming**: Go standard (`CamelCase` types, `lowerCamelCase` locals, `PascalCase` exported)
- **Errors**: Custom error types (`UnknownGenerator`, `InvariantViolation`) with `Error() string`
- **Logging**: `log/slog` throughout; no structured logging library
- **HTTP handlers**: `uiView<Name>`, `uiView<Name>Submit` pattern
- **Templates**: `.p2` extension (Pongo2), per-module `ui/p2/views/<module>/` layout, Bulma CSS classes for styling
- **Pagination**: Consistent `Paginate(r)` scope usage
