-- +goose Up

-- Tenancy control plane (consolidated): tenant registry, custom-domain
-- mapping, shared-to-dedicated move ledger, and control-plane admins.
-- Supersedes the four 20261009000002-05 files (same four CREATEs, one
-- version) written before the 20251107* base renames. Applies after them.
-- Fresh databases already carry the tenant-data delta (tenant_id columns,
-- (tenant_id,email) unique, password_reset kind PK) via the base files;
-- pre-tenancy databases that applied those versions earlier need manual
-- ALTERs (out of scope here).
-- Tenants are created explicitly via POST /admin/tenants — nothing is
-- seeded. The first tenant is bootstrapped with the cadmin SeedAdmin Go
-- API + the create endpoint (see TENANCY.md).
CREATE TABLE IF NOT EXISTS tenants (
  id CHAR(36) PRIMARY KEY,
  slug VARCHAR(64) NOT NULL,
  name VARCHAR(255) NOT NULL,
  placement VARCHAR(16) NOT NULL DEFAULT 'shared',
  pool VARCHAR(64) NOT NULL DEFAULT 'shared_1',
  status VARCHAR(16) NOT NULL DEFAULT 'active',
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_tenants_slug (slug),
  INDEX idx_tenants_status (status)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Custom-domain to tenant mapping for customer domains.
CREATE TABLE IF NOT EXISTS tenant_domains (
  id CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  domain VARCHAR(255) NOT NULL,
  verified TINYINT(1) NOT NULL DEFAULT 0,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_tenant_domains_domain (domain),
  INDEX idx_tenant_domains_tenant (tenant_id),
  CONSTRAINT fk_tenant_domains_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Shared-to-dedicated move ledger: resumable, idempotent, auditable.
CREATE TABLE IF NOT EXISTS tenant_migrations (
  id CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL,
  phase VARCHAR(32) NOT NULL DEFAULT 'provision',
  reason TEXT NULL,
  watermark TIMESTAMP NULL DEFAULT NULL,
  checksums TEXT NULL,
  progress TEXT NULL,
  attempts INT NOT NULL DEFAULT 0,
  error TEXT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  INDEX idx_tenant_migrations_tenant (tenant_id),
  CONSTRAINT fk_tenant_migrations_tenant FOREIGN KEY (tenant_id) REFERENCES tenants(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Control-plane administrators. Tenant admins stay in users (role
-- column); these super_admins manage tenants and never live in tenant
-- data. Seed via the cadmin SeedAdmin Go API (no seed-admin CLI yet).
CREATE TABLE IF NOT EXISTS control_admins (
  id CHAR(36) PRIMARY KEY,
  name VARCHAR(255) NOT NULL,
  email VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'super_admin',
  is_active TINYINT(1) NOT NULL DEFAULT 1,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  updated_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP ON UPDATE CURRENT_TIMESTAMP,
  UNIQUE KEY uq_control_admins_email (email)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS control_admins;
DROP TABLE IF EXISTS tenant_migrations;
DROP TABLE IF EXISTS tenant_domains;
DROP TABLE IF EXISTS tenants;
