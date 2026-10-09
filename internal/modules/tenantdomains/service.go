package tenantdomains

import (
	"context"
	"errors"
	"net/http"
	"strings"

	"github.com/google/uuid"
	"gorm.io/gorm"

	"github.com/idehen-divine/GinPlate/pkg/tenancy"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// tenantLookup resolves a tenant slug to its record. It is implemented by
// the parent Tenants service; declared locally so this package never
// imports its parent (which would cycle, since the parent composes this
// package's service).
type tenantLookup interface {
	Get(ctx context.Context, slug string) (*tenancy.TenantRecord, error)
}

// TenantDomain manages custom-domain mappings: add (unverified), list,
// verify (ownership proof recorded), remove. Resolution itself lives in
// pkg/tenancy; this unit owns the mappings' lifecycle.
type TenantDomain struct {
	db     *gorm.DB
	repo   TenantDomainRepository
	lookup tenantLookup
}

// NewTenantDomain wires the control handle, repository, and tenant lookup.
// The repository is required (no silent default: persistence must be
// explicit); lookup may be nil only in tests that never resolve slugs.
func NewTenantDomain(db *gorm.DB, repo TenantDomainRepository, lookup tenantLookup) *TenantDomain {
	return &TenantDomain{db: db, repo: repo, lookup: lookup}
}

// AddDomain maps a customer-owned domain to a tenant. The mapping starts
// unverified and never resolves traffic until an admin verifies ownership
// out of band (see VerifyDomain).
func (s *TenantDomain) AddDomain(ctx context.Context, slug, domain string) (*Domain, error) {
	rec, err := s.lookup.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	domain = strings.ToLower(strings.TrimSpace(domain))
	if !tenancy.IsValidDomain(domain) {
		return nil, web.Wrap(http.StatusBadRequest, "Invalid domain.", errors.New("domain must be a bare hostname"))
	}
	d := &Domain{TenantID: rec.ID, Domain: domain}
	if err := s.repo.CreateDomain(ctx, s.db, d); err != nil {
		return nil, web.Wrap(http.StatusInternalServerError, "Could not add domain.", err)
	}
	return d, nil
}

// ListDomains lists a tenant's custom-domain mappings.
func (s *TenantDomain) ListDomains(ctx context.Context, slug string) ([]Domain, error) {
	rec, err := s.lookup.Get(ctx, slug)
	if err != nil {
		return nil, err
	}
	rows, err := s.repo.ListDomains(ctx, s.db, rec.ID)
	if err != nil {
		return nil, web.Wrap(http.StatusInternalServerError, "Could not list domains.", err)
	}
	return rows, nil
}

// VerifyDomain flips a mapping's verified flag. Verification itself happens
// out of band (DNS proof); this records the outcome.
func (s *TenantDomain) VerifyDomain(ctx context.Context, id uuid.UUID, verified bool) (*Domain, error) {
	d, err := s.repo.FindDomainByID(ctx, s.db, id)
	if err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, web.NotFound("Domain not found.")
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not load domain.", err)
	}
	d.Verified = verified
	if err := s.repo.SaveDomain(ctx, s.db, d); err != nil {
		return nil, web.Wrap(http.StatusInternalServerError, "Could not update domain.", err)
	}
	return d, nil
}

// RemoveDomain deletes a custom-domain mapping. In-flight requests holding
// the tenant handle are unaffected; new resolutions stop matching at once.
func (s *TenantDomain) RemoveDomain(ctx context.Context, id uuid.UUID) error {
	if _, err := s.repo.FindDomainByID(ctx, s.db, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return web.NotFound("Domain not found.")
		}
		return web.Wrap(http.StatusInternalServerError, "Could not load domain.", err)
	}
	if err := s.repo.DeleteDomain(ctx, s.db, id); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not remove domain.", err)
	}
	return nil
}
