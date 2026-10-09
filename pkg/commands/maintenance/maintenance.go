package maintenance

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// maintenancePath resolves the marker path and driver (nil config tolerated:
// down/up must run even when config is broken).
func maintenancePath(config *config.Config) (path, driver string) {
	path, driver = "storage/framework/down", "file"
	if config == nil {
		if p := os.Getenv("APP_MAINTENANCE_PATH"); p != "" {
			path = p
		}
		if d := os.Getenv("APP_MAINTENANCE_DRIVER"); d != "" {
			driver = d
		}
		return path, driver
	}
	if config.App.Maintenance.Path != "" {
		path = config.App.Maintenance.Path
	}
	if config.App.Maintenance.Driver != "" {
		driver = config.App.Maintenance.Driver
	}
	return path, driver
}

// NewDownCmd builds `ginplate down`: API 503s every request until `ginplate up`.
func NewDownCmd(config *config.Config) *cobra.Command {
	var secret, message string
	var retry int
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Put the API into maintenance mode",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, driver := maintenancePath(config)
			if driver != "file" {
				return fmt.Errorf("only the file maintenance driver is supported, got %q", driver)
			}
			if err := web.WriteDownFile(path, web.DownState{
				Secret: secret, Retry: retry, Message: message,
			}); err != nil {
				return err
			}
			cmd.Printf("maintenance mode on (%s)\n", path)
			return nil
		},
	}
	cmd.Flags().StringVar(&secret, "secret", "", "Bypass secret (X-Maintenance-Bypass header)")
	cmd.Flags().IntVar(&retry, "retry", 60, "Retry-After seconds sent with 503s")
	cmd.Flags().StringVar(&message, "message", "", "Custom 503 message (default built-in)")
	return cmd
}

// NewUpCmd builds `ginplate up`: clears the maintenance marker.
func NewUpCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Take the API out of maintenance mode",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := maintenancePath(config)
			if err := web.ClearDownFile(path); err != nil {
				return err
			}
			cmd.Println("back up")
			return nil
		},
	}
}
