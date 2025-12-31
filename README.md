# GinPlate

Go + Gin API boilerplate: JWT auth with driver-selected sessions, and a
single cobra-powered binary.

## Features

- **Auth** (`internal/modules/auth`, `internal/modules/users`): signup/login/refresh/check, bcrypt,
  driver-selected sessions (`SESSION_DRIVER`: `database`, `redis`, or `file`;
  rotation + full-session kill on logout), token versioning. `GET /users`
  is admin-only via `RequireRole` — copy that pattern for protected routes.
  Service errors return classified `web.AppError`, so handlers just render.
- **Storage** (`pkg/storage`): `local`, `public`, and `s3` disks behind one
  interface (`storage.Open(cfg.Filesystem)`), traversal-safe paths.
- **Mail** (`pkg/mail` transport + `internal/mail` mailables): `log`, `smtp`,
  and `ses` drivers behind one interface (`mail.Open(cfg.Mail,
  cfg.Filesystem.S3, queue)`), full messages (To/Cc/Bcc, Text+HTML,
  attachments + inline, headers, tags). Laravel-style mailables live one per
  directory (`internal/mail/welcome/`, `internal/mail/password_reset/` — struct
  beside its template); handlers send via `appmail.Send` / `appmail.Queue`,
  new mails via `ginplate make:mail OrderShipped`. `ginplate mail:test
  --to a@b.c` verifies delivery; `POST /api/v1/mail/preview` (admin,
  debug-only) does it over HTTP.
- **Notifications** (`pkg/notify` + `internal/notifications/`): Laravel-style
  notifications over expandable channels (`database`, `mail` bundled; future
  ones plug in via `RegisterChannel`). One dir per notification
  (`internal/notifications/welcome/` reuses the Welcome mailable); callers
  pick `Send` (inline, now) or `Queue` (background `notification.send` job)
  per call. Inbox API: `GET /notifications` (paged + unread count),
  `POST /notifications/:id/read`, `POST /notifications/read-all`
  (caller-scoped). Signup queues Welcome automatically; new ones via
  `ginplate make:notification OrderShipped`.
- **HTTP hardening** (`pkg/web/httpmw`): CORS, security headers, gzip,
  per-instance rate limiting; graceful shutdown with timeouts.
- **Conventions**: standard JSON envelope, paged lists
  (`?limit=&offset=&sort=&order=&search=`), Swagger UI, Zap logging with
  Gin/GORM adapters, repository-pattern example (`internal/modules/users`).
- **Errors**: `APP_DEBUG=true` returns error detail and stack traces (4xx
  payloads always show; 5xx detail is debug-only, bare `Server Error`
  otherwise). Panics are logged server-side either way. Every error funnels
  through `web.Render`: `gorm.ErrRecordNotFound` → 404, classified
  `web.AppError` keeps its status, unknown failures → safe 500s.

Every response uses one envelope (all keys always present, `code`
mirrors the HTTP status):

```json
{"success": true, "code": 200, "message": "Users.", "data": {...}, "errors": null}
{"success": false, "code": 422, "message": "The given data was invalid.", "data": null, "errors": {"email": "..."}}
```

## Quickstart

```bash
cp .env.example .env            # mysql by default; pgsql example inside
go run ./cmd/ginplate key:generate  # fill APP_KEY (required to start)
go run ./cmd/ginplate migrate up   # create the database + run the schema
go run ./cmd/ginplate serve        # API on :8080, swagger on /swagger/index.html
```

Run `make help` for Make targets and `make commands` (or
`ginplate --help`) for the CLI tree. One binary does everything:
`cmd/ginplate` (serve, migrate, generators, custom).

## Install the CLI

```bash
make install   # go install ./cmd/ginplate → $HOME/go/bin/ginplate
```

