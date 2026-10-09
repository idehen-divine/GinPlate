package config

import (
	"crypto/tls"
	"crypto/x509"
	"fmt"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/spf13/viper"
)

// Database holds the primary DB plus the shared Redis connection.
type Database struct {
	Driver  string `mapstructure:"DB_CONNECTION"`
	Host    string `mapstructure:"DB_HOST"`
	Port    string `mapstructure:"DB_PORT"`
	Name    string `mapstructure:"DB_DATABASE"`
	User    string `mapstructure:"DB_USERNAME"`
	Pass    string `mapstructure:"DB_PASSWORD"`
	LogMode string `mapstructure:"DB_LOG_MODE"`
	// Pool bounds are shared by every replica against the same database:
	// size them for replica count, not just one process.
	MaxOpenConns    int `mapstructure:"DB_MAX_OPEN_CONNS"`
	MaxIdleConns    int `mapstructure:"DB_MAX_IDLE_CONNS"`
	ConnMaxLifetime int `mapstructure:"DB_CONN_MAX_LIFETIME_SEC"`
	ConnMaxIdleTime int `mapstructure:"DB_CONN_MAX_IDLE_TIME_SEC"`
	// SSLMode controls transport encryption. Only verify-full/verify-ca (and
	// verify_identity) verify; require/preferred/true mean encrypted but
	// unverified and are rejected in production. See Validate.
	SSLMode string `mapstructure:"DB_SSLMODE"`
	// SSLRootCert is an optional PEM bundle added to the system CA pool.
	SSLRootCert string `mapstructure:"DB_SSLROOTCERT"`
	// SSLServerName overrides the TLS ServerName. Empty means DB_HOST.
	SSLServerName string `mapstructure:"DB_SSLSERVERNAME"`

	Redis Redis `mapstructure:",squash"`
}

// Redis holds the shared Redis connection.
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
	v.SetDefault("DB_MAX_OPEN_CONNS", 20)
	v.SetDefault("DB_MAX_IDLE_CONNS", 5)
	v.SetDefault("DB_CONN_MAX_LIFETIME_SEC", 1800)
	v.SetDefault("DB_CONN_MAX_IDLE_TIME_SEC", 300)
	v.SetDefault("DB_SSLMODE", "disable")
	v.SetDefault("DB_SSLROOTCERT", "")
	v.SetDefault("DB_SSLSERVERNAME", "")
	v.SetDefault("REDIS_HOST", "127.0.0.1")
	v.SetDefault("REDIS_PORT", "6379")
	v.SetDefault("REDIS_PASSWORD", "")
	v.SetDefault("REDIS_USERNAME", "")
	v.SetDefault("REDIS_DB", 0)
}

// mysqlVerifyProfile is the only MySQL TLS profile that verifies.
const mysqlVerifyProfile = "ginplate-verify"

// registerMySQLVerifyTLS registers the verifying MySQL TLS profile
// (re-registration overwrites, so repeated DSN builds are safe).
func registerMySQLVerifyTLS(serverName, rootCertPEM string) error {
	pool, err := x509.SystemCertPool()
	if err != nil || pool == nil {
		pool = x509.NewCertPool()
	}
	if strings.TrimSpace(rootCertPEM) != "" {
		pem, err := os.ReadFile(rootCertPEM)
		if err != nil {
			return fmt.Errorf("db: read DB_SSLROOTCERT %q: %w", rootCertPEM, err)
		}
		if !pool.AppendCertsFromPEM(pem) {
			return fmt.Errorf("db: no valid certificates in DB_SSLROOTCERT %q", rootCertPEM)
		}
	}
	if strings.TrimSpace(serverName) == "" {
		return fmt.Errorf("db: empty TLS server name")
	}
	return mysql.RegisterTLSConfig(mysqlVerifyProfile, &tls.Config{
		RootCAs:    pool,
		ServerName: serverName,
		MinVersion: tls.VersionTLS12,
	})
}

// ValidateTLS registers the verifying MySQL TLS profile when DB_SSLMODE
// asks for verification. Call at boot: verifying configs must fail fast on
// bad CA/server names instead of silently downgrading.
func (d Database) ValidateTLS() error {
	if normalizeDriver(d.Driver) != "mysql" {
		return nil
	}
	switch strings.ToLower(strings.TrimSpace(d.SSLMode)) {
	case "verify-full", "verify-ca", "verify_identity":
		serverName := strings.TrimSpace(d.SSLServerName)
		if serverName == "" {
			serverName = d.Host
		}
		if err := registerMySQLVerifyTLS(serverName, strings.TrimSpace(d.SSLRootCert)); err != nil {
			return fmt.Errorf("config: DB_SSLMODE verification: %w", err)
		}
	}
	return nil
}

// mysqlTLSParam maps DB_SSLMODE to a `tls` DSN profile name.
func (d Database) mysqlTLSParam() string {
	switch strings.ToLower(strings.TrimSpace(d.SSLMode)) {
	case "verify-full", "verify-ca", "verify_identity":
		serverName := strings.TrimSpace(d.SSLServerName)
		if serverName == "" {
			serverName = d.Host
		}
		if err := registerMySQLVerifyTLS(serverName, strings.TrimSpace(d.SSLRootCert)); err != nil {
			// Never downgrade: return the verify profile anyway so the
			// driver fails to connect instead of going unverified. Boot
			// validation (ValidateTLS) catches this first with a clear error.
			return mysqlVerifyProfile
		}
		return mysqlVerifyProfile
	case "require", "preferred", "true":
		return "skip-verify"
	case "skip-verify":
		return "skip-verify"
	default:
		return "false"
	}
}

// DSN assembles the GORM DSN from parts (passwords may contain @ : / ? %).
func (d Database) DSN() string {
	sslMode := strings.TrimSpace(d.SSLMode)
	if sslMode == "" {
		sslMode = "disable"
	}
	if normalizeDriver(d.Driver) == "pgsql" {
		u := &url.URL{
			Scheme: "postgres",
			User:   url.UserPassword(d.User, d.Pass),
			Host:   d.Host + ":" + d.Port,
			Path:   "/" + d.Name,
		}
		q := u.Query()
		q.Set("sslmode", sslMode)
		u.RawQuery = q.Encode()
		return u.String()
	}
	mc := mysql.NewConfig()
	mc.User = d.User
	mc.Passwd = d.Pass
	mc.Net = "tcp"
	mc.Addr = d.Host + ":" + d.Port
	mc.DBName = d.Name
	mc.ParseTime = true
	mc.Loc = time.Local
	mc.Params = map[string]string{"charset": "utf8mb4"}
	mc.TLSConfig = d.mysqlTLSParam()
	return mc.FormatDSN()
}

func normalizeDriver(s string) string {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "pgsql", "postgres", "postgresql", "pgx":
		return "pgsql"
	default:
		return "mysql"
	}
}

func (r Redis) Addr() string {
	return r.Host + ":" + r.Port
}
