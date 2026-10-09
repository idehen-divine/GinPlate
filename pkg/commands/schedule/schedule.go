package schedule

import (
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/app"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// NewScheduleWorkCmd runs the scheduler: pushes due entries onto the queue.
func NewScheduleWorkCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "schedule:work",
		Short: "Run the scheduler (push due entries to the queue)",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.RunScheduler(config)
		},
	}
}
