# GinPlate Codebase Audit

## Executive summary

**Overall status: Not production-ready without remediation.**

The repository has a solid baseline: clear package boundaries, centralized configuration, graceful HTTP shutdown, database pool limits, structured logging, input validation, and a meaningful test suite. `go test ./...`, `go test -race ./...`, `go vet ./...`, and `go mod verify` all pass.

However, the following issues block production deployment:

1. **Vulnerable Go toolchain and dependencies**: `govulncheck` reports 24 vulnerabilities, including reachable issues in `net/http`, HTTP/2, TLS, `pgx`, and Redis.
2. **SMTP STARTTLS downgrade**: failed opportunistic STARTTLS can fall through to plaintext authentication and mail transmission.
3. **Database TLS verification downgrade**: invalid MySQL TLS verification configuration silently falls back to `skip-verify`.
4. **Authorization staleness**: role changes do not invalidate existing JWT privileges.
5. **Maintenance bypass secret leakage**: query-string secrets are exposed to access logs and upstream observability systems.

The working tree also contains substantial uncommitted changes, including deleted test files. The audit reflects the current working tree rather than a clean committed revision.

---

# Security

## Critical

No confirmed Critical vulnerabilities were identified.

## High

### 1. SMTP STARTTLS downgrade and credential disclosure

| Location | Severity | Issue |
|---|---|---|
| [pkg/mail/smtp.go](./pkg/mail/smtp.go), lines 91-96 | High | Failed opportunistic STARTTLS negotiation is ignored. |

When `STARTTLS` is advertised but negotiation fails, the implementation only returns the error when `MAIL_ENCRYPTION=tls`. In the default or empty encryption mode it continues to authenticate and send mail.

**Risk:** A network attacker capable of tampering with SMTP traffic can strip or interfere with STARTTLS. SMTP credentials, password-reset links, and transactional messages may then be transmitted in plaintext.

**Recommendation:** Fail closed whenever STARTTLS is advertised but fails. Require explicit TLS for authenticated SMTP and reject plaintext authentication:

```go
if ok, _ := client.Extension("STARTTLS"); ok {
    if err := client.StartTLS(tlsCfg); err != nil {
        return fmt.Errorf("mail: smtp starttls: %w", err)
    }
} else if m.username != "" || m.encryption == "tls" {
    return fmt.Errorf("mail: refusing SMTP without TLS")
}
```

Production configuration should reject authenticated SMTP unless encryption is explicitly configured.

### 2. MySQL TLS verification downgrade

| Location | Severity | Issue |
|---|---|---|
| [pkg/config/database.go](./pkg/config/database.go), lines 117-129 | High | MySQL TLS verification failure downgrades to `skip-verify`. |

`mysqlTLSParam` returns `skip-verify` when `registerMySQLVerifyTLS` fails. This contradicts production validation, which claims to require certificate verification.

**Risk:** A malformed CA bundle or incorrect server name can silently convert verified TLS into unauthenticated encrypted transport. A redirected or impersonated database endpoint could capture credentials and application data.

**Recommendation:** Propagate TLS registration errors instead of returning a string-only DSN parameter. Validate and register the TLS profile during configuration loading or startup, and refuse to boot on failure.

### 3. Vulnerable runtime and dependency versions

| Location | Severity | Issue |
|---|---|---|
| [go.mod](./go.mod), [Dockerfile](./Dockerfile), [.github/workflows/ci.yml](./.github/workflows/ci.yml) | High | Vulnerable Go toolchain and dependencies. |

`govulncheck` reported 24 vulnerabilities, including:

- Go standard library vulnerabilities in `net/http`, `crypto/tls`, `html/template`, `net/url`, `encoding/xml`, `encoding/asn1`, and `net/textproto`.
- HTTP/2 HPACK race and server-crash issues.
- `golang.org/x/net v0.57.0`, fixed in at least `v0.60.0`.
- `github.com/jackc/pgx/v5 v5.5.5`, fixed in at least `v5.9.2`.
- `github.com/redis/go-redis/v9 v9.7.0`, fixed in at least `v9.7.3`.

**Recommendation:** Upgrade to the latest supported patched Go release rather than pinning Go `1.26.0`. Update the Docker image, CI setup, `go.mod`, and rerun:

```bash
go get golang.org/x/net@latest
go get github.com/jackc/pgx/v5@latest
go get github.com/redis/go-redis/v9@latest
go mod tidy
go test -race ./...
go run golang.org/x/vuln/cmd/govulncheck@latest ./...
```

## Medium

### 4. JWT role claims remain valid after role demotion

| Location | Severity | Issue |
|---|---|---|
| [internal/modules/auth/service.go](./internal/modules/auth/service.go), lines 172-184; [internal/middleware/role.go](./internal/middleware/role.go), lines 27-41 | Medium | Authorization uses stale roles embedded in JWTs. |

