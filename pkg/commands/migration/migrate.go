package migration

import (
	"context"
	"fmt"
	"log"
	"strings"

	"github.com/google/uuid"
	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/internal/modules/cadmin"
	"github.com/idehen-divine/GinPlate/internal/modules/tenantmigrations"
	"github.com/idehen-divine/GinPlate/internal/modules/tenants"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
)

// domain is the goose migration domain (migrations/app/{mysql,pgsql}):
// tenant data plus tenants, domains, ledger, and control admins.
const domain = "app"

// tenantDomain is the goose migration domain for dedicated tenant
// databases (migrations/tenants/{mysql,pgsql}): tenant data only, no
// control-plane tables.
const tenantDomain = "tenants"

// openDatabase binds driver, DSN, and domain once per command.
func openDatabase(config *config.Config) database.DB {
	return database.For(config.Database.Driver, config.Database.DSN(), domain)
}

// controlDSN resolves the control database DSN, falling back to the
// primary DSN for single-database topologies.
func controlDSN(config *config.Config) string {
	if s := strings.TrimSpace(config.Tenancy.ControlDSN); s != "" {
		return s
	}
	return config.Database.DSN()
}

// poolDSN resolves a shared-pool DSN by name, falling back to the primary
// DSN for the default pool in single-database topologies.
func poolDSN(config *config.Config, name string) (string, error) {
	pools := tenancy.ParsePoolDSNs(config.Tenancy.PoolDSNs)
	if dsn, ok := pools[name]; ok && strings.TrimSpace(dsn) != "" {
		return dsn, nil
	}
	def := strings.TrimSpace(config.Tenancy.DefaultPool)
	if def == "" {
		def = "shared_1"
	}
	if name == def {
		return config.Database.DSN(), nil
	}
	return "", fmt.Errorf("unknown pool %q", name)
}

// tenantDSN resolves the database DSN for a tenant slug via its placement.
func tenantDSN(config *config.Config, slug string) (string, error) {
	ctrl, err := database.Connect(config.Database.Driver, controlDSN(config))
	if err != nil {
		return "", fmt.Errorf("control database: %w", err)
	}
	sqlDB, err := ctrl.DB()
	if err != nil {
		return "", err
	}
	defer sqlDB.Close()
	var rec tenancy.TenantRecord
	ctx := tenancy.WithoutTenantScope(context.Background())
	if err := ctrl.WithContext(ctx).Where("slug = ?", slug).First(&rec).Error; err != nil {
		return "", fmt.Errorf("tenant %q: %w", slug, err)
	}
	kind, ref, err := tenancy.TenantTarget(rec.Placement, rec.Pool, rec.Slug, config.Tenancy.DefaultPool, config.Tenancy.DSNTemplate)
	if err != nil {
		return "", err
	}
	if kind == "pool" {
		return poolDSN(config, ref)
	}
	return ref, nil
}

