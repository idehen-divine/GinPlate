-- +goose Up
CREATE EXTENSION IF NOT EXISTS pgcrypto;

-- Base schema: users table. Roles are a plain string column on users;
-- add a roles/permissions model later only if you outgrow that.
-- IDs are UUIDs; the app sets them in BeforeCreate.
-- Add your domain tables in a new timestamped migration via
-- `ginplate make:migration <Name>`.

CREATE TABLE IF NOT EXISTS users (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(255) NOT NULL,
  email VARCHAR(255) NOT NULL,
  password_hash VARCHAR(255) NOT NULL,
  role VARCHAR(32) NOT NULL DEFAULT 'member',
  is_active BOOLEAN NOT NULL DEFAULT TRUE,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  updated_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  UNIQUE (email)
);

-- +goose Down
DROP TABLE IF EXISTS users;
