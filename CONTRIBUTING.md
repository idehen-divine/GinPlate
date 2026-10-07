# Contributing to GinPlate

Thanks for helping improve GinPlate. This project is a backend starter, so
contributions should keep the codebase predictable, boring in the good way,
and easy to extend.

## Development Setup

```bash
cp .env.example .env
go run ./cmd/ginplate key:generate
go run ./cmd/ginplate migrate up
go run ./cmd/ginplate serve
```

Run checks before opening a pull request:

```bash
make fmt
make vet
make test
```

Or run the bundled check target:

```bash
make check
```

## Branches

Use short, descriptive branch names:

```text
feat/password-reset-expiry
fix/session-rotation
docs/mail-setup
```

## Commit Messages

Use Conventional Commit-style messages without scopes:

```text
feat: add password reset expiry
fix: reject unsafe storage paths
test: add queue retry coverage
docs: document mail setup
refactor: simplify command registration
chore: tidy module dependencies
```

Avoid scoped commit syntax:

```text
feat(auth): add password reset expiry
```

## Code Style

- Run `gofmt` on Go files.
- Keep handlers thin: bind input, call one service method, render a response.
- Keep business rules in services.
- Keep persistence in repositories.
- Return classified `web.AppError` values from services when possible.
- Use typed roles from `pkg/web` instead of string literals.
- Prefer existing package patterns over new abstractions.
- Add tests near the behavior you change.

## Module Shape

Resource modules should follow this structure:

```text
model.go
dto.go
repository.go
service.go
handler.go
routes.go
resource.go
service_test.go
```

Process modules, such as authentication, can skip `model.go` and
`repository.go` when they coordinate existing data instead of owning a table.

## Migrations

Create migrations with the CLI:

```bash
go run ./cmd/ginplate make:migration CreatePostsTable --create posts
```

Do not hand-number migration files. Generated migration versions use UTC
timestamps so files apply in creation order.

When a schema change supports both MySQL and PostgreSQL, update both dialects.

## Generators

Use the generators for common project shapes:

```bash
go run ./cmd/ginplate make:command SendReport
go run ./cmd/ginplate make:job Billing.Charge
go run ./cmd/ginplate make:mail OrderShipped
go run ./cmd/ginplate make:notification OrderShipped
```

After deleting a generated command directory, refresh generated imports:

```bash
make sync-commands
```

## Testing

Add focused tests for:

- service rules
- repository behavior when query logic changes
- queue, cache, session, or storage driver changes
- command generators
- validation and error formatting

Some tests open local sockets for Redis or SMTP-style test servers. If your
environment blocks local socket creation, rerun the suite somewhere that allows
loopback networking.

## Pull Request Checklist

- The change is focused on one concern.
- `make fmt` passes.
- `make vet` passes.
- `make test` passes.
- Both MySQL and PostgreSQL migrations are updated when needed.
- Documentation is updated when commands, configuration, or workflows change.
- No secrets, `.env` files, generated binaries, or local storage files are
  committed.

## Security

Do not open public issues for sensitive vulnerabilities. If you discover a
security issue, contact the maintainer privately so it can be fixed before
details are published.