// NewMigrateCmd groups database migration subcommands.
func NewMigrateCmd(config *config.Config) *cobra.Command {
	migrate := &cobra.Command{Use: "migrate", Short: "Database migrations"}
	migrate.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Create the database (if missing) + run pending migrations",
		RunE: func(_ *cobra.Command, _ []string) error {
			d := openDatabase(config)
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
			if err := openDatabase(config).Status(); err != nil {
				return fmt.Errorf("status: %w", err)
			}
			return nil
		},
	})
	migrate.AddCommand(&cobra.Command{
		Use:   "rollback",
		Short: "Revert the last applied migration",
		RunE: func(_ *cobra.Command, _ []string) error {
			if err := openDatabase(config).RollbackLast(); err != nil {
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
			if err := openDatabase(config).RollbackAll(); err != nil {
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
			if err := openDatabase(config).Refresh(); err != nil {
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
			if err := openDatabase(config).Fresh(); err != nil {
				return fmt.Errorf("fresh: %w", err)
			}
			fmt.Println("fresh")
			return nil
		},
	})
	migrate.AddCommand(poolCmd(config))
	migrate.AddCommand(provisionCmd(config))
	migrate.AddCommand(tenantCmd(config))
	migrate.AddCommand(seedAdminCmd(config))
	migrate.AddCommand(tenantMigrateCmd(config))
	migrate.AddCommand(tenantCleanupCmd(config))
	migrate.AddCommand(tenantRollbackCmd(config))
	migrate.AddCommand(tenantStatusCmd(config))
	return migrate
}

// tenantManagerForCLI builds a DBManager from config for move commands.
func tenantManagerForCLI(config *config.Config) (*tenancy.DBManager, error) {
	return tenancy.NewDBManager(tenancy.ManagerParams{
		Driver:      config.Database.Driver,
		ControlDSN:  controlDSN(config),
		Pools:       tenancy.ParsePoolDSNs(config.Tenancy.PoolDSNs),
		DefaultPool: config.Tenancy.DefaultPool,
		DSNTemplate: config.Tenancy.DSNTemplate,
		AppUser:     config.Tenancy.AppUser,
		AppPass:     config.Tenancy.AppPass,
	}, config.Database.DSN())
}

// tenantMigratorForCLI wires a Migrator over the control database.
func tenantMigratorForCLI(config *config.Config) (*tenantmigrations.TenantMigrations, *tenancy.DBManager, error) {
	mgr, err := tenantManagerForCLI(config)
	if err != nil {
		return nil, nil, err
	}
	return tenantmigrations.NewTenantMigrations(mgr, mgr.Control(), tenants.GormRepository{}, tenants.GormRepository{}, tenantmigrations.TenantMigrationsOpts{
		Driver:     config.Database.Driver,
		ChunkSize:  config.Tenancy.ChunkSize,
		RetainDays: config.Tenancy.RetainDays,
		AppUser:    config.Tenancy.AppUser,
		AppPass:    config.Tenancy.AppPass,
		Logf:       log.Printf,
	}), mgr, nil
}

// tenantMigrateCmd requests a shared-to-dedicated move and runs it inline.
func tenantMigrateCmd(config *config.Config) *cobra.Command {
	var reason string
	cmd := &cobra.Command{
		Use:   "tenant-migrate <slug>",
		Short: "Move a tenant shared -> dedicated (request + run inline)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			migrator, mgr, err := tenantMigratorForCLI(config)
			if err != nil {
				return err
			}
			defer mgr.Close()
			ctx := tenancy.WithoutTenantScope(context.Background())
			mig, err := migrator.RequestMigration(ctx, args[0], reason)
			if err != nil {
				return err
			}
			if err := migrator.HandleMigrateTask(ctx, mig.ID); err != nil {
				return fmt.Errorf("migrate: %w", err)
			}
			fmt.Println("migrated to retaining:", mig.ID)
			return nil
		},
	}
	cmd.Flags().StringVar(&reason, "reason", "", "Move reason for the ledger")
	return cmd
}

// tenantCleanupCmd drops retained pool rows after the retention window.
func tenantCleanupCmd(config *config.Config) *cobra.Command {
	var force bool
	cmd := &cobra.Command{
		Use:   "tenant-cleanup <slug>",
		Short: "Drop retained pool rows after a move (respects retention unless --force)",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			migrator, mgr, err := tenantMigratorForCLI(config)
			if err != nil {
				return err
			}
			defer mgr.Close()
			if err := migrator.Cleanup(tenancy.WithoutTenantScope(context.Background()), args[0], force); err != nil {
				return err
			}
			fmt.Println("cleanup done")
			return nil
		},
	}
	cmd.Flags().BoolVar(&force, "force", false, "Skip the retention window")
	return cmd
}

// tenantRollbackCmd aborts a non-terminal move and restores shared/active.
func tenantRollbackCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "tenant-rollback <migration-id>",
		Short: "Roll back a non-terminal tenant migration",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			migrator, mgr, err := tenantMigratorForCLI(config)
			if err != nil {
				return err
			}
			defer mgr.Close()
			id, err := uuid.Parse(args[0])
			if err != nil {
				return fmt.Errorf("invalid migration id: %w", err)
			}
			if err := migrator.Rollback(tenancy.WithoutTenantScope(context.Background()), id); err != nil {
				return err
			}
			fmt.Println("rolled back")
			return nil
		},
	}
}