If your shell says `command not found`, that directory isn't on your
`PATH` yet — fix the current shell and persist it:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
echo 'export PATH="$PATH:$HOME/go/bin"' >> ~/.bashrc   # or ~/.zshrc
```

After that, bare `ginplate serve`, `ginplate migrate status`, etc. work
from any directory. Re-run `make install` after generating new commands —
the installed binary is a compiled snapshot.

## Commands

```
ginplate serve                  # run the API
ginplate queue:work             # run the background worker (database/redis/sync per QUEUE_CONNECTION)
ginplate schedule:work          # push due schedule entries to the queue
ginplate queue:failed [--limit N]  # list buried jobs (retry/forget/flush too)
ginplate make:job Billing.Charge [--schedule=daily@02:00]  # scaffold a job handler (+ optional schedule)
ginplate migrate up             # create the database + run pending migrations
ginplate migrate status         # show applied/pending migrations
ginplate migrate rollback       # revert the last applied migration
ginplate migrate reset           # revert all applied migrations
ginplate migrate refresh        # revert all, then re-apply (dev/test)
ginplate migrate fresh          # drop every table, migrate from scratch (DESTRUCTIVE)
ginplate make:migration <Name>  # scaffold a versioned migration (both dialects)
ginplate make:command <Name>    # scaffold a new custom command (auto-registered)
ginplate make:mail OrderShipped # scaffold a mailable (struct + template side by side)
ginplate make:notification OrderShipped # scaffold a notification (database + mail channels)
ginplate key:generate           # fill APP_KEY in .env (--show to print only)
ginplate mail:test --to a@b.c   # send a test email (--queue for the job path)
ginplate down [--secret S]      # maintenance mode on (503s, optional bypass + retry)
ginplate up                     # maintenance mode off
```

## Migrations (goose-powered)

| Command | What it does |
|---|---|
| `migrate up` | create DB if missing + run pending |
| `migrate status` | list applied / pending |
| `migrate rollback` | revert last batch |
| `migrate reset` | revert everything |
| `migrate refresh` | reset + re-apply |
| `migrate fresh` | drop all tables + migrate (data loss!) |

New schema changes via the generator (never hand-number files):

```bash
go run ./cmd/ginplate make:migration CreatePostsTable --create posts
# → migrations/app/{mysql,pgsql}/20261006200000_create_posts_table.sql
go run ./cmd/ginplate migrate up
```

Versions are UTC timestamps, so files always apply in creation order —
one table per file, never hand-number anything.

`make:` targets mirror the CLI: `make migrate-up|status|rollback|reset|refresh|fresh`
and `make make-migration NAME=CreatePostsTable CREATE=posts`.

## Adding your own command

```bash
go run ./cmd/ginplate make:command SendReport   # writes internal/commands/send-report/send_report.go
```

Each command gets its own directory + Go package and self-registers via
`init()`, so after rebuilding it appears as `ginplate send-report` — no
manual wiring. The import list linking them lives in
`pkg/commands/generated.go` (never hand-edit it). To remove a command,
delete its directory, then `make sync-commands` — it deletes the stale
`generated.go` first (stale imports would stop the tool itself from
compiling) and regenerates it from disk. To hand-write one instead,
copy the shape of any generated stub: its own folder, a constructor, plus
an `init()` calling `custom.RegisterCustom(use, constructor)`.

Keep run logic in `pkg/app` (or your modules) and let the constructor only
wire flags → function, so commands stay testable.

## Background jobs

`ginplate queue:work` pops jobs off `QUEUE_CONNECTION` (`sync` runs handlers
inline on push, so no worker is needed; `database` uses the `jobs` table;
`redis` a list) and dispatches to handlers in `internal/jobs` (see the
`log.hello` example). Failures retry with backoff up to
`QUEUE_TRIES` total runs, then bury. Add a handler with
`queue.Handle("name", func(ctx, job) error {...})` in `init()`.

Jobs that exhaust `QUEUE_TRIES` are buried into the `failed_jobs` table
with their last error (worker records automatically when a database is
reachable, else burial stays log-only):

```
ginplate queue:failed            # list buried jobs
ginplate queue:retry <id|all>    # re-queue for a fresh run (attempts restart)
ginplate queue:forget <id>       # delete one buried job
ginplate queue:flush --force     # delete all buried jobs
```

Database-backed reservations hold a 60s lease: a handler running longer
than that gets reserved by a second worker too, so keep handlers fast or
idempotent past 60s.

## Scheduling

`ginplate schedule:work` ticks every minute and pushes due entries onto
the queue broker for `queue:work` processes to run — Laravel-style. Entries live
next to their handlers in `internal/jobs` and self-register in `init()`:

```go
scheduler.Schedule(
    scheduler.New("billing.charge").
        DailyAt(2, 0).
        WithoutOverlapping(),   // skip ticks while a run is unsettled
    scheduler.New("reports.daily").
        Hourly().
        OnOneServer(),          // one fire per tick across replicas
)
```

Builders: `EveryMinute()`, `Hourly()`, `DailyAt(h, m)`,
`Weekly(day, h, m)`, `Cron("0 2 * * *")`, plus `ExpireAfter(d)` to bound
overlap locks (default 24h, so a crashed worker delays but never wedges a
schedule). Overlap entries wrap the payload with lock metadata — unwrap in
the handler with `scheduler.Data(job.Payload)` (plain payloads pass
through). Overlap locks need a shared locker to span processes: Redis when
reachable, in-process otherwise (fine for a single replica; run the redis
queue connection for fleet-wide exclusion).

## Serving files

Anything written through the `public` disk (`storage/app/public` by
default) is served world-readable at `PublicURL` — the route path comes
from the URL itself (`https://app.com/storage` → `/storage/*`), so what
`disk.URL()` returns always resolves. Missing files 404; `..` escapes are
blocked. Private `local` files are never served over HTTP; `s3` objects
keep using presigned links (see `Storage.URL`).