Authorization uses the role embedded in the JWT. The active-user check verifies `IsActive`, but does not reload or compare the current role.

**Risk:** A demoted administrator retains administrative access until the access token expires. This violates immediate privilege revocation expectations.

**Recommendation:** Either load the current user role during authentication and authorize against the database value, or maintain a per-user authorization/session version and invalidate sessions whenever roles change.

### 5. Maintenance bypass secret accepted in query strings

| Location | Severity | Issue |
|---|---|---|
| [pkg/web/maintenance.go](./pkg/web/maintenance.go), lines 59-70 | Medium | Maintenance secrets can be supplied through `?secret=...`. |

The maintenance secret is accepted via a query parameter. Gin access logging runs globally before the maintenance middleware.

**Risk:** The secret can appear in application logs, reverse-proxy logs, monitoring systems, browser history, and referrer telemetry. Anyone with log access can replay it.

**Recommendation:** Remove query-string support and require an operator-only header or authenticated administrative mechanism. If compatibility requires query parameters, explicitly redact the parameter from every access-log and proxy layer.

### 6. Maintenance bypass is a static bearer secret

The bypass value is effectively a global bearer credential and appears to be stored in a maintenance marker file.

**Risk:** File disclosure, accidental log exposure, or reuse across deployments can bypass the complete application maintenance boundary.

**Recommendation:** Use a short-lived signed operator token or authenticated administrative endpoint. Store the secret through a secret manager, rotate it, and use constant-time comparison.

---

# Production readiness

## High

### 7. Notification queue silently falls back to synchronous delivery

| Location | Severity | Issue |
|---|---|---|
| [pkg/app/app.go](./pkg/app/app.go), `openNotifyQueue` | High | Redis failure changes persistent delivery to synchronous delivery. |

When Redis is configured but unreachable, the code logs a warning and returns a synchronous queue.

**Risk:** Requests can unexpectedly block on email or notification delivery. A transient Redis outage changes delivery semantics, increases latency, and may cause request failures or duplicate behavior.

**Recommendation:** Fail startup when an explicitly configured queue backend is unavailable. Only allow synchronous fallback behind an explicit development-only configuration flag.

### 8. Queue-created Redis client is not consistently owned or closed

`openNotifyQueue` may create a Redis client internally when `rdb == nil`, but the function returns only a queue and does not return ownership or cleanup information.

**Risk:** Long-running processes can retain clients, sockets, and goroutines without a clear shutdown path. Repeated initialization in tests or embedded use can leak resources.

**Recommendation:** Return a resource bundle with explicit cleanup:

```go
type QueueResources struct {
    Queue queue.Queue
    Close func() error
}
```

Close internally-created Redis clients during shutdown.

### 9. Health endpoints expose operational data without an access boundary

| Location | Severity | Issue |
|---|---|---|
| [pkg/app/router.go](./pkg/app/router.go), `/health` and `/metrics` | High | Operational data is publicly registered. |

`/health` exposes queue depth and failed-job counts. `/metrics` is publicly registered.

**Risk:** Operational state can be used for reconnaissance and may expose workload, outage, or job-processing information. Metrics endpoints are commonly scraped from trusted networks, not public interfaces.

**Recommendation:** Keep `/livez` public if needed, but protect `/readyz`, `/health`, and `/metrics` with network policy, mTLS, an internal listener, or authentication. Avoid database-heavy checks on every health request.

## Medium

### 10. Health checks perform database schema checks and counts per request

`/health` and `/metrics` call `Migrator().HasTable` and count queue records on demand.

**Risk:** Monitoring traffic can add database load, especially during outages when probes become more frequent. `HasTable` can also generate metadata queries.

**Recommendation:** Use lightweight cached gauges updated by workers, or perform a single bounded health query with a strict timeout. Separate liveness from dependency readiness.

### 11. Shutdown does not explicitly close all service resources

HTTP shutdown is coordinated, and the SQL pool is closed, but sender/queue/resource ownership is not consistently modeled.

**Risk:** Graceful shutdown may return while background clients or worker goroutines still exist, causing connection leaks or truncated queue work.

**Recommendation:** Define an application resource container with ordered shutdown:

1. Stop accepting requests.
2. Stop schedulers and workers.
3. Drain or cancel queue work.
4. Close mail, Redis, database, and logging resources.
5. Enforce the configured shutdown deadline.

### 12. Fixed database pool limits are not configuration-driven

| Location | Severity | Issue |
|---|---|---|
| [pkg/database/database.go](./pkg/database/database.go), lines 46-53 | Medium | Pool limits are hardcoded. |

The pool is hardcoded to 20 open connections and 5 idle connections.

**Risk:** The values may be too small for production traffic or too large when multiple API, worker, and scheduler replicas share the same database. This can create latency or exhaust database connection limits.

