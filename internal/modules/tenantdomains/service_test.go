package tenantdomains

import (
	"context"
	"errors"
	"testing"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/pkg/tenancy"
)

// stubStore is an in-memory TenantDomainRepository.
type stubStore struct {
	byID map[uuid.UUID]*Domain
	err  error
}

func newStubStore() *stubStore { return &stubStore{byID: map[uuid.UUID]*Domain{}} }

func (s *stubStore) CreateDomain(_ context.Context, _ *gorm.DB, d *Domain) error {
	if s.err != nil {
		return s.err
	}
	if d.ID == uuid.Nil {
		d.ID = uuid.New()
	}
	s.byID[d.ID] = d
	return nil
}

func (s *stubStore) SaveDomain(_ context.Context, _ *gorm.DB, d *Domain) error {
	s.byID[d.ID] = d
	return nil
}

func (s *stubStore) DeleteDomain(_ context.Context, _ *gorm.DB, id uuid.UUID) error {
	delete(s.byID, id)
	return nil
}

func (s *stubStore) ListDomains(_ context.Context, _ *gorm.DB, tenantID uuid.UUID) ([]Domain, error) {
	var rows []Domain
	for _, d := range s.byID {
		if d.TenantID == tenantID {
			rows = append(rows, *d)
		}
	}
	return rows, nil
}

func (s *stubStore) FindDomainByID(_ context.Context, _ *gorm.DB, id uuid.UUID) (*Domain, error) {
	if d, ok := s.byID[id]; ok {
		return d, nil
	}
	return nil, gorm.ErrRecordNotFound
}

// stubLookup resolves slugs without a database.
type stubLookup struct {
	bySlug map[string]*tenancy.TenantRecord
}

func (s *stubLookup) Get(_ context.Context, slug string) (*tenancy.TenantRecord, error) {
	if r, ok := s.bySlug[slug]; ok {
		return r, nil
	}
	return nil, errors.New("Tenant not found.")
}

func newService() (*TenantDomain, *stubStore) {
	store := newStubStore()
	lookup := &stubLookup{bySlug: map[string]*tenancy.TenantRecord{
		"acme": {ID: uuid.MustParse("11111111-1111-1111-1111-111111111111"), Slug: "acme"},
	}}
	return NewTenantDomain(nil, store, lookup), store
}

// TestTenantDomain covers domain lifecycle validation without a database.
func TestTenantDomain(t *testing.T) {
	t.Run("add-starts-unverified", func(t *testing.T) {
		svc, _ := newService()
		d, err := svc.AddDomain(context.Background(), "acme", "App.AcmeCorp.COM")
		if err != nil {
			t.Fatal(err)
		}
		if d.Domain != "app.acmecorp.com" || d.Verified {
			t.Fatalf("domain = %+v", d)
		}
	})

	t.Run("add-rejects", func(t *testing.T) {
		svc, _ := newService()
		if _, err := svc.AddDomain(context.Background(), "ghost", "x.example.com"); err == nil {
			t.Fatal("unknown tenant accepted")
		}
		if _, err := svc.AddDomain(context.Background(), "acme", "https://x.example.com"); err == nil {
			t.Fatal("bad domain accepted")
		}
	})

	t.Run("verify-flips-flag", func(t *testing.T) {
		svc, store := newService()
		d, err := svc.AddDomain(context.Background(), "acme", "x.example.com")
		if err != nil {
			t.Fatal(err)
		}
		updated, err := svc.VerifyDomain(context.Background(), d.ID, true)
		if err != nil || !updated.Verified {
			t.Fatalf("verify = %+v,%v", updated, err)
		}
		if _, err := svc.VerifyDomain(context.Background(), uuid.New(), true); err == nil {
			t.Fatal("missing domain verified")
		}
		_ = store
	})

	t.Run("remove-deletes", func(t *testing.T) {
		svc, store := newService()
		d, err := svc.AddDomain(context.Background(), "acme", "gone.example.com")
		if err != nil {
			t.Fatal(err)
		}
		if err := svc.RemoveDomain(context.Background(), d.ID); err != nil {
			t.Fatal(err)
		}
		if _, ok := store.byID[d.ID]; ok {
			t.Fatal("domain survived removal")
		}
		if err := svc.RemoveDomain(context.Background(), uuid.New()); err == nil {
			t.Fatal("missing domain removed")
		}
	})

	t.Run("list-scopes-to-tenant", func(t *testing.T) {
		svc, _ := newService()
		if _, err := svc.AddDomain(context.Background(), "acme", "a.example.com"); err != nil {
			t.Fatal(err)
		}
		rows, err := svc.ListDomains(context.Background(), "acme")
		if err != nil || len(rows) != 1 {
			t.Fatalf("list = %+v,%v", rows, err)
		}
		if _, err := svc.ListDomains(context.Background(), "ghost"); err == nil {
			t.Fatal("unknown tenant listed")
		}
	})
}
