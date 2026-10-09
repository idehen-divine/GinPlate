-- +goose Up

-- Base schema (squashed): app tables besides users. sessions references
-- users(id), which the earlier users migration creates first.
CREATE TABLE IF NOT EXISTS password_reset_tokens (
  email VARCHAR(255) NOT NULL,
  kind VARCHAR(16) NOT NULL DEFAULT 'user',
  tenant_id CHAR(36) NOT NULL DEFAULT '',
  -- Only the SHA-256 hex digest is persisted; the raw token is emailed
  -- once and never stored. Single-use via used_at, expiry via expires_at.
  -- kind separates tenant users from control admins sharing one table.
  token_hash VARCHAR(64) NOT NULL,
  expires_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  used_at TIMESTAMP NULL DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  PRIMARY KEY (email, kind),
  UNIQUE KEY uq_password_reset_token_hash (token_hash),
  INDEX idx_password_reset_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS sessions (
  id CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL DEFAULT '',
  user_id CHAR(36) NULL,
  refresh_jti CHAR(36) NOT NULL,
  ip_address VARCHAR(45) NULL,
  user_agent TEXT NULL,
  last_activity TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  access_expires_at TIMESTAMP NULL DEFAULT NULL,
  refresh_expires_at TIMESTAMP NULL DEFAULT NULL,
  INDEX idx_sessions_user (user_id),
  INDEX idx_sessions_tenant (tenant_id),
  UNIQUE KEY uq_sessions_refresh (refresh_jti),
  CONSTRAINT fk_sessions_user FOREIGN KEY (user_id) REFERENCES users(id) ON DELETE CASCADE
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS caches (
  cache_key VARCHAR(255) PRIMARY KEY,
  value TEXT NOT NULL,
  expires_at TIMESTAMP NULL DEFAULT NULL,
  INDEX idx_caches_expires (expires_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- Microsecond timestamps: FIFO order breaks on random UUIDs when pushes
-- share a second, so available_at needs sub-second precision like pgsql.
CREATE TABLE IF NOT EXISTS jobs (
  id CHAR(36) PRIMARY KEY,
  name VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  available_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  reserved_at TIMESTAMP(6) NULL DEFAULT NULL,
  created_at TIMESTAMP(6) NOT NULL DEFAULT CURRENT_TIMESTAMP(6),
  INDEX idx_jobs_available (available_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS failed_jobs (
  id CHAR(36) PRIMARY KEY,
  connection VARCHAR(32) NOT NULL,
  queue VARCHAR(64) NOT NULL DEFAULT 'default',
  name VARCHAR(64) NOT NULL,
  payload TEXT NOT NULL,
  exception TEXT NOT NULL,
  attempts INT NOT NULL DEFAULT 0,
  failed_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_failed_jobs_failed_at (failed_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

CREATE TABLE IF NOT EXISTS notifications (
  id CHAR(36) PRIMARY KEY,
  tenant_id CHAR(36) NOT NULL DEFAULT '',
  notifiable_type VARCHAR(64) NOT NULL,
  notifiable_id VARCHAR(64) NOT NULL,
  type VARCHAR(128) NOT NULL,
  data TEXT NOT NULL,
  read_at TIMESTAMP NULL DEFAULT NULL,
  created_at TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP,
  INDEX idx_notifications_notifiable (notifiable_type, notifiable_id, created_at),
  INDEX idx_notifications_tenant (tenant_id)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci;

-- +goose Down
DROP TABLE IF EXISTS notifications;
DROP TABLE IF EXISTS failed_jobs;
DROP TABLE IF EXISTS jobs;
DROP TABLE IF EXISTS caches;
DROP TABLE IF EXISTS sessions;
DROP TABLE IF EXISTS password_reset_tokens;
