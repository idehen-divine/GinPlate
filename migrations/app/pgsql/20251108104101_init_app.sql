-- +goose Up

-- Base schema (squashed): app tables besides users. sessions references
-- users(id), which the earlier users migration creates first.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
  email VARCHAR(255) PRIMARY KEY,
  token VARCHAR(255) NOT NULL,
  created_at TIMESTAMPTZ
);

CREATE TABLE IF NOT EXISTS sessions (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  user_id UUID REFERENCES users(id) ON DELETE CASCADE,
  refresh_jti UUID NOT NULL,
  ip_address VARCHAR(45),
  user_agent TEXT,
  last_activity TIMESTAMPTZ NOT NULL DEFAULT now(),
  access_expires_at TIMESTAMPTZ,
  refresh_expires_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_sessions_user ON sessions(user_id);
CREATE UNIQUE INDEX IF NOT EXISTS uq_sessions_refresh ON sessions(refresh_jti);

CREATE TABLE IF NOT EXISTS caches (
  cache_key VARCHAR(255) PRIMARY KEY,
  value TEXT NOT NULL,
  expires_at TIMESTAMPTZ
);
CREATE INDEX IF NOT EXISTS idx_caches_expires ON caches(expires_at);

CREATE TABLE IF NOT EXISTS jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  name VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  available_at TIMESTAMPTZ NOT NULL DEFAULT now(),
  reserved_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_jobs_available ON jobs(available_at);

CREATE TABLE IF NOT EXISTS failed_jobs (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  connection VARCHAR(32) NOT NULL,
  queue VARCHAR(64) NOT NULL DEFAULT 'default',
  name VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  exception TEXT NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  failed_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_failed_jobs_failed_at ON failed_jobs(failed_at);

CREATE TABLE IF NOT EXISTS notifications (
  id UUID PRIMARY KEY DEFAULT gen_random_uuid(),
  notifiable_type VARCHAR(64) NOT NULL,
  notifiable_id VARCHAR(64) NOT NULL,
  type VARCHAR(128) NOT NULL,
  data TEXT NOT NULL,
  read_at TIMESTAMPTZ,
  created_at TIMESTAMPTZ NOT NULL DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_notifications_notifiable ON notifications(notifiable_type, notifiable_id, created_at);

-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS failed_jobs;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS caches;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS password_reset_tokens;
