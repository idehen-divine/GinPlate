# GinPlate Backend Rules

Conventions for writing backend code in this repo. Check sibling files
first: if a pattern exists, follow it. `internal/modules/users` is the
reference resource module. Naming follows
[docs/naming-conventions.md](naming-conventions.md); the bindings below
restate what applies to backend layers.

## Module shapes

Pick by what the feature owns:

- **Resource** (owns a table — users, orders): `model → repository →
  service → handler → routes` plus `dto` and `resource`. See `users`.
- **Process** (owns a workflow — auth, checkout): `dto → service →
  handler → routes`, no model/repository. Operates on other modules' data.
- **Integration** (coordinates backends — notifications): service over DB
  + queue + mail, routes for the inbox side.

## The eight layers

1. **model.go** — table shape only: fields, constraints, `TableName()`,
   `BeforeCreate` UUIDs. No HTTP, no validation, no queries.
2. **dto.go** — request bodies with `binding:` tags. Update DTOs use
   pointer fields (nil = absent). Shape validation only; no authz.
3. **repository.go** — `Repository` interface + `GormRepository`. All
   persistence here: allow-listed sorts, search, scoping. Tests stub it.
   Field and cross-boundary parameters are named `repository`, never `repo`.
4. **service.go** — business rules over the interface. Returns classified
   errors (`web.NotFound/Conflict/Forbidden/Wrap`) so handlers render
   without thinking. `NewService(nil)` selects GORM; tests pass a stub.
   Handlers depend on the service through a behavior interface
   (`UserService`), holding it in a field named `service`, never `svc`.
   The concrete type stays `Service` inside its own package.
5. **handler.go** — thin translation: bind → `ValidationErrors` →
   one service call → `web.Success`/`web.Render`. No SQL, no rule logic.
   Path UUIDs via `web.ParseUUID`. Authz beyond route middleware goes
   through service checks (author-or-admin), never inline. Receivers stay
   short (`h`, `c`) in these small scopes; use descriptive names only where
   several contexts or handlers coexist. Name returned data by domain
   (`users`, `items`) rather than `res`.
6. **routes.go** — `RegisterRoutes(r, h, key, store)`: URL mounting +
   middleware (`middleware.RequireAuth`, `middleware.RequireRole(middleware.RoleAdmin)`).
   The full authz policy must be readable here. A route takes as many
   middlewares as it needs: `g.Use(a, b, c)` chains them in order.
7. **resource.go** — response shaping (`ToMap`, `Collection`): declared
   fields, computed values, conditionals on viewer claims. Shapes `data`
   only; the `{success, code, message, data, errors}` envelope is untouched.
8. **Tests** — one test file per module, named after it (`auth_test.go`,
   `user_test.go`): all test funcs with subtests against stubs live there
   together, never split across files. Test names state behavior
   (`TestRequireAuthRejectsRevokedSession`), and doubles are domain-specific
   (`stubUserRepository`, never bare `stub`). Use descriptive error names
   (`databaseError`, `sessionError`) when errors coexist; plain `err` in
   short scopes.

## Non-negotiables

- **Typed roles only**: `middleware.RoleAdmin` / `middleware.RoleMember` — never `"admin"` literals.
- **Descriptive long-lived names**: `config` (never `cfg`), `databaseConnection` / `sqlDatabase` / `redisClient` where GORM, `database/sql`, and Redis handles coexist, `result` (never `res`) for service output. Short forms survive only in narrow scopes (`ctx`, `db`, `h`, `c`, test locals). See [docs/naming-conventions.md](naming-conventions.md).
- **Never hardcode table/column assumptions in handlers** — repository owns queries.
- **Errors carry status**: services return `*web.AppError` (or wrapped);
  handlers never invent status codes. For new failure kinds, add a typed
  error + mapper in `internal/exceptions` (`ginplate make:exception Name
  --status 402`) instead of editing `web.Render`.
- **Jobs over inline work**: anything async `Push`es to `internal/jobs`.
  Handlers finishing past 60s risk double execution (DB lease) — keep them
  fast or idempotent.
- **Overlap needs a reason**: scheduled entries default to overlapping;
  add `WithoutOverlapping` / `OnOneServer` deliberately, with `ExpireAfter`
  bounding crash recovery.

## Middleware

Auth (`RequireAuth` + `RequireRole`) lives in `internal/middleware`
(`auth.go` + `role.go`) so app policy is editable without touching
`pkg/web`. A route takes as many middlewares as it needs:
`g.Use(a, b, c)` chains them in order — e.g. `users` chains auth → role.

- **Active accounts**: `RequireAuth` itself rejects deactivated accounts
  through the `middleware.ActiveCheck` hook, which the users module sets
  once in its `init()` (never reassigned at runtime) and which loads via
  memoized `users.CurrentUser`, so one query max per request. No per-route
  wiring needed. Without a DB handle (tests, `route:list`) the hook skips
  — production always mounts `ProvideDB` first, so it always enforces there.
  Bump `users.Service.BumpAuthVersion` whenever roles change: outstanding
  tokens die on version mismatch.
- **Current user**: never query it directly — use `users.CurrentUser(c)`.
  First call queries, later calls return the cached row (Laravel
  `Auth::user()` behaves the same way).
- **Global** (every request): built-ins stay hardcoded in `pkg/app/app.go`
  (logger → recovery → maintenance → CORS → security → gzip? →
  rate-limit? → customs → `ProvideDB`). Custom globals live in
  `internal/middleware` (e.g. `requestid.go`) and self-register via
  `init()` with `web.RegisterGlobalMiddleware(name, order, fn)` — default
  order `web.DefaultGlobalMiddlewareOrder` runs after all built-ins.
  Scaffold with `ginplate make:middleware RequestID --global`.
- **Per-route** (some routes): no registry — scaffold with
  `ginplate make:middleware AuditLog`, then add the constructor to the
  module's `g.Use(...)` line in `routes.go` alongside as many others as
  the route needs, plus one `web.RegisterRouteMeta(method, path,
  "auth+label")` line in the same `init()` so `route:list` shows it.
  Shape it like `RequireRole`: check → `c.Next()` or `Fail` + `c.Abort()`.
- Both must only capture deps at registration and touch them when serving
  (nil-safe for `route:list`). `internal/middleware` must never import
  `internal/modules/*` (would cycle) — model-owned helpers like
  `users.CurrentUser` live in their module instead.

## Wiring checklist (new module)

- [ ] `init()` self-registers via `web.RegisterModule` in `routes.go`, plus one blank-import line in `internal/modules/register.go` (`pkg/app/router.go` only links the root registry)
- [ ] Per-route middleware (if any) added to `g.Use(...)` + `RegisterRouteMeta` label (`auth[+role][+custom]`)
- [ ] Names follow [docs/naming-conventions.md](naming-conventions.md) (`service`, `repository`, behavior interfaces, domain doubles)
- [ ] Migration via `make:migration` (both dialects, columns match model)
- [ ] Swagger annotations on handlers, then `make docs`
- [ ] `resource.go` if the model has non-public fields
- [ ] README layout line updated
