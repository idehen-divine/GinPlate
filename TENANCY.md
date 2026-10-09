# Multi-tenancy (elastic)

One codebase serves many tenants. Tenants share pool databases by default
(row isolation via `tenant_id`); enterprise tenants move to dedicated
databases without code changes.

## Concepts

- **Control database** (`CONTROL_DSN`, defaults to the primary DSN): owns
  `tenants`, `tenant_migrations`, `control_admins`. Nothing tenant-facing
  reads it except resolution.
- **Shared pool** (`SHARED_POOL_DSNS`, `DEFAULT_POOL`): default home for
  tenant rows. Empty in development means the primary database.
- **Dedicated database** (`TENANT_DSN_TEMPLATE` with one `%s`): per-tenant
  databases after a move. `placement` on the tenant record routes traffic.
- **Tenant identity**: exactly one of `X-Tenant-Slug` (a `tenants.slug`)
  or `X-Tenant-Domain` (a verified `tenant_domains` row) — both present but
  disagreeing is a 400, neither present is a 400, unknown slugs and
  unknown-or-unverified domains share one 404. Slugs are lowercase
  `[a-z0-9-]`; domains are bare hostnames (no scheme/path/port), validated
  before they can reach DSN templates. There is no fallback tenant and
  nothing is seeded: every tenant is created explicitly (see Bootstrapping
  below) and addressed explicitly thereafter.
- **Row isolation**, three deep: GORM callbacks auto-fill `tenant_id` on
  create and add `WHERE tenant_id = ?` on read/update/delete (silent when
  no tenant is in context, so tenant-less code and tests are unaffected);
  Postgres RLS policies confine `APP_DB_USER`; JWT `tid` must match the
  resolved tenant or the request 401s — stolen cross-tenant tokens are dead.

## Request flow

`ResolveTenant` → `BlockWritesWhenMigrating` → `FinalizeTx` live in
`pkg/tenancy` (not `internal/middleware`, which hosts auth/role guards).
They are not yet mounted in the `pkg/app` router: `RegisterAPIRoutes`
mounts only modules registered in `internal/modules/register.go`
(`auth`, `notifications`, `users`), and the tenancy modules below expose
`RegisterRoutes` without self-registering, so no route currently resolves
a tenant. When mounted, they belong on the tenant `/api/v1` group before
auth. Health/readiness stay public; the resolver itself skips
`/api/v1/admin/*` by path prefix so the control plane resolves its own
handles. On Postgres each request runs in a transaction holding
`SET LOCAL app.tenant_id`; MySQL relies on GORM scoping alone.

## Bootstrapping (no seed data)

Nothing is seeded: a fresh database serves nothing until an admin creates
the first tenant. The full bootstrap:

```bash
ginplate migrate up                                          # schema
ginplate migrate seed-admin "Root" root@example.com <pw>    # super_admin
# login -> POST /api/v1/admin/auth/login, then:
# POST /api/v1/admin/tenants {"slug": "acme", "name": "Acme Inc"}
# use it -> X-Tenant-Slug: acme (or a verified X-Tenant-Domain)
```

## Migration layout

Two domains, both dialects, all embedded via `migrations.FS`:

- `migrations/app/{mysql,pgsql}` — shared/control database: tenant data
  plus `tenants`, `tenant_domains`, `tenant_migrations`,
  `control_admins`. Served by `migrate up | status | rollback | reset |
  refresh | fresh`.
- `migrations/tenants/{mysql,pgsql}` — dedicated tenant databases and
  shared pools: tenant data only, no control-plane tables. Served by
  `migrate pool up` (shared pool, `--pool`, default `DEFAULT_POOL`),
  `migrate provision <slug>` (create + migrate a dedicated database from
  `TENANT_DSN_TEMPLATE`), and `migrate tenant <slug>` (pool or
  dedicated, resolved via placement).

Versions (same per dialect across both domains, so the mirror test can
pin them):

- mysql: `20251107220300_users.sql`, `20251107220301_init_app.sql`
  (+ app-only `20261009000006_tenancy.sql`)
- pgsql: `20251108104100_users.sql`, `20251108104101_init_app.sql`
  (+ app-only `20261009000006_tenancy.sql`)

The base files already carry the tenant-data delta (`tenant_id` columns
+ indexes, composite `(tenant_id, email)` unique, `password_reset`
`(email, kind)` PK, pgsql RLS policies); `20261009000006_tenancy.sql`
(consolidated from the four stash files `20261009000002-05`, same four
`CREATE`s, one version) adds only the control-plane tables. Fresh
databases get the full schema from these files. Databases that applied
the base versions before the tenancy hunks landed need manual `ALTER`s
(goose never re-runs an applied version).

