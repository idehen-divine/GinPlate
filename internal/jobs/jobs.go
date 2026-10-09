// Package jobs holds background-job handlers. Register handlers via
// queue.Handle and recurring pushes via scheduler.Schedule in init()
// (or run `ginplate make:job`).
package jobs

import (
	"context"
	"encoding/json"
	"log/slog"
	"sync"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/internal/modules/tenantmigrations"
	"github.com/idehen-divine/GinPlate/internal/modules/tenants"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/database"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/idehen-divine/GinPlate/pkg/scheduler"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
)

func init() {
	queue.Handle("log.hello", LogHello)
	queue.Handle(mail.JobName, SendMail)
	queue.Handle(notify.JobName, SendNotification)
	queue.Handle(tenantmigrations.JobMigrate, MigrateTenant)
	scheduler.Schedule(
		scheduler.New("log.hello").
			Named("hello-greeting").
			WithPayload([]byte(`{"to":"world"}`)).
			DailyAt(6, 0).
			WithoutOverlapping(),
	)
}

// SendMail delivers a mail.send job (deps resolve lazily per job).
func SendMail(ctx context.Context, job queue.Job) error {
	config, err := config.Load()
	if err != nil {
		return err
	}
	sender, err := mail.OpenSender(config.Mail, config.Filesystem.S3)
	if err != nil {
		return err
	}
	return mail.HandlerFor(sender)(ctx, job)
}

// SendNotification delivers a notification.send job (row first, then mail).
// Tenant-enveloped payloads resolve the tenant's database (and scope ctx)
// so inbox rows land in the tenant's database; raw payloads use the
// primary database as before.
func SendNotification(ctx context.Context, job queue.Job) error {
	config, err := config.Load()
	if err != nil {
		return err
	}
	payload := job.Payload
	var db *gorm.DB
	var closeDB func()
	if slug, inner, uerr := tenancy.UnwrapPayload(job.Payload); uerr == nil {
		mgr, err := cachedManager(config)
		if err != nil {
			return err
		}
		rec, err := mgr.TenantBySlug(ctx, slug)
		if err != nil {
			return err
		}
		dbh, err := mgr.DBForTenant(ctx, rec)
		if err != nil {
			return err
		}
		ctx = tenancy.WithTenantID(ctx, rec.ID)
		payload = inner
		db = dbh // managed handle: shared, never closed per job
	} else {
		dbh, err := database.Connect(config.Database.Driver, config.Database.DSN())
		if err != nil {
			return err
		}
		if sqlDB, err := dbh.DB(); err == nil {
			closeDB = func() { _ = sqlDB.Close() }
		}
		db = dbh
	}
	if closeDB != nil {
		defer closeDB()
	}
	sender, err := mail.OpenSender(config.Mail, config.Filesystem.S3)
	if err != nil {
		return err
	}
	return notify.HandlerFor(notify.Deps{DB: db, Sender: sender})(ctx, queue.Job{
		ID: job.ID, Name: job.Name, Payload: payload,
		Attempts: job.Attempts, AvailableAt: job.AvailableAt,
	})
}

// tenantManager caches one DBManager per control DSN so tenant:migrate
// jobs share handles instead of reconnecting the control database per job.
var tenantManager = struct {
	sync.Mutex
	mgr *tenancy.DBManager
	dsn string
}{}

// MigrateTenant advances one shared-to-dedicated move ledger row. The job
// payload carries only the migration id; tenant and placement resolve from
// the control database at execution time.
func MigrateTenant(ctx context.Context, job queue.Job) error {
	var payload struct {
		MigrationID string `json:"migration_id"`
	}
	if err := json.Unmarshal(job.Payload, &payload); err != nil {
		return err
	}
	id, err := uuid.Parse(payload.MigrationID)
	if err != nil {
		return err
	}
	config, err := config.Load()
	if err != nil {
		return err
	}
	mgr, err := cachedManager(config)
	if err != nil {
		return err
	}
	migrator := tenantmigrations.NewTenantMigrations(mgr, mgr.Control(), tenants.GormRepository{}, tenants.GormRepository{}, tenantmigrations.TenantMigrationsOpts{
		Driver:     config.Database.Driver,
		ChunkSize:  config.Tenancy.ChunkSize,
		RetainDays: config.Tenancy.RetainDays,
		AppUser:    config.Tenancy.AppUser,
		AppPass:    config.Tenancy.AppPass,
		Logf:       slog.Info,
	})
	return migrator.HandleMigrateTask(ctx, id)
}

func cachedManager(config *config.Config) (*tenancy.DBManager, error) {
	control := config.Tenancy.ControlDSN
	if control == "" {
		control = config.Database.DSN()
	}
	tenantManager.Lock()
	defer tenantManager.Unlock()
	if tenantManager.mgr != nil && tenantManager.dsn == control {
		return tenantManager.mgr, nil
	}
	mgr, err := tenancy.NewDBManager(tenancy.ManagerParams{
		Driver:      config.Database.Driver,
		ControlDSN:  config.Tenancy.ControlDSN,
		Pools:       tenancy.ParsePoolDSNs(config.Tenancy.PoolDSNs),
		DefaultPool: config.Tenancy.DefaultPool,
		DSNTemplate: config.Tenancy.DSNTemplate,
		AppUser:     config.Tenancy.AppUser,
		AppPass:     config.Tenancy.AppPass,
	}, config.Database.DSN())
	if err != nil {
		return nil, err
	}
	tenantManager.mgr = mgr
	tenantManager.dsn = control
	return mgr, nil
}

// LogHello is the example handler.
func LogHello(ctx context.Context, job queue.Job) error {
	var payload struct {
		To string `json:"to"`
	}
	if err := json.Unmarshal(scheduler.Data(job.Payload), &payload); err != nil {
		return err
	}
	slog.Info("hello job", "id", job.ID, "to", payload.To)
	return nil
}
