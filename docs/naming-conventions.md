# Naming Conventions

Naming should make the ownership, responsibility, and lifetime of a value
clear without requiring the reader to inspect several files. Prefer names
that describe what a dependency does over names that describe only its
implementation.

The goal is not to eliminate every short identifier. Go's standard short
names are useful in small, obvious scopes. The rule is to spend descriptive
names where they reduce architectural or maintenance ambiguity.

## General rules

- Use idiomatic Go casing: `UserService`, `HTTPClient`, `UserID`,
  `DBConnection`.
- Do not use snake case or lowercase compound type names such as
  `userservice`.
- Avoid redundant package prefixes. In package `users`, use `Service`,
  `Repository`, and `Handler`; outside the package use `users.Service` and
  `users.Repository`.
- Name interfaces after the behavior they provide, usually with a noun or
  `-er` suffix: `Repository`, `Sender`, `Queue`, `Store`.
- Keep interfaces small and define them close to the consumer when possible.
- Use concrete names for implementations when the distinction matters:
  `GormRepository`, `RedisStore`, `FileStore`, `SMTPSender`.
- Use names that distinguish values with different lifetimes or ownership:
  `requestContext`, `databaseConnection`, `sessionStore`.
- Do not rename code only for aesthetics in an unrelated change. Apply naming
  improvements when touching the surrounding behavior or during a focused
  refactor.

## Services and service interfaces

Services contain business rules and coordinate repositories, queues, mailers,
and other application dependencies.

Use a descriptive interface name at a boundary:

```go
type UserService interface {
    List(context.Context, *gorm.DB, web.ListFilter) (web.ListResult[User], error)
}
```

The handler should depend on the interface rather than the concrete service:

```go
type Handler struct {
    service UserService
}

func NewHandler(service UserService) *Handler {
    return &Handler{service: service}
}
```

Use `service`, not `svc`, for a handler field or constructor parameter when
the package already establishes the domain. `users.Handler` already tells the
reader that this is the users service.

The concrete implementation may remain simply `Service` inside its own
package:

```go
type Service struct {
    repository Repository
}
```

Use a domain-qualified implementation name only when multiple services are
used in the same package or when the implementation is part of an exported
API, for example `UserService` and `CachedUserService`.

Do not create an interface merely to wrap a concrete type. Add one when a
consumer needs substitution, testing, or a deliberate architectural
boundary.

## Repositories and persistence

Repositories own database queries and persistence-specific behavior.

Preferred names:

```go
type Repository interface { /* ... */ }
type GormRepository struct { /* ... */ }

type Service struct {
    repository Repository
}
```

Prefer `repository` over `repo` in structs, constructors, and methods that
span multiple lines. `repo` is acceptable in a short local test setup where
the meaning is unambiguous.

Use names that identify the backend when more than one persistence mechanism
exists:

- `GormRepository`
- `PostgresRepository`
- `RedisSessionStore`
- `FileSessionStore`
- `DatabaseFailedJobStore`

Avoid names such as `DBRepo`, `Impl`, or `Manager`; they describe neither the
domain nor the behavior.

## Handlers and HTTP values

Handlers translate HTTP input and output. They should remain thin.

Preferred:

```go
type Handler struct {
    service UserService
}

func (handler *Handler) List(c *gin.Context) {
    filter := web.BindFilter(c)
    result, err := handler.service.List(
        c.Request.Context(),
        web.MustDB(c),
        filter,
    )
    // ...
}
```

For very small handlers, `h` and `c` are acceptable Go idioms, but use
descriptive names when a function has several contexts, handlers, or request
objects in scope:

- `handler` instead of `h`
- `requestContext` instead of `ctx` when more than one context exists
- `ginContext` instead of `c` when multiple contexts exist
- `request` instead of `req` when multiple requests exist
- `responseWriter` instead of `w` when multiple writers exist

Do not name every Gin context `context`; that can obscure the standard
library's `context.Context`. Prefer `ginContext` in code that also handles a
request context.

## Configuration and infrastructure

Configuration and infrastructure variables often live for the entire process,
so descriptive names are preferred:

- `config` instead of `cfg`
- `database` or `databaseConnection` instead of `db` when both GORM and
  `database/sql` values are present
- `sqlDatabase` instead of `sqlDB`
- `redisClient` instead of `rdb`
- `applicationLogger` instead of `appLog` when multiple loggers exist
- `shutdownContext` instead of `ctx` during shutdown

