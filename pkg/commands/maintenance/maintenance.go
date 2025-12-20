package maintenance

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// maintenancePath resolves the marker path and driver, tolerating a nil
// config: down/up are config-free commands (they must run even when config
// is broken), so compiled-in defaults apply. With no config, raw
// environment variables still win over those defaults.
func maintenancePath(cfg *config.Config) (path, driver string) {
	path, driver = "storage/framework/down", "file"
	if cfg == nil {
		if p := os.Getenv("APP_MAINTENANCE_PATH"); p != "" {
			path = p
		}
		if d := os.Getenv("APP_MAINTENANCE_DRIVER"); d != "" {
			driver = d
		}
		return path, driver
	}
	if cfg.App.Maintenance.Path != "" {
		path = cfg.App.Maintenance.Path
	}
	if cfg.App.Maintenance.Driver != "" {
		driver = cfg.App.Maintenance.Driver
	}
	return path, driver
}

// NewDownCmd builds `ginplate down`: writes the maintenance marker so the
// API 503s every request until `ginplate up`. Only the file driver is
// supported; anything else in APP_MAINTENANCE_DRIVER is refused.
func NewDownCmd(cfg *config.Config) *cobra.Command {
	var secret, message string
	var retry int
	cmd := &cobra.Command{
		Use:   "down",
		Short: "Put the API into maintenance mode",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, driver := maintenancePath(cfg)
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
	cmd.Flags().StringVar(&secret, "secret", "", "Bypass secret (?secret= / X-Maintenance-Bypass header)")
	cmd.Flags().IntVar(&retry, "retry", 60, "Retry-After seconds sent with 503s")
	cmd.Flags().StringVar(&message, "message", "", "Custom 503 message (default built-in)")
	return cmd
}

// NewUpCmd builds `ginplate up`: removes the maintenance marker.
func NewUpCmd(cfg *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "up",
		Short: "Take the API out of maintenance mode",
		RunE: func(cmd *cobra.Command, _ []string) error {
			path, _ := maintenancePath(cfg)
			if err := web.ClearDownFile(path); err != nil {
				return err
			}
			cmd.Println("back up")
			return nil
		},
	}
}
