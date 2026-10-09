package work

import (
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/app"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// NewWorkCmd runs the background worker: dispatches jobs until interrupted.
func NewWorkCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "queue:work",
		Short: "Run the background worker",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.RunWorker(config)
		},
	}
}