Short forms remain acceptable in narrow, conventional scopes:

```go
func (r *GormRepository) List(ctx context.Context, db *gorm.DB, filter web.ListFilter)
```

The example is readable because the function is short and the types make
each value's role obvious. If the function grows or uses multiple databases,
rename the values.

## Errors and results

Use names that describe the failed operation:

- `validationError`
- `databaseError`
- `sessionError`
- `migrationError`
- `sendError`

Prefer `err` for the current error in a short scope. Use a descriptive name
when errors coexist:

```go
databaseError := database.Connect(...)
if databaseError != nil {
    return fmt.Errorf("database: %w", databaseError)
}

sessionError := store.Link(...)
if sessionError != nil {
    return fmt.Errorf("session: %w", sessionError)
}
```

Use `result`, `items`, `users`, or another domain-specific name rather than
`res` when a value is returned from a service and then transformed by a
handler.

## Constructors and registration

Constructor names should identify the type they create:

- `NewService`
- `NewHandler`
- `NewGormRepository`
- `NewRedisLocker`
- `NewDatabaseFailedStore`

When a package exposes multiple constructors for the same broad role, include
the implementation or purpose:

- `NewSMTPMailer`
- `NewSESMailer`
- `NewFileSessionStore`
- `NewRedisSessionStore`

Registration functions should describe what is being registered:

- `RegisterRoutes`
- `RegisterAPIRoutes`
- `RegisterGlobalMiddleware`
- `RegisterErrorMapper`
- `RegisterJobHandler`

Avoid generic names such as `Setup`, `Init`, or `Configure` when the target is
known.

## Middleware, jobs, and schedulers

Middleware names should describe the policy:

- `RequireAuth`
- `RequireRole`
- `RequestID`
- `RateLimit`
- `SecurityHeaders`

Use `middleware` for a middleware value rather than `mw` when it is stored or
passed across functions.

Job handler names should describe the action:

- `SendMail`
- `SendNotification`
- `LogHello`

Payload types should include the job domain when they are exported or used
outside one short function:

- `SendMailPayload`
- `SendNotificationPayload`
- `WelcomeNotificationPayload`

Scheduler variables should describe the lock or entry:

- `schedulerLocker`
- `scheduleEntry`
- `overlapLock`

## Tests

Test doubles should communicate both role and behavior:

- `stubUserRepository`
- `fakeUserService`
- `recordingMailSender`
- `failingSessionStore`

Avoid generic `stub`, `mock`, or `thing` names when more than one double is
present. Test-local short names such as `want`, `got`, `input`, and `testCase`
are idiomatic.

Use test names that state behavior:

```go
func TestUserServiceListReturnsPagedUsers(t *testing.T)
func TestRequireAuthRejectsRevokedSession(t *testing.T)
func TestSMTPMailerRejectsFailedStartTLS(t *testing.T)
```

## Current application guidance

The current codebase already follows several good naming patterns:

- `GormRepository`, `RedisStore`, and `FileStore` identify implementations.
- `NewService`, `NewHandler`, and `RegisterRoutes` are predictable.
- Typed names such as `TokenPair`, `RefreshDTO`, and `ListFilter` communicate
  purpose.
- `RequireAuth` and `RequireRole` describe middleware policy directly.

The main consistency improvements to apply in new or touched code are:

1. Replace service fields and parameters named `svc` with `service`.
2. Replace repository fields and parameters named `repo` with `repository`
   when they cross a function or struct boundary.
3. Use `UserService`, `NotificationService`, or another behavior-oriented
   interface at consumer boundaries.
4. Use `handler`, `requestContext`, `ginContext`, `config`, `redisClient`,
   and `sqlDatabase` when short names would be ambiguous.
5. Prefer domain-specific test doubles such as `stubUserRepository`.
6. Avoid broad mechanical renames in unrelated changes.

## Review checklist

Before merging a new feature, check:

- Does every exported identifier have a clear, domain-oriented name?
- Is the interface named for behavior rather than implementation?
- Does the consumer depend on the smallest useful interface?
- Are implementation names explicit when multiple backends exist?
- Are short names limited to small, obvious scopes?
- Would a new contributor understand the variable without opening another
  file?
- Do test doubles identify their role and failure behavior?
- Does the name follow existing package vocabulary?
