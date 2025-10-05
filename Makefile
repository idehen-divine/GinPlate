.PHONY: help commands run run-dev worker scheduler queue-failed queue-retry queue-forget queue-flush keygen mail-test down up migrate-up migrate-status migrate-rollback migrate-reset migrate-refresh migrate-fresh make-migration make-command make-job make-mail make-notification sync-commands docs swagger test test-coverage fmt vet lint check tidy build build-prod install deps

help:
	@echo "Available targets:"
	@echo "  run             - Run the API (ginplate serve)"
	@echo "  run-dev         - Run the API in debug mode"
	@echo "  worker          - Run the background worker (ginplate queue:work)"
	@echo "  scheduler       - Push due schedules to the queue (ginplate schedule:work)"
	@echo "  queue-failed    - List buried jobs (ginplate queue:failed)"
	@echo "  queue-retry     - Re-queue a buried job: ID=<id|all>"
	@echo "  queue-forget    - Delete a buried job: ID=<id>"
	@echo "  queue-flush     - Delete all buried jobs (DESTRUCTIVE)"
	@echo "  migrate-up      - Create the database + run pending migrations"
	@echo "  migrate-status  - Show applied/pending migrations"
	@echo "  migrate-rollback- Revert the last applied migration"
	@echo "  migrate-reset   - Revert all applied migrations"
	@echo "  migrate-refresh - Revert all, then re-apply (dev/test only)"
	@echo "  migrate-fresh   - Drop every table, migrate from scratch (DESTRUCTIVE)"
	@echo "  make-migration  - Scaffold a migration: NAME=CreatePostsTable [CREATE=posts]"
	@echo "  make-command    - Scaffold a command: NAME=SendReport"
	@echo "  make-job        - Scaffold a job: NAME=Billing.Charge [SCHEDULE=daily@02:00]"
	@echo "  make-mail       - Scaffold a mailable: NAME=OrderShipped"
	@echo "  make-notification - Scaffold a notification: NAME=OrderShipped"
	@echo "  keygen          - Generate APP_KEY into .env"
	@echo "  mail-test       - Send a test email: TO=a@b.c [SUBJECT=..] [QUEUE=1]"
	@echo "  down            - Maintenance mode on (503s, --secret/--retry/--message)"
	@echo "  up              - Maintenance mode off"
	@echo "  sync-commands   - Refresh generated imports after deleting a command dir"
	@echo "  commands        - List the ginplate CLI commands"
	@echo "  docs swagger    - Regenerate Swagger docs into ./docs"
	@echo "  build           - Build the binary into ./bin"
	@echo "  build-prod      - Optimized production build into ./bin"
	@echo "  install         - Install the ginplate binary onto PATH"
	@echo "  test            - Run tests"
	@echo "  test-coverage   - Tests + HTML coverage report"
	@echo "  fmt vet lint    - Format, vet, lint"
	@echo "  check           - fmt + vet + test"
	@echo "  deps tidy       - Download deps / tidy go.mod"

commands:
	go run ./cmd/ginplate --help

run:
	go run ./cmd/ginplate serve

run-dev:
	GIN_MODE=debug go run ./cmd/ginplate serve

worker:
	go run ./cmd/ginplate queue:work

scheduler:
	go run ./cmd/ginplate schedule:work

queue-failed:
	go run ./cmd/ginplate queue:failed

queue-retry:
	go run ./cmd/ginplate queue:retry $(ID)

queue-forget:
	go run ./cmd/ginplate queue:forget $(ID)

queue-flush:
	go run ./cmd/ginplate queue:flush --force

migrate-up:
	go run ./cmd/ginplate migrate up

migrate-status:
	go run ./cmd/ginplate migrate status

migrate-rollback:
	go run ./cmd/ginplate migrate rollback

migrate-reset:
	go run ./cmd/ginplate migrate reset

migrate-refresh:
	go run ./cmd/ginplate migrate refresh

migrate-fresh:
	go run ./cmd/ginplate migrate fresh

make-migration:
	go run ./cmd/ginplate make:migration $(NAME) $(if $(CREATE),--create $(CREATE))

make-command:
	go run ./cmd/ginplate make:command $(NAME)

make-job:
	go run ./cmd/ginplate make:job $(NAME) $(if $(SCHEDULE),--schedule=$(SCHEDULE))

make-mail:
	go run ./cmd/ginplate make:mail $(NAME)

make-notification:
	go run ./cmd/ginplate make:notification $(NAME)

keygen:
	go run ./cmd/ginplate key:generate

mail-test:
	go run ./cmd/ginplate mail:test --to $(TO) $(if $(SUBJECT),--subject "$(SUBJECT)") $(if $(QUEUE),--queue)

down:
	go run ./cmd/ginplate down $(if $(SECRET),--secret $(SECRET)) $(if $(RETRY),--retry $(RETRY)) $(if $(MESSAGE),--message "$(MESSAGE)")

up:
	go run ./cmd/ginplate up

# Refresh the generated import list after deleting a command directory.
# generated.go is removed first because its stale imports would otherwise
# stop the tool itself from compiling; removing it is always safe.
sync-commands:
	rm -f pkg/commands/generated.go
	go run ./cmd/ginplate make:command --sync

docs swagger:
	go run github.com/swaggo/swag/cmd/swag init -g cmd/ginplate/main.go -o docs

build:
	mkdir -p bin
	go build -o bin/ginplate ./cmd/ginplate

build-prod:
	mkdir -p bin
	CGO_ENABLED=0 go build -ldflags="-s -w" -o bin/ginplate ./cmd/ginplate

install:
	go install ./cmd/ginplate

test:
	go test ./...

test-coverage:
	go test -cover -coverprofile=coverage.out ./...
	go tool cover -html=coverage.out -o coverage.html

fmt:
	go fmt ./...

vet:
	go vet ./...

lint:
	golangci-lint run

check: fmt vet test

deps:
	go mod download
	go mod tidy

tidy:
	go mod tidy