**Recommendation:** Expose `DB_MAX_OPEN_CONNS`, `DB_MAX_IDLE_CONNS`, `DB_CONN_MAX_LIFETIME_SEC`, and `DB_CONN_MAX_IDLE_TIME_SEC`, with safe defaults and production validation.

### 13. No explicit request body size limit

The HTTP server has read/write/idle timeouts, but request-size limiting is not evident at the global middleware level.

**Risk:** Large JSON or multipart requests can cause memory pressure and denial of service before application validation runs.

**Recommendation:** Apply a global `http.MaxBytesReader` or Gin request-size middleware, with route-specific limits for uploads.

---

# Code quality and maintainability

## High

### 14. Generated command scaffold contains intentionally non-functional commands

| Location | Severity | Issue |
|---|---|---|
| [pkg/commands/makecommand/makecommand.go](./pkg/commands/makecommand/makecommand.go), lines 135-139 | High | Generated commands return success while doing nothing. |

Generated commands return success while printing `"not implemented yet"`.

**Risk:** A generated command can be deployed or invoked in automation while reporting success despite doing nothing.

**Recommendation:** Return an explicit error until implemented:

```go
return fmt.Errorf("command %q is not implemented", cmd.Name())
```

Alternatively generate a compile-time TODO marker requiring implementation.

### 15. Generated jobs can silently acknowledge messages without doing work

| Location | Severity | Issue |
|---|---|---|
| [pkg/commands/makejob/makejob.go](./pkg/commands/makejob/makejob.go), lines 157-169 | High | Generated jobs return `nil` after decoding payloads. |

Generated jobs unmarshal payloads and then return `nil` without performing work.

**Risk:** A newly generated job can acknowledge and permanently remove a queue message while doing nothing.

**Recommendation:** Return an explicit `ErrNotImplemented` until the handler is completed, or generate a compile-time placeholder requiring implementation.

### 16. Error handling loses operational context in several paths

There are multiple ignored errors, including Redis and logger close errors, metric query errors, maintenance file read errors treated as “not down,” and hook failures intentionally ignored.

**Risk:** Operational failures can be hidden, making outages and data-loss conditions difficult to diagnose.

**Recommendation:**

- Ignore only documented, non-actionable cleanup errors.
- Log failed metric collection at debug or warn level.
- Fail closed for maintenance-marker read errors if the marker is security-sensitive.
- Record hook failures with structured context and counters.

## Medium

### 17. Registration through `init` increases hidden coupling

| Location | Severity | Issue |
|---|---|---|
| [pkg/app/router.go](./pkg/app/router.go), [internal/modules/register.go](./internal/modules/register.go) | Medium | Routes, middleware, jobs, and exceptions rely on package initialization side effects. |

Registration order and side effects are implicit. Tests can become order-dependent, and import changes can silently remove behavior.

**Recommendation:** Keep the mechanism for scaffolding if desired, but expose an explicit `RegisterAll(registry)` path for production wiring and tests.

### 18. Global mutable authentication callback

`middleware.ActiveCheck` is a package-level mutable function.

**Risk:** Tests and multiple application instances can race or overwrite one another. The callback also creates implicit global state.

**Recommendation:** Store `ActiveCheck` on an authentication middleware instance or dependency container. Avoid mutable package globals.

### 19. File session driver scans the entire directory

| Location | Severity | Issue |
|---|---|---|
| [pkg/session/file.go](./pkg/session/file.go) | Medium | Refresh operations scan every session file. |

`RefreshValid` scans every session file for each refresh operation.

**Risk:** Refresh latency and filesystem load grow linearly with session count. Expired files can accumulate because cleanup is opportunistic.

**Recommendation:** Use a database or Redis index for production. If retaining the file driver for development, add periodic cleanup and enforce a maximum session count.

---

# Dependencies

## High

### 20. Vulnerability scanning is expected to fail with the current dependency set

The CI workflow runs `govulncheck`, but the current dependency set causes a non-zero result. The intended CI security gate therefore fails until the vulnerable versions are upgraded.

| Location | Severity | Issue |
|---|---|---|
| [go.mod](./go.mod), [.github/workflows/ci.yml](./.github/workflows/ci.yml) | High | Vulnerable dependencies remain in the build graph. |

**Recommendation:** Upgrade Go and the affected dependencies first. Ensure CI scans the exact production build/toolchain.

## Medium

### 21. Dependency versions are not centrally reviewed or documented

The repository has a large dependency graph, including AWS SDK components, Gin, GORM, Swagger, Redis, pgx, and multiple indirect packages.

**Risk:** Transitive vulnerabilities can remain unnoticed between periodic scans. The dependency update output also shows numerous available updates.

**Recommendation:** Adopt a dependency update policy:

