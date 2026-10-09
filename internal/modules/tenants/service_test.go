package tenants

import (
	"context"
	"strings"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/internal/modules/tenantmigrations"
	"github.com/idehen-divine/GinPlate/pkg/tenancy"
)

// stubRepo is an in-memory TenantsRepository + TenantMigrationsRepository
// for hermetic service tests.
type stubRepo struct {
	bySlug map[string]*tenancy.TenantRecord
	live   *tenantmigrations.Migration
	err    error
}

func newStubRepo() *stubRepo { return &stubRepo{bySlug: map[string]*tenancy.TenantRecord{}} }

func (s *stubRepo) CreateTenant(_ context.Context, _ *gorm.DB, rec *tenancy.TenantRecord) error {
	if s.err != nil {
		return s.err
	}
	s.bySlug[rec.Slug] = rec
	return nil
}

func (s *stubRepo) SaveTenant(_ context.Context, _ *gorm.DB, rec *tenancy.TenantRecord) error {
	s.bySlug[rec.Slug] = rec
	return nil
}

func (s *stubRepo) FindBySlug(_ context.Context, _ *gorm.DB, slug string) (*tenancy.TenantRecord, error) {
	if r, ok := s.bySlug[slug]; ok {
		return r, nil
	}
	return nil, gorm.ErrRecordNotFound
}

func (s *stubRepo) FindTenantByID(_ context.Context, _ *gorm.DB, _ uuid.UUID) (*tenancy.TenantRecord, error) {
	return nil, gorm.ErrRecordNotFound
}

func (s *stubRepo) ListTenants(_ context.Context, _ *gorm.DB, _, _ int) ([]tenancy.TenantRecord, int64, error) {
	rows := make([]tenancy.TenantRecord, 0, len(s.bySlug))
	for _, r := range s.bySlug {
		rows = append(rows, *r)
	}
	return rows, int64(len(rows)), nil
}

func (s *stubRepo) DeleteTenant(_ context.Context, _ *gorm.DB, id uuid.UUID) error {
	for slug, r := range s.bySlug {
		if r.ID == id {
			delete(s.bySlug, slug)
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (s *stubRepo) CreateMigration(_ context.Context, _ *gorm.DB, _ *tenantmigrations.Migration) error {
	return nil
}

func (s *stubRepo) SaveMigration(_ context.Context, _ *gorm.DB, _ *tenantmigrations.Migration) error {
	return nil
}

func (s *stubRepo) FindMigration(_ context.Context, _ *gorm.DB, _ uuid.UUID) (*tenantmigrations.Migration, error) {
	return nil, gorm.ErrRecordNotFound
}

func (s *stubRepo) LatestMigration(_ context.Context, _ *gorm.DB, _ uuid.UUID) (*tenantmigrations.Migration, error) {
	if s.live != nil {
		return s.live, nil
	}
	return nil, gorm.ErrRecordNotFound
}

// Compile-time proof the stub satisfies the seams this package tests.
var (
	_ TenantsRepository          = (*stubRepo)(nil)
	_ TenantMigrationsRepository = (*stubRepo)(nil)
)

// TestTenants covers tenant lifecycle validation without a database.
func TestTenants(t *testing.T) {
	t.Run("create-shared-first", func(t *testing.T) {
		svc := NewTenants(nil, newStubRepo(), newStubRepo(), nil, "shared_1")
		rec, err := svc.Create(context.Background(), CreateTenantDTO{Slug: "Acme", Name: "Acme Inc"})
		if err != nil {
			t.Fatal(err)
		}
		if rec.Slug != "acme" || rec.Placement != tenancy.PlacementShared ||
			rec.Pool != "shared_1" || rec.Status != tenancy.StatusActive {
			t.Fatalf("record = %+v", rec)
		}
	})

	t.Run("create-rejects", func(t *testing.T) {
		repo := newStubRepo()
		svc := NewTenants(nil, repo, repo, nil, "shared_1")
		if _, err := svc.Create(context.Background(), CreateTenantDTO{Slug: "../evil", Name: "X"}); err == nil {
			t.Fatal("bad slug accepted")
		}
		if _, err := svc.Create(context.Background(), CreateTenantDTO{Slug: "acme", Name: "A"}); err != nil {
			t.Fatal(err)
		}
		if _, err := svc.Create(context.Background(), CreateTenantDTO{Slug: "acme", Name: "B"}); err == nil {
			t.Fatal("duplicate slug accepted")
		} else if err.Error() != "Slug already taken." {
			t.Fatalf("wrong error: %v", err)
		}
	})

	t.Run("status-and-missing", func(t *testing.T) {
		svc := NewTenants(nil, newStubRepo(), newStubRepo(), nil, "shared_1")
		if _, err := svc.Get(context.Background(), "ghost"); err == nil {
			t.Fatal("missing tenant found")
		}
		rec, err := svc.Create(context.Background(), CreateTenantDTO{Slug: "acme", Name: "A"})
		if err != nil {
			t.Fatal(err)
		}
		_ = rec
		updated, err := svc.SetStatus(context.Background(), "acme", tenancy.StatusSuspended)
		if err != nil || updated.Status != tenancy.StatusSuspended {
			t.Fatalf("suspend = %+v,%v", updated, err)
		}
		if m, err := svc.LatestMigration(context.Background(), "acme"); err != nil || m != nil {
			t.Fatalf("migration = %+v,%v", m, err)
		}
	})

	t.Run("remove-guards", func(t *testing.T) {
		ctx := context.Background()
		svc := NewTenants(nil, newStubRepo(), newStubRepo(), nil, "shared_1")
		if err := svc.Remove(ctx, "ghost"); err == nil {
			t.Fatal("missing tenant removed")
		}
		if _, err := svc.Create(ctx, CreateTenantDTO{Slug: "acme", Name: "A"}); err != nil {
			t.Fatal(err)
		}
		// Shared, quiet, but no placement router wired: fail closed, and
		// the tenant row must survive the failed removal.
		if err := svc.Remove(ctx, "acme"); err == nil {
			t.Fatal("removal without router accepted")
		}
		if _, err := svc.Get(ctx, "acme"); err != nil {
			t.Fatalf("tenant lost to failed removal: %v", err)
		}
	})

	t.Run("remove-blocks-live-and-dedicated", func(t *testing.T) {
		ctx := context.Background()
		repo := newStubRepo()
		svc := NewTenants(nil, repo, repo, nil, "shared_1")
		if _, err := svc.Create(ctx, CreateTenantDTO{Slug: "busy", Name: "B"}); err != nil {
			t.Fatal(err)
		}
		repo.live = &tenantmigrations.Migration{Phase: tenantmigrations.PhaseCopy}
		if err := svc.Remove(ctx, "busy"); err == nil {
			t.Fatal("live migration removal accepted")
		} else if got := err.Error(); !strings.Contains(got, "Tenant has a live migration.") {
			t.Fatalf("wrong error: %v", err)
		}
		repo.live = &tenantmigrations.Migration{Phase: tenantmigrations.PhaseDone}
		rec, err := svc.Get(ctx, "busy")
		if err != nil {
			t.Fatal(err)
		}
		rec.Placement = tenancy.PlacementDedicated
		if err := svc.Remove(ctx, "busy"); err == nil {
			t.Fatal("dedicated removal accepted")
		} else if got := err.Error(); !strings.Contains(got, "Dedicated tenant removal is not supported.") {
			t.Fatalf("wrong error: %v", err)
		}
	})
}
