package schedule

import (
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/app"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// NewScheduleWorkCmd runs the scheduler (ginplate schedule:work). It ticks
// every minute and pushes due entries (registered via scheduler.Schedule in
// internal/jobs) onto the queue broker for queue:work processes to run.
func NewScheduleWorkCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "schedule:work",
		Short: "Run the scheduler (push due entries to the queue)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.RunScheduler(cfg)
		},
	}
}
