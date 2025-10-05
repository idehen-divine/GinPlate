// Package commands assembles the cobra command tree for the ginplate
// binary. Each command group lives in its own folder with a constructor:
//
//	serve/       - NewServeCmd: run the API server
//	work/        - NewWorkCmd: run the background worker
//	schedule/    - NewScheduleWorkCmd: push due schedule entries to the queue
//	queuefailed/ - NewQueueFailedCmd + retry/forget/flush: buried-job tooling
//	migration/   - NewMigrateCmd: database migrations + NewMakeMigrationCmd generator
//	makenotification/ - NewMakeNotificationCmd: `make:notification` generator
//	makecommand/ - NewMakeCommandCmd: `make:command` stub generator
//	makemail/    - NewMakeMailCmd: `make:mail` mailable generator
//	makejob/     - NewMakeJobCmd: `make:job` job-handler generator
//	maintenance/ - NewDownCmd/NewUpCmd: maintenance mode
//	key/         - NewKeyGenerateCmd: APP_KEY generator
//
// Clonable one-off commands (make:command stubs) live in
// internal/commands and self-register via init(), attaching below with no
// manual wiring. See package custom (internal/commands/registry.go).
//
// It depends only on cobra and project packages — never on main packages —
// so anyone cloning the repo can reuse it: import this package, call NewRoot,
// execute.
package commands

import (
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/internal/commands"
	"github.com/idehen-divine/GinPlate/pkg/commands/key"
	mailcmd "github.com/idehen-divine/GinPlate/pkg/commands/mail"
	"github.com/idehen-divine/GinPlate/pkg/commands/maintenance"
	"github.com/idehen-divine/GinPlate/pkg/commands/makecommand"
	"github.com/idehen-divine/GinPlate/pkg/commands/makejob"
	"github.com/idehen-divine/GinPlate/pkg/commands/makemail"
	"github.com/idehen-divine/GinPlate/pkg/commands/makenotification"
	"github.com/idehen-divine/GinPlate/pkg/commands/migration"
	"github.com/idehen-divine/GinPlate/pkg/commands/queuefailed"
	"github.com/idehen-divine/GinPlate/pkg/commands/schedule"
	"github.com/idehen-divine/GinPlate/pkg/commands/serve"
	"github.com/idehen-divine/GinPlate/pkg/commands/work"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// NewRoot assembles the full command tree: serve, migrate, generators,
// maintenance, key management, plus every self-registered custom command.
func NewRoot(cfg *config.Config) *cobra.Command {
	root := &cobra.Command{
		Use:   "ginplate",
		Short: "GinPlate single-database boilerplate",
		Long:  "Unified CLI for the GinPlate API, migrations, custom commands, and generators.",
	}
	root.AddCommand(serve.NewServeCmd(cfg))
	root.AddCommand(work.NewWorkCmd(cfg))
	root.AddCommand(schedule.NewScheduleWorkCmd(cfg))
	root.AddCommand(queuefailed.NewQueueFailedCmd(cfg))
	root.AddCommand(queuefailed.NewQueueRetryCmd(cfg))
	root.AddCommand(queuefailed.NewQueueForgetCmd(cfg))
	root.AddCommand(queuefailed.NewQueueFlushCmd(cfg))
	root.AddCommand(migration.NewMigrateCmd(cfg))
	root.AddCommand(makecommand.NewMakeCommandCmd())
	root.AddCommand(makejob.NewMakeJobCmd())
	root.AddCommand(makemail.NewMakeMailCmd())
	root.AddCommand(makenotification.NewMakeNotificationCmd())
	root.AddCommand(migration.NewMakeMigrationCmd())
	root.AddCommand(maintenance.NewDownCmd(cfg))
	root.AddCommand(maintenance.NewUpCmd(cfg))
	root.AddCommand(key.NewKeyGenerateCmd())
	root.AddCommand(mailcmd.NewMailTestCmd(cfg))
	for _, cmd := range custom.Registered() {
		root.AddCommand(cmd)
	}
	return root
}
