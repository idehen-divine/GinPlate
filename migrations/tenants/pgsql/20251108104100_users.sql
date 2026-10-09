-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Base schema: users table. Roles are a plain string column on users;
-- add a roles/permissions model later only if you outgrow that.
-- auth_version revokes JWTs on privilege change (tokens carry aver).
-- IDs are UUIDs; the app sets them in BeforeCreate.
-- Add your domain tables in a new timestamped migration via
-- `ginplate make:migration <Name>`.

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  tenant_id UUID NOT NULL DEFAULT '00000000-0000-0000-0000-000000000000',
  name VARCHAR(255) NOT NULL,
  email VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'member',
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  auth_version INTEGER NOT NULL DEFAULT 1,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (tenant_id, email)
);

CREATE INDEX IF NOT EXISTS idx_users_tenant ON users(tenant_id);

-- +goose Down
DROP TABLE IF EXISTS users;