The `migrations/tenants/` mirror is kept, not folded in: `pool`,
`provision`, and `tenant` provision that domain directly, and the
mirror test (`TestTenantMirror`) blocks merges that change tenant data
in one domain but not the other. In single-database topologies (empty
`CONTROL_DSN`/`SHARED_POOL_DSNS`) both domains run against the same
physical database and share one goose version table — harmless, because
the mirrored files are identical and every statement is
`IF NOT EXISTS`.

## Operating moves

```bash
# app schema (tenant data + tenants, domains, ledger, admins)
ginplate migrate up
# tenant-data schema on a shared pool or dedicated database
ginplate migrate pool up
ginplate migrate tenant acme
ginplate migrate provision acme   # create + migrate a dedicated database

# move a tenant shared -> dedicated (ledgered, resumable)
ginplate migrate tenant-migrate acme --reason "enterprise tier"

# after the retention window (MIGRATION_RETAIN_DAYS, default 7)
ginplate migrate tenant-cleanup acme

# inspect / abort
ginplate migrate tenant-status acme
ginplate migrate tenant-rollback <migration-id>

# first control admin
ginplate migrate seed-admin "Root" root@example.com <password-12+chars>
```

Phases: `provision → copy → verify → cutover → retaining → done`, with
`failed` restoring shared/active after 10 attempts. While `migrating`,
writes 429 (reads flow); the worker also runs `tenant:migrate` jobs.

## Custom domains

A corporate tenant can point its own domain at the platform (DNS CNAME).
An admin maps it (`POST /admin/tenants/:slug/domains`, starts unverified),
proves ownership out of band, then flips `verified`
(`PATCH /admin/tenants/domains/:id/verify`). From then on the tenant's
frontend sends `X-Tenant-Domain` instead of the slug — it never needs to
know it. Example rows in `tenant_domains.domain`:

- `app.acmecorp.com`
- `portal.globex.io`
- `shop.example.co.uk`

Bare hostnames only: `https://app.acmecorp.com` (scheme), `app.acmecorp.com/login`
(path), and `app.acmecorp.com:8080` (port) are all rejected with 400.
Values normalize to lowercase. Only verified rows resolve; unverified and
unmapped domains return the same 404 as unknown slugs. Remember to add each
custom origin to `CORS_ALLOWED_ORIGINS` — that stays manual and explicit.

## Control plane

`POST /api/v1/admin/auth/login` (open) issues 12h control tokens on a
separate secret (`CONTROL_JWT_SECRET`, required in production); tenant
tokens can never authenticate there. Control sessions ride the shared
session store (logout revokes; `POST /admin/auth/logout`), and control
passwords reset through `POST /admin/auth/forgot|reset` against
`kind='control_admin'` rows — the same hardened contract as tenant users
(hashed, expiring, single-use). Tenant CRUD lives under
`/api/v1/admin/tenants` (create/list/get/status/migrate/rollback), and
`GET /api/v1/admin/notifications` serves the shared operational alert
inbox (tenant created/suspended, moves requested/completed/failed).

## Code layout

Three sibling packages under `internal/modules/`, one per unit —
`tenants` (`Tenants` service: lifecycle, status, removal),
`tenantdomains` (`TenantDomain` service: custom-domain mappings),
`tenantmigrations` (`TenantMigrations` service: ledgered moves). Each owns
its repository interface; a single `GormRepository` implements all three, and
cross-unit reads use narrow local interfaces so the dependency direction
stays acyclic (no import cycles). Each unit also owns its HTTP surface
(`Handler` + `routes.go` per package: tenant CRUD, domain mappings, and
move endpoints respectively); endpoint paths are unchanged by the split.
None of the four (including `cadmin`) is registered in
`internal/modules/register.go` yet, so their routes are not mounted —
wiring them into the router is still open work, not a flag flip.

## Notes and limits

- Email uniqueness is per tenant: `(tenant_id, email)`. Legacy rows on an
  existing database keep the empty-`tenant_id` sentinel default (no tenant
  claims them); all pre-tenancy tokens lack `tid` and stop working (one
  clean re-login).
- Sessions live in the shared session backend for every tenant (global
  JTIs, `tid`-bound, `tenant_id` recorded on DB rows) — like queue and
  cache brokers, which are shared with per-tenant envelopes/prefixes.
- Live MySQL + Postgres coverage (concurrent rotation, cutover) is
  CI-gated (`TEST_MYSQL_DSN`/`TEST_PGSQL_DSN`); hermetic suites cover
  routing, scoping SQL, guards, and the move state machine.
