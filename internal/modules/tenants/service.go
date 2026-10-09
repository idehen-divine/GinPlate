package tenants

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/internal/modules/tenantmigrations"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Tenants is the tenant lifecycle over the control database: create
// shared-first, list/get, suspend/resume, remove. Placement moves (shared
// to dedicated) run through TenantMigrations, never by direct edits.
type Tenants struct {
	db       *gorm.DB
	tenants  TenantsRepository
	moves    TenantMigrationsRepository
	mgr      *tenancy.DBManager
	defPool  string
	notifier *notify.Notifier
}

// NewTenants wires the control handle, narrow repositories, placement
// routing for removal purges, and the default pool. Nil repositories fall
// back to the shared GORM implementation.
func NewTenants(db *gorm.DB, tenants TenantsRepository, moves TenantMigrationsRepository, mgr *tenancy.DBManager, defaultPool string) *Tenants {
	if tenants == nil {
		tenants = NewGormRepository()
	}
	if moves == nil {
		moves = NewGormRepository()
	}
	if defaultPool == "" {
		defaultPool = "shared_1"
	}
	return &Tenants{db: db, tenants: tenants, moves: moves, mgr: mgr, defPool: defaultPool}
}

// WithNotifier attaches control-admin alert delivery (best-effort, never
// fatal to tenant operations).
func (s *Tenants) WithNotifier(n *notify.Notifier) *Tenants {
	s.notifier = n
	return s
}

// adminAudience is the shared control-admin alert inbox: every super_admin
// reads the same operational alerts.
func adminAudience() notify.Notifiable {
	return notify.Notifiable{Type: "control_admin", ID: "broadcast"}
}

// adminAlert is a database-channel lifecycle event for control admins.
type adminAlert struct {
	event  string
	slug   string
	detail string
}

func (a adminAlert) Type() string                     { return "tenant." + a.event }
func (a adminAlert) Via() []string                    { return []string{notify.ChannelDatabase} }
func (a adminAlert) ToMail() (pkgmail.Message, error) { return pkgmail.Message{}, nil }
func (a adminAlert) ToDatabase() (map[string]any, error) {
	return map[string]any{"slug": a.slug, "detail": a.detail}, nil
}

// alert sends a control-admin lifecycle event, logging instead of failing.
func (s *Tenants) alert(ctx context.Context, event, slug, detail string) {
	if s.notifier == nil {
		return
	}
	if err := s.notifier.Send(ctx, adminAudience(), adminAlert{event: event, slug: slug, detail: detail}); err != nil {
		slog.Warn("control alert failed", "event", event, "tenant", slug, "err", err)
	}
}

// Create validates and inserts a shared-first tenant.
func (s *Tenants) Create(ctx context.Context, dto CreateTenantDTO) (*tenancy.TenantRecord, error) {
	slug := strings.ToLower(strings.TrimSpace(dto.Slug))
	if !tenancy.IsValidSlug(slug) {
		return nil, web.Wrap(http.StatusBadRequest, "Invalid tenant slug.", errors.New("slug must be [a-z0-9-], start alnum"))
	}
	if _, err := s.tenants.FindBySlug(ctx, s.db, slug); err == nil {
		return nil, web.Conflict("Slug already taken.")
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, web.Wrap(http.StatusInternalServerError, "Could not create tenant.", err)
	}
	pool := strings.TrimSpace(dto.Pool)
	if pool == "" {
		pool = s.defPool
	}
	rec := &tenancy.TenantRecord{
		ID: uuid.New(), Slug: slug, Name: strings.TrimSpace(dto.Name),
		Placement: tenancy.PlacementShared, Pool: pool, Status: tenancy.StatusActive,
	}
	if err := s.tenants.CreateTenant(ctx, s.db, rec); err != nil {
		return nil, web.Wrap(http.StatusInternalServerError, "Could not create tenant.", err)
	}
	s.alert(ctx, "created", slug, "Tenant created in pool "+pool+".")
	return rec, nil
}

// List pages tenants.
func (s *Tenants) List(ctx context.Context, limit, offset int) ([]tenancy.TenantRecord, int64, error) {
	rows, total, err := s.tenants.ListTenants(ctx, s.db, limit, offset)
	if err != nil {
		return nil, 0, web.Wrap(http.StatusInternalServerError, "Could not list tenants.", err)
	}
	return rows, total, nil
}

// Get loads one tenant by slug.
func (s *Tenants) Get(ctx context.Context, slug string) (*tenancy.TenantRecord, error) {
	rec, err := s.tenants.FindBySlug(ctx, s.db, strings.ToLower(strings.TrimSpace(slug)))
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, web.NotFound("Tenant not found.")
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not load tenant.", err)
	}
	return rec, nil
}

// SetStatus flips active/suspended. Placement and pool are Migrator-owned
// and rejected here so moves cannot bypass the ledger.
func (s *Tenants) SetStatus(ctx context.Context, slug, status string) (*tenancy.TenantRecord, error) {
	rec, err := s.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	rec.Status = status
	if err := s.tenants.SaveTenant(ctx, s.db, rec); err != nil {
		return nil, web.Wrap(http.StatusInternalServerError, "Could not update tenant.", err)
	}
	s.alert(ctx, "status", rec.Slug, "Status set to "+status+".")
	return rec, nil
}

// LatestMigration reports the newest move ledger row, or nil when the
// tenant never moved.
func (s *Tenants) LatestMigration(ctx context.Context, slug string) (*tenantmigrations.Migration, error) {
	rec, err := s.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	m, err := s.moves.LatestMigration(ctx, s.db, rec.ID)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not load migration.", err)
	}
	return m, nil
}

// Remove deletes a shared tenant: it purges the tenant's pool rows, then
// deletes the record (FK cascades take domains and ledger rows). A live
// (non-terminal) migration blocks removal with 409, and dedicated tenants
// are refused with 400 — dropping a live dedicated database is a separate,
// explicitly designed feature, not a side effect.
func (s *Tenants) Remove(ctx context.Context, slug string) error {
	rec, err := s.Get(ctx, slug)
	if err != nil {
		return err
	}
	if live, err := s.moves.LatestMigration(ctx, s.db, rec.ID); err == nil && live != nil && !live.Terminal() {
		return web.Wrap(http.StatusConflict, "Tenant has a live migration.", errors.New("finish or roll back the move first"))
	} else if err != nil && !errors.Is(err, gorm.ErrRecordNotFound) {
		return web.Wrap(http.StatusInternalServerError, "Could not remove tenant.", err)
	}
	if rec.Placement != tenancy.PlacementShared {
		return web.Wrap(http.StatusBadRequest, "Dedicated tenant removal is not supported.", errors.New("migrate back to shared first"))
	}
	if s.mgr == nil {
		return web.Wrap(http.StatusInternalServerError, "Could not remove tenant.", errors.New("no placement router"))
	}
	poolDSN, err := s.mgr.PoolDSNFor(rec.Pool)
	if err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not remove tenant.", err)
	}
	if err := tenantmigrations.PurgeTenantData(ctx, s.mgr.DriverName(), poolDSN, rec.ID.String()); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not remove tenant.", err)
	}
	if err := s.tenants.DeleteTenant(ctx, s.db, rec.ID); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not remove tenant.", err)
	}
	s.alert(ctx, "removed", rec.Slug, "Tenant removed with its pool rows.")
	return nil
}
