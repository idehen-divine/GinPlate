# GinPlate Backend Rules

Conventions for writing backend code in this repo. Check sibling files
first: if a pattern exists, follow it. `internal/modules/users` is the
reference resource module.

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
4. **service.go** — business rules over the interface. Returns classified
   errors (`web.NotFound/Conflict/Forbidden/Wrap`) so handlers render
   without thinking. `NewService(nil)` selects GORM; tests pass a stub.
5. **handler.go** — thin translation: bind → `ValidationErrors` →
   one service call → `web.Success`/`web.Render`. No SQL, no rule logic.
   Path UUIDs via `web.ParseUUID`. Authz beyond route middleware goes
   through service checks (author-or-admin), never inline.
6. **routes.go** — `RegisterRoutes(r, h, key, store)`: URL mounting +
   middleware (`RequireAuth`, `RequireRole(web.RoleAdmin)`). The full
   authz policy must be readable here.
7. **resource.go** — response shaping (`ToMap`, `Collection`): declared
   fields, computed values, conditionals on viewer claims. Shapes `data`
   only; the `{success, code, message, data, errors}` envelope is untouched.
8. **service_test.go** — single `TestXxx` with subtests against stubs.

## Non-negotiables

- **Typed roles only**: `web.RoleAdmin` / `web.RoleMember` — never `"admin"` literals.
- **Never hardcode table/column assumptions in handlers** — repository owns queries.
- **Errors carry status**: services return `*web.AppError` (or wrapped);
  handlers never invent status codes.
- **Jobs over inline work**: anything async `Push`es to `internal/jobs`.
  Handlers finishing past 60s risk double execution (DB lease) — keep them
  fast or idempotent.
- **Overlap needs a reason**: scheduled entries default to overlapping;
  add `WithoutOverlapping` / `OnOneServer` deliberately, with `ExpireAfter`
  bounding crash recovery.

## Wiring checklist (new module)

- [ ] `RegisterRoutes` called in `pkg/app/app.go` under `/api/v1`
- [ ] Migration via `make:migration` (both dialects, columns match model)
- [ ] Swagger annotations on handlers, then `make docs`
- [ ] `resource.go` if the model has non-public fields
- [ ] README layout line updated
