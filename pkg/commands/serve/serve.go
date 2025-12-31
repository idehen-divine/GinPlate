package serve

import (
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/app"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// NewServeCmd runs the API server (ginplate serve).
func NewServeCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "serve",
		Short: "Run the API server",
		RunE: func(_ *cobra.Command, _ []string) error {
			return app.RunAPI(cfg)
		},
	}
}
