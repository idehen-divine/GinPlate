package config

import (
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Database holds the primary database connection plus the shared Redis
// connection. Like Laravel's database.php, Redis lives here: sessions,
// cache, and queue all read the same address.
type Database struct {
	Driver  string `mapstructure:"DB_CONNECTION"`
	Host    string `mapstructure:"DB_HOST"`
	Port    string `mapstructure:"DB_PORT"`
	Name    string `mapstructure:"DB_DATABASE"`
	User    string `mapstructure:"DB_USERNAME"`
	Pass    string `mapstructure:"DB_PASSWORD"`
	LogMode string `mapstructure:"DB_LOG_MODE"`

	Redis Redis `mapstructure:",squash"`
}

// Redis holds the shared Redis connection. REDIS_CLIENT is accepted and
// ignored (Go has no phpredis). User/DB select the ACL identity and logical
// database; empty user means no ACL auth, DB 0 is the default index.
type Redis struct {
	Host string `mapstructure:"REDIS_HOST"`
	Port string `mapstructure:"REDIS_PORT"`
	Pass string `mapstructure:"REDIS_PASSWORD"`
	User string `mapstructure:"REDIS_USERNAME"`
	DB   int    `mapstructure:"REDIS_DB"`
}

func applyDatabaseDefaults(v *viper.Viper) {
	v.SetDefault("DB_CONNECTION", "mysql")
	v.SetDefault("DB_HOST", "127.0.0.1")
	v.SetDefault("DB_PORT", "3306")
	v.SetDefault("DB_DATABASE", "ginplate")
	v.SetDefault("DB_USERNAME", "root")
	v.SetDefault("DB_PASSWORD", "")
	v.SetDefault("DB_LOG_MODE", "info")
	v.SetDefault("REDIS_HOST", "127.0.0.1")
	v.SetDefault("REDIS_PORT", "6379")
	v.SetDefault("REDIS_PASSWORD", "")
	v.SetDefault("REDIS_USERNAME", "")
	v.SetDefault("REDIS_DB", 0)
}

// DSN assembles the GORM DSN from parts. MySQL uses go-sql-driver shape,
// Postgres uses URL form (required for app-role swapping on pgsql).
func (d Database) DSN() string {
	if normalizeDriver(d.Driver) == "pgsql" {
		dsn := fmt.Sprintf("postgres://%s:%s@%s:%s/%s?sslmode=disable",
			d.User, d.Pass, d.Host, d.Port, d.Name)
		return dsn
	}
	return fmt.Sprintf("%s:%s@tcp(%s:%s)/%s?charset=utf8mb4&parseTime=True&loc=Local",
		d.User, d.Pass, d.Host, d.Port, d.Name)
}

// normalizeDriver maps engine names to canonical "mysql"|"pgsql".
// (Local copy: config must not pull in goose via pkg/database.)
func normalizeDriver(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pgsql", "postgres", "postgresql", "pgx":
		return "pgsql"
	default:
		return "mysql"
	}
}

// Addr assembles host:port for the Redis client. Empty password means no AUTH.
func (r Redis) Addr() string {
	return r.Host + ":" + r.Port
}