// tenantStatusCmd prints the newest move ledger row for a tenant.
func tenantStatusCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "tenant-status <slug>",
		Short: "Show the newest migration ledger row for a tenant",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			mgr, err := tenantManagerForCLI(config)
			if err != nil {
				return err
			}
			defer mgr.Close()
			ctx := tenancy.WithoutTenantScope(context.Background())
			svc := tenants.NewTenants(mgr.Control(), tenants.GormRepository{}, tenants.GormRepository{}, nil, config.Tenancy.DefaultPool)
			m, err := svc.LatestMigration(ctx, args[0])
			if err != nil {
				return err
			}
			if m == nil {
				fmt.Println("no migration")
				return nil
			}
			fmt.Printf("id=%s phase=%s attempts=%d error=%s\n", m.ID, m.Phase, m.Attempts, m.Error)
			return nil
		},
	}
}

// poolCmd migrates the tenant-data schema onto a shared pool.
func poolCmd(config *config.Config) *cobra.Command {
	var pool string
	cmd := &cobra.Command{Use: "pool", Short: "Tenant-data schema on a shared pool"}
	cmd.PersistentFlags().StringVar(&pool, "pool", "", "Pool name (default: DEFAULT_POOL)")
	cmd.AddCommand(&cobra.Command{
		Use:   "up",
		Short: "Run pending app migrations on the pool",
		RunE: func(_ *cobra.Command, _ []string) error {
			name := strings.TrimSpace(pool)
			if name == "" {
				name = strings.TrimSpace(config.Tenancy.DefaultPool)
			}
			if name == "" {
				name = "shared_1"
			}
			dsn, err := poolDSN(config, name)
			if err != nil {
				return err
			}
			if err := database.For(config.Database.Driver, dsn, tenantDomain).Up(); err != nil {
				return fmt.Errorf("migrate pool %q: %w", name, err)
			}
			fmt.Println("pool migrated")
			return nil
		},
	})
	return cmd
}

// provisionCmd creates a dedicated tenant database and migrates it.
func provisionCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "provision <slug>",
		Short: "Create a dedicated tenant database + run tenant migrations",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			slug := strings.ToLower(strings.TrimSpace(args[0]))
			if !tenancy.IsValidSlug(slug) {
				return fmt.Errorf("invalid tenant slug %q", slug)
			}
			tpl := strings.TrimSpace(config.Tenancy.DSNTemplate)
			if tpl == "" {
				return fmt.Errorf("TENANT_DSN_TEMPLATE is empty: dedicated placement unsupported")
			}
			dsn := fmt.Sprintf(tpl, slug)
			d := database.For(config.Database.Driver, dsn, tenantDomain)
			if err := d.CreateDB(); err != nil {
				return fmt.Errorf("create tenant db: %w", err)
			}
			if err := d.Up(); err != nil {
				return fmt.Errorf("migrate tenant db: %w", err)
			}
			fmt.Println("provisioned")
			return nil
		},
	}
}

// tenantCmd runs tenant migrations on one tenant's database (pool or dedicated).
func tenantCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "tenant <slug>",
		Short: "Run pending tenant migrations on the tenant's database",
		Args:  cobra.ExactArgs(1),
		RunE: func(_ *cobra.Command, args []string) error {
			slug := strings.ToLower(strings.TrimSpace(args[0]))
			dsn, err := tenantDSN(config, slug)
			if err != nil {
				return err
			}
			if err := database.For(config.Database.Driver, dsn, tenantDomain).Up(); err != nil {
				return fmt.Errorf("migrate tenant %q: %w", slug, err)
			}
			fmt.Println("tenant migrated")
			return nil
		},
	}
}

// seedAdminCmd bootstraps a control super_admin (idempotent by email).
func seedAdminCmd(config *config.Config) *cobra.Command {
	return &cobra.Command{
		Use:   "seed-admin <name> <email> <password>",
		Short: "Create a control super_admin (reruns are no-ops)",
		Args:  cobra.ExactArgs(3),
		RunE: func(_ *cobra.Command, args []string) error {
			ctrl, err := database.Connect(config.Database.Driver, controlDSN(config))
			if err != nil {
				return fmt.Errorf("control database: %w", err)
			}
			sqlDB, err := ctrl.DB()
			if err != nil {
				return err
			}
			defer sqlDB.Close()
			ctx := tenancy.WithoutTenantScope(context.Background())
			a, created, err := cadmin.SeedAdmin(ctrl.WithContext(ctx), args[0], args[1], args[2])
			if err != nil {
				return fmt.Errorf("seed admin: %w", err)
			}
			if created {
				fmt.Println("admin created:", a.Email)
			} else {
				fmt.Println("admin exists:", a.Email)
			}
			return nil
		},
	}
}
