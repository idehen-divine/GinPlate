# GinPlate

A batteries-included Go API starter built with Gin, Cobra, GORM, Goose,
Redis, Zap, queues, mail, notifications, storage, and a Laravel-inspired
developer workflow.

GinPlate is meant to be a clean backend foundation: one binary, predictable
module boundaries, explicit configuration, structured responses, and enough
infrastructure to build a real API without spending the first week wiring
plumbing.

## Highlights

- JWT authentication with access tokens, refresh tokens, session rotation,
  password reset support, and role-aware route guards.
- Driver-based sessions, cache, queue, storage, and mail.
- MySQL and PostgreSQL migrations powered by Goose.
- Cobra CLI for serving the API, running migrations, queue workers,
  schedulers, generators, maintenance mode, and mail testing.
- Queue system with sync, database, and Redis backends.
- Failed job listing, retry, forget, and flush commands.
- Scheduler with cron expressions, overlap locks, and one-server execution.
- Mail system with log, SMTP, and SES transports.
- Laravel-style mailables and notifications.
- Notification inbox API with unread counts and read actions.
- Local, public, and S3-compatible storage disks.
- Standard JSON response envelope and classified application errors.
- HTTP hardening with CORS, gzip, security headers, rate limiting, graceful
  shutdown, and panic recovery.
- Zap logging with Gin and GORM integration.
- Generators for commands, migrations, jobs, mailables, and notifications.

## Tech Stack

| Concern | Package |
| --- | --- |
| HTTP | Gin |
| CLI | Cobra |
| ORM | GORM |
| Migrations | Goose |
| Config | Viper |
| Logging | Zap |
| Auth | JWT, bcrypt |
| Queue | Sync, database, Redis |
| Cache | Memory, database, Redis |
| Mail | Log, SMTP, SES |
| Storage | Local, public, S3 |

## Requirements

- Go `1.26.0` or newer, matching `go.mod`
- MySQL or PostgreSQL
- Redis, optional but recommended for sessions, cache, queues, and scheduler
  locks

## Quick Start

```bash
cp .env.example .env
go run ./cmd/ginplate key:generate
go run ./cmd/ginplate migrate up
go run ./cmd/ginplate serve
```

The API starts on `http://localhost:8080` by default.

Swagger can be served at `/swagger/index.html` when generated docs are
available and `ENABLE_SWAGGER=true`.

## Install the CLI

```bash
make install
```

If `ginplate` is not found after installation, add Go binaries to your shell
path:

```bash
export PATH="$PATH:$(go env GOPATH)/bin"
```

Once installed, commands can be run directly:

```bash
ginplate serve
ginplate migrate status
ginplate queue:work
```

## Configuration

Configuration loads from `.env` and environment variables. Environment
variables win over `.env` values.

Important defaults:

| Key | Default | Description |
| --- | --- | --- |
| `APP_PORT` | `8080` | HTTP server port |
| `APP_DEBUG` | `false` | Enables detailed error output |
| `DB_CONNECTION` | `mysql` | `mysql` or `pgsql` |
| `SESSION_DRIVER` | `redis` | `redis`, `database`, or `file` |
| `CACHE_STORE` | `redis` | `redis`, `database`, or `memory` |
| `QUEUE_CONNECTION` | `sync` | `sync`, `database`, or `redis` |
| `MAIL_MAILER` | `log` | `log`, `smtp`, or `ses` |
| `FILESYSTEM_DISK` | `local` | `local`, `public`, or `s3` |

`APP_KEY` is required for startup. Generate it with:

```bash
go run ./cmd/ginplate key:generate
```

## Commands

```bash
ginplate serve
ginplate migrate up
ginplate migrate status
ginplate migrate rollback
ginplate migrate reset
ginplate migrate refresh
ginplate migrate fresh
ginplate make:migration CreatePostsTable --create posts
ginplate make:command SendReport
ginplate make:job Billing.Charge --schedule=daily@02:00
ginplate make:mail OrderShipped
ginplate make:notification OrderShipped
ginplate queue:work
ginplate queue:failed
ginplate queue:retry <id|all>
ginplate queue:forget <id>
ginplate queue:flush --force
ginplate schedule:work
ginplate key:generate
ginplate mail:test --to user@example.com
ginplate down
ginplate up
```

