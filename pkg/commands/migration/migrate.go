package migration

import (
	"fmt"
	"log"

	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
)

// domain is the goose migration domain for the app schema
// (migrations/app/{mysql,pgsql}).
const domain = "app"

// db binds driver, DSN, and domain once per command so RunE bodies stay
// one-liners.
func db(cfg *config.Config) database.DB {
	return database.For(cfg.Database.Driver, cfg.Database.DSN(), domain)
}

// NewMigrateCmd groups database migration subcommands (powered by goose).
func NewMigrateCmd(cfg *config.Config) *cobra.Command {
	migrate := &cobra.Command{Use: "migrate", Short: "Database migrations"}
	migrate.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Create the database (if missing) + run pending migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			d := db(cfg)
			if err := d.CreateDB(); err != nil {
				return fmt.Errorf("create db: %w", err)
			}
			if err := d.Up(); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			fmt.Println("migrated")
			return nil
		},
	})
	migrate.AddCommand(&cobra.Command{
		Use:   "status",
		Short: "Show applied/pending migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			log.Println("== app ==")
			if err := db(cfg).Status(); err != nil {
				return fmt.Errorf("status: %w", err)
			}
			return nil
		},
	})
	migrate.AddCommand(&cobra.Command{
		Use:   "rollback",
		Short: "Revert the last applied migration",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := db(cfg).RollbackLast(); err != nil {
				return fmt.Errorf("rollback: %w", err)
			}
			fmt.Println("rolled back")
			return nil
		},
	})
	migrate.AddCommand(&cobra.Command{
		Use:   "reset",
		Short: "Revert all applied migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := db(cfg).RollbackAll(); err != nil {
				return fmt.Errorf("reset: %w", err)
			}
			fmt.Println("reset")
			return nil
		},
	})
	migrate.AddCommand(&cobra.Command{
		Use:   "refresh",
		Short: "Revert all migrations, then re-apply them",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := db(cfg).Refresh(); err != nil {
				return fmt.Errorf("refresh: %w", err)
			}
			fmt.Println("refreshed")
			return nil
		},
	})
	migrate.AddCommand(&cobra.Command{
		Use:   "fresh",
		Short: "Drop every table, then migrate from scratch (DESTRUCTIVE)",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := db(cfg).Fresh(); err != nil {
				return fmt.Errorf("fresh: %w", err)
			}
			fmt.Println("fresh")
			return nil
		},
	})
	return migrate
}
