package config

import (
	"github.com/spf13/viper"
)

// Tenancy holds elastic multi-tenancy settings: one control database owns
// tenant records, while tenant data lives in shared pools by default and
// can move to dedicated databases per tenant (placement shared|dedicated).
// Empty DSNs fall back to the primary database, so single-database local
// development works with zero extra configuration.
type Tenancy struct {
	// ControlDSN is the full GORM DSN of the control database (tenants,
	// tenant_migrations, control_admins). Empty means the primary DSN.
	ControlDSN string `mapstructure:"CONTROL_DSN"`
	// PoolDSNs maps shared-pool name to full GORM DSN, comma-separated
	// "name=dsn" pairs (e.g. "shared_1=...,shared_2=..."). Empty means the
	// primary DSN serves as the default pool.
	PoolDSNs string `mapstructure:"SHARED_POOL_DSNS"`
	// DefaultPool names the pool new tenants land in.
	DefaultPool string `mapstructure:"DEFAULT_POOL"`
	// DSNTemplate builds dedicated database DSNs with one %s for the
	// tenant slug (e.g. "u:p@tcp(db:3306)/ginplate_tenant_%s?...").
	// Empty means dedicated placement is unsupported.
	DSNTemplate string `mapstructure:"TENANT_DSN_TEMPLATE"`
	// AppUser/AppPass optionally swap the owner credentials for a limited
	// application role (pgsql), so row-level-security policies actually
	// constrain the runtime. Empty keeps owner credentials (RLS defined
	// but bypassed by owners).
	AppUser string `mapstructure:"APP_DB_USER"`
	AppPass string `mapstructure:"APP_DB_PASSWORD"`
	// ChunkSize bounds Migrator copy batches; RetainDays delays pool-row
	// cleanup after cutover.
	ChunkSize  int `mapstructure:"MIGRATION_CHUNK_SIZE"`
	RetainDays int `mapstructure:"MIGRATION_RETAIN_DAYS"`
	// ControlJWTSecret signs control-plane (admin) tokens. Empty falls back
	// to APP_KEY with a startup warning; production requires it explicit.
	ControlJWTSecret string `mapstructure:"CONTROL_JWT_SECRET"`
}

func applyTenancyDefaults(v *viper.Viper) {
	v.SetDefault("CONTROL_DSN", "")
	v.SetDefault("SHARED_POOL_DSNS", "")
	v.SetDefault("DEFAULT_POOL", "shared_1")
	v.SetDefault("TENANT_DSN_TEMPLATE", "")
	v.SetDefault("APP_DB_USER", "")
	v.SetDefault("APP_DB_PASSWORD", "")
	v.SetDefault("MIGRATION_CHUNK_SIZE", 5000)
	v.SetDefault("MIGRATION_RETAIN_DAYS", 7)
	v.SetDefault("CONTROL_JWT_SECRET", "")
}