One file per domain, one nested struct each, loaded by a single `Load()`
from `.env` + environment (env wins). Every key has a code default, so a
bare `.env` still boots — except `APP_KEY`, which must exist (run
`key:generate`) or startup fails fast rather than signing weak tokens.

| File | Holds | Notable keys |
|---|---|---|
| `app.go` | identity, server, HTTP, maintenance | `APP_NAME/ENV/DEBUG/PORT/URL`, timeouts, CORS, `APP_MAINTENANCE_*` |
| `auth.go` | JWT | `APP_KEY` (raw or `base64:`), `APP_TTL_MIN` (access; refresh fixed 30d) |
| `database.go` | DB + shared Redis | `DB_CONNECTION/HOST/PORT/DATABASE/USERNAME/PASSWORD`, `REDIS_*` |
| `cache.go` | cache store: `redis`, `database`, or `memory` | `CACHE_STORE/PREFIX` |
| `filesystem.go` | local/public/s3 disks | `FILESYSTEM_DISK/ROOT/PUBLIC_*`, `AWS_*` |
| `mail.go` | mailer: `log`, `smtp`, or `ses` | `MAIL_MAILER/HOST/PORT/USERNAME/PASSWORD/ENCRYPTION/TIMEOUT_SEC`, `MAIL_FROM_*`, SES reuses `AWS_*` |
| `queue.go` | queue backend + retry budget | `QUEUE_CONNECTION`, `QUEUE_TRIES` |
| `logging.go` | log level/output | `LOG_LEVEL/OUTPUT` |
| `session.go` | session driver selection | `SESSION_DRIVER` (`database`, `redis`, `file`) |
| `services.go` | third-party creds (empty scaffold) | add `SERVICES_*` per vendor |

## Layout

```
cmd/ginplate             single main binary (serve|migrate|generators)
pkg/app                  shared entrypoints (RunAPI, migrate ops)
pkg/commands             cobra tree: root + serve/ migration/ makecommand/ makejob/ maintenance/ key/ work/ schedule/ folders
internal/commands        clonable stubs (package custom, self-registering via init)
pkg/queue                Queue interface + sync/database/redis drivers, worker loop, Registry
pkg/scheduler            Cron entries, overlap/server locks (memory + redis), schedule:work loop
internal/jobs            Handler + schedule-entry registry (log.hello example); extend via `make:job`
internal/mail            Mailable per directory (struct + template); send via appmail; extend via `make:mail`
internal/notifications   Notification per directory (Via/ToMail/ToDatabase); send via notify; extend via `make:notification`
internal/modules/notifications  Inbox API (list + read + read-all, caller-scoped)
pkg/web                  generic HTTP primitives (response, paging, auth, db seam)
internal/modules         auth, users (accounts + paged listing)
pkg/config, database (gorm + goose), redis, logger, storage, mail, validator, cache
migrations/app           base schema, mysql + pgsql (users, password_reset_tokens, sessions, caches, jobs, failed_jobs, notifications)
```

## Tenancy

Need multi-tenancy? Check out the `tenancy` branch.
