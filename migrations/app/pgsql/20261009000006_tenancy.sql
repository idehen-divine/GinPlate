-- +goose Up

-- Tenancy control plane (consolidated): tenant registry, custom-domain
-- mapping, shared-to-dedicated move ledger, and control-plane admins.
-- Supersedes the four 20261009000002-05 files (same four CREATEs, one
-- version) written before the 20251108* base renames. Applies after them.
-- Fresh databases already carry the tenant-data delta (tenant_id columns,
-- (tenant_id,email) unique, password_reset kind PK, RLS policies) via the
-- base files; pre-tenancy databases that applied those versions earlier
-- need manual ALTERs (out of scope here).
-- Tenants are created explicitly via POST /admin/tenants — nothing is
-- seeded. The first tenant is bootstrapped with the cadmin SeedAdmin Go
-- API + the create endpoint (see TENANCY.md).
CREATE TABLE IF NOT EXISTS tenants (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  slug VARCHAR(64) NOT NULL,
  name VARCHAR(255) NOT NULL,
  placement VARCHAR(16) NOT NULL DEFAULT 'shared',
  pool VARCHAR(64) NOT NULL DEFAULT 'shared_1',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenants_slug ON tenants(slug);
CREATE INDEX IF NOT EXISTS idx_tenants_status ON tenants(status);

-- Custom-domain to tenant mapping for customer domains.
CREATE TABLE IF NOT EXISTS tenant_domains (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  domain VARCHAR(255) NOT NULL,
  verified BOOLEAN NOT NULL DEFAULT FALSE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_tenant_domains_domain ON tenant_domains(domain);
CREATE INDEX IF NOT EXISTS idx_tenant_domains_tenant ON tenant_domains(tenant_id);

-- Shared-to-dedicated move ledger: resumable, idempotent, auditable.
CREATE TABLE IF NOT EXISTS tenant_migrations (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL REFERENCES tenants(id) ON DELETE CASCADE,
  phase VARCHAR(32) NOT NULL DEFAULT 'provision',
  reason TEXT,
  watermark TIMESTAMPTZ,
  checksums TEXT,
  progress TEXT,
  attempts INT NOT NULL DEFAULT 0,
  error TEXT,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_tenant_migrations_tenant ON tenant_migrations(tenant_id);

-- Control-plane administrators. Tenant admins stay in users (role
-- column); these super_admins manage tenants and never live in tenant
-- data. Seed via the cadmin SeedAdmin Go API (no seed-admin CLI yet).
CREATE TABLE IF NOT EXISTS control_admins (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) NOT NULL,
  email VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'super_admin',
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE UNIQUE INDEX IF NOT EXISTS uq_control_admins_email ON control_admins(email);

-- +goose Down
DROP TABLE IF EXISTS control_admins;
DROP TABLE IF EXISTS tenant_migrations;
DROP TABLE IF EXISTS tenant_domains;
DROP TABLE IF EXISTS tenants;