The Makefile mirrors the common commands:

```bash
make help
make run
make worker
make scheduler
make migrate-up
make test
make check
```

## API Responses

All HTTP responses use one envelope:

```json
{
  "success": true,
  "code": 200,
  "message": "Users.",
  "data": {},
  "errors": null
}
```

Validation and application errors use the same shape:

```json
{
  "success": false,
  "code": 422,
  "message": "The given data was invalid.",
  "data": null,
  "errors": {
    "email": "The email field is required."
  }
}
```

Service layers return classified `web.AppError` values. Handlers bind input,
call one service method, and render with `web.Success` or `web.Render`.

## Project Layout

```text
cmd/ginplate                   Application binary entrypoint
internal/commands              Custom command registry
internal/jobs                  Queue job registry and scheduled jobs
internal/mail                  Application mailables
internal/modules/auth          Authentication workflow
internal/modules/users         User resource module
internal/modules/notifications Notification inbox module
internal/notifications         Application notifications
migrations/app                 MySQL and PostgreSQL migrations
pkg/app                        Runtime wiring for API, workers, scheduler
pkg/cache                      Cache interface and drivers
pkg/commands                   Cobra command tree and generators
pkg/config                     Environment configuration
pkg/database                   GORM and migration database helpers
pkg/logger                     Zap logger setup
pkg/mail                       Mail messages, templates, transports
pkg/notify                     Notification channels and delivery
pkg/queue                      Queue brokers, worker, failed jobs
pkg/redis                      Redis client setup
pkg/scheduler                  Scheduled entries and locks
pkg/session                    Session stores
pkg/storage                    Storage disks
pkg/validator                  Validation formatting
pkg/web                        HTTP responses, errors, auth, request helpers
```

## Module Pattern

Resource modules follow this shape:

```text
model.go        Database model and table details
dto.go          Request DTOs
repository.go   Persistence boundary
service.go      Business rules and classified errors
handler.go      HTTP binding and rendering
routes.go       Route registration and middleware
resource.go     Response shaping
service_test.go Focused service coverage
```

`internal/modules/users` is the reference implementation.

## Migrations

Create migrations through the generator:

```bash
go run ./cmd/ginplate make:migration CreatePostsTable --create posts
```

This writes matching files for:

```text
migrations/app/mysql
migrations/app/pgsql
```

Run pending migrations:

```bash
go run ./cmd/ginplate migrate up
```

Use `migrate fresh` carefully. It drops every table before rebuilding the
schema.

## Queues

Set `QUEUE_CONNECTION` to `sync`, `database`, or `redis`.

```bash
ginplate queue:work
ginplate queue:failed
ginplate queue:retry all
```

Jobs retry according to `QUEUE_TRIES`. Exhausted jobs are buried into
`failed_jobs` when a database connection is available.

## Scheduler

Scheduled jobs are registered from Go code:

```go
scheduler.Schedule(
    scheduler.New("billing.charge").DailyAt(2, 0).WithoutOverlapping(),
    scheduler.New("reports.daily").Hourly().OnOneServer(),
)
```

Run the scheduler loop:

```bash
ginplate schedule:work
```

Redis-backed locks are used when Redis is available. Otherwise GinPlate falls
back to in-process locks, which are suitable for single-instance development.

## Mail

Set `MAIL_MAILER` to `log`, `smtp`, or `ses`.

```bash
ginplate mail:test --to user@example.com
ginplate make:mail OrderShipped
```

Mailables live in `internal/mail/<name>` with the Go type beside its template.

## Notifications

Notifications support database and mail channels out of the box.

```bash
ginplate make:notification OrderShipped
```

The inbox module exposes authenticated routes for listing notifications,
marking one notification as read, and marking all notifications as read.

## Storage

Use `FILESYSTEM_DISK` to select `local`, `public`, or `s3`.

The public disk serves files from the configured public URL path. Local private
files are not exposed over HTTP, and path traversal attempts are rejected.

## Development

```bash
make fmt
make vet
make test
make check
```

Run the full test suite:

```bash
go test ./...
```

See [CONTRIBUTING.md](CONTRIBUTING.md) for module conventions, commit style,
and contribution workflow.

## License

GinPlate is open-sourced under the [MIT License](LICENSE).