- Automated pull requests for direct dependencies.
- Weekly vulnerability scans.
- A supported Go-version matrix.
- Release-note review for security-sensitive libraries.
- Production images locked to patched digest versions where practical.

---

# Testing and reliability

## High

### 22. Deleted tests reduce regression coverage

The current worktree deletes multiple tests, including:

- `internal/modules/auth/password_reset_test.go`
- `internal/modules/auth/service_test.go`
- `internal/modules/users/service_test.go`
- `pkg/notify/database_test.go`

Although the remaining suite passes, coverage has materially changed.

**Risk:** Authentication, password reset, user-service, and notification database regressions may no longer be detected.

**Recommendation:** Restore or replace the deleted tests before merging. Require coverage thresholds for authentication, authorization, queue processing, and database repositories.

## Medium

### 23. Missing end-to-end tests for production wiring

There are unit and package tests, but limited evidence of full-stack tests covering:

- HTTP server startup.
- Real route registration.
- Authentication plus session revocation.
- Redis/database queue behavior.
- Graceful shutdown with in-flight requests.
- TLS configuration.
- Maintenance mode.
- Health and metrics access boundaries.

**Recommendation:** Add integration tests using ephemeral MySQL/Postgres and Redis containers or test services. Include failure-path tests for dependency outages and shutdown deadlines.

### 24. Missing security regression tests

The following should be explicitly tested:

- STARTTLS negotiation failure must abort delivery.
- Invalid MySQL CA/server-name configuration must fail startup.
- Role demotion invalidates or rejects old privileged tokens.
- Maintenance secrets in query strings are rejected or redacted.
- Oversized request bodies are rejected.
- Metrics and health endpoints require intended access controls.

## Low

### 25. Several packages have no direct tests

Examples include command entrypoints, validator wiring, jobs, scheduling commands, and HTTP middleware integration.

**Recommendation:** Prioritize behavior tests around exported package APIs and failure paths rather than aiming for uniform line coverage.

---

# Configuration and deployment

## High

### 26. Docker build is not reproducible enough

| Location | Severity | Issue |
|---|---|---|
| [Dockerfile](./Dockerfile) | High | Base images use mutable tags. |

The base images use mutable tags such as `golang:1.26-alpine` and `alpine:3.21`.

**Risk:** A rebuild can silently change the toolchain or OS package set, introducing regressions or vulnerabilities.

**Recommendation:** Pin images to patched version tags or digests, and update them through a controlled dependency process.

### 27. Compose Redis has no authentication or TLS

| Location | Severity | Issue |
|---|---|---|
| [docker-compose.yml](./docker-compose.yml) | High | Development Redis has no authentication or TLS. |

This is documented as local development only, which reduces severity, but the configuration can still be copied into an insecure environment.

**Recommendation:** Make the development-only boundary more explicit in startup checks. For shared environments require Redis authentication, TLS, and a non-default ACL user.

## Medium

### 28. Configuration validation is stronger for production than for staging

Most security checks are activated only when `APP_ENV` is `production` or `prod`.

**Risk:** Staging environments often contain real user data and credentials but may run with weaker TLS, CORS, logging, or mail settings.

**Recommendation:** Use explicit security profiles such as `local`, `test`, `staging`, and `production`, or enforce secure defaults for all non-test deployments.

### 29. `APP_URL` and proxy trust assumptions need documentation

The application may terminate TLS behind a reverse proxy while listening over HTTP internally.

**Risk:** Incorrect proxy header handling can result in incorrect generated URLs, redirect behavior, secure-cookie decisions, and misleading logs.

**Recommendation:** Document and configure trusted proxy behavior explicitly. Validate forwarded headers only from trusted proxy networks.

---

# Positive findings

- Database pool limits and connection lifetime settings are present.
- HTTP read, write, idle, and shutdown timeouts are configured.
- The HTTP server uses graceful shutdown.
- JWT parsing restricts accepted algorithms to HS256.
- File sessions use restrictive directory and file permissions.
- SQL query parameters are generally used for user-controlled values.
- Route sorting and user sort fields are allow-listed.
- Production configuration rejects several unsafe settings.
- The full test suite and race detector currently pass.
- Module checksums verify successfully.
- Docker runs the application as a non-root user.
- Compose ports are bound to loopback for local development.

## Recommended remediation order

1. Upgrade Go, `x/net`, pgx, Redis, and all vulnerable dependencies.
2. Fix SMTP STARTTLS and MySQL TLS fail-open behavior.
3. Correct role revocation semantics.
4. Remove or protect query-string maintenance secrets.
5. Eliminate silent Redis-to-sync queue fallback.
6. Restore deleted security and service tests.
7. Add integration tests for startup, shutdown, queue failures, and auth/session behavior.
8. Pin container images and make pool/resource settings configurable.
