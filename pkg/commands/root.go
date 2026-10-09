// Package commands assembles the cobra command tree: one folder per command
// group with a constructor. Custom stubs (internal/commands) self-register
// via init(); see package custom (internal/commands/registry.go).
package commands

import (
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/internal/commands"
	"github.com/idehen-divine/GinPlate/pkg/commands/key"
	mailcmd "github.com/idehen-divine/GinPlate/pkg/commands/mail"
	"github.com/idehen-divine/GinPlate/pkg/commands/maintenance"
	"github.com/idehen-divine/GinPlate/pkg/commands/makecommand"
	"github.com/idehen-divine/GinPlate/pkg/commands/makeexception"
	"github.com/idehen-divine/GinPlate/pkg/commands/makejob"
	"github.com/idehen-divine/GinPlate/pkg/commands/makemail"
	"github.com/idehen-divine/GinPlate/pkg/commands/makemiddleware"
	"github.com/idehen-divine/GinPlate/pkg/commands/makenotification"
	"github.com/idehen-divine/GinPlate/pkg/commands/migration"
	"github.com/idehen-divine/GinPlate/pkg/commands/queuefailed"
	"github.com/idehen-divine/GinPlate/pkg/commands/route"
	"github.com/idehen-divine/GinPlate/pkg/commands/schedule"
	"github.com/idehen-divine/GinPlate/pkg/commands/serve"
	"github.com/idehen-divine/GinPlate/pkg/commands/work"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// NewRoot assembles the full command tree plus self-registered customs.
func NewRoot(config *config.Config) *cobra.Command {
	root := &cobra.Command{
		Use:   "ginplate",
		Short: "GinPlate single-database boilerplate",
		Long:  "Unified CLI for the GinPlate API, migrations, custom commands, and generators.",
	}
	root.AddCommand(serve.NewServeCmd(config))
	root.AddCommand(work.NewWorkCmd(config))
	root.AddCommand(schedule.NewScheduleWorkCmd(config))
	root.AddCommand(queuefailed.NewQueueFailedCmd(config))
	root.AddCommand(queuefailed.NewQueueRetryCmd(config))
	root.AddCommand(queuefailed.NewQueueForgetCmd(config))
	root.AddCommand(queuefailed.NewQueueFlushCmd(config))
	root.AddCommand(migration.NewMigrateCmd(config))
	root.AddCommand(makecommand.NewMakeCommandCmd())
	root.AddCommand(makeexception.NewMakeExceptionCmd())
	root.AddCommand(makejob.NewMakeJobCmd())
	root.AddCommand(makemail.NewMakeMailCmd())
	root.AddCommand(makemiddleware.NewMakeMiddlewareCmd())
	root.AddCommand(makenotification.NewMakeNotificationCmd())
	root.AddCommand(migration.NewMakeMigrationCmd())
	root.AddCommand(maintenance.NewDownCmd(config))
	root.AddCommand(maintenance.NewUpCmd(config))
	root.AddCommand(key.NewKeyGenerateCmd())
	root.AddCommand(mailcmd.NewMailTestCmd(config))
	root.AddCommand(route.NewRouteListCmd(config))
	for _, cmd := range custom.Registered() {
		root.AddCommand(cmd)
	}
	return root
}
