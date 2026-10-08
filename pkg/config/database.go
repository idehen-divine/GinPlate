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
	// SSLMode controls transport encryption. PostgreSQL maps to the
	// `sslmode` URL parameter. MySQL maps to a
	// `tls` DSN profile: false (plaintext), skip-verify (encrypted,
	// unverified), or ginplate-verify (encrypted AND verified against
	// system CAs plus DB_SSLROOTCERT, with SNI/verification against
	// DB_SSLSERVERNAME or DB_HOST). verify-full and verify-ca both select
	// the verifying profile; require/preferred/true select skip-verify and
	// are rejected in production, because "encrypted" must never be mistaken
	// for "verified". See Validate.
	SSLMode string `mapstructure:"DB_SSLMODE"`
	// SSLRootCert is an optional PEM bundle appended to the system CA pool
	// for MySQL verification (e.g. a private or RDS CA). Empty means the
	// system pool alone.
	SSLRootCert string `mapstructure:"DB_SSLROOTCERT"`
	// SSLServerName overrides the TLS ServerName used for MySQL
	// verification. Empty means DB_HOST.
	SSLServerName string `mapstructure:"DB_SSLSERVERNAME"`

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
	v.SetDefault("DB_SSLMODE", "disable")
	v.SetDefault("DB_SSLROOTCERT", "")
	v.SetDefault("DB_SSLSERVERNAME", "")
	v.SetDefault("REDIS_HOST", "127.0.0.1")
	v.SetDefault("REDIS_PORT", "6379")
	v.SetDefault("REDIS_PASSWORD", "")
	v.SetDefault("REDIS_USERNAME", "")
	v.SetDefault("REDIS_DB", 0)
}

// mysqlVerifyProfile is the registered MySQL TLS profile that encrypts
// AND verifies: system CAs plus DB_SSLROOTCERT, ServerName verification,
// InsecureSkipVerify left false. It is the only MySQL profile that counts
// as verified; tls=true/skip-verify do not verify.
const mysqlVerifyProfile = "ginplate-verify"

// registerMySQLVerifyTLS registers (or re-registers) the verifying MySQL
// TLS profile for serverName with the system CA pool plus rootCertPEM.
// Re-registration overwrites the same key, so repeated DSN builds with one
// configuration are safe. mysql.RegisterTLSConfig is internally locked.
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

// mysqlTLSParam maps DB_SSLMODE to a `tls` DSN profile name. Verification
// is explicit: only verify-full/verify-ca/verify_identity select the
// verifying profile (registered as a side effect); require/preferred/true
// mean "encrypted, unverified" and map to skip-verify; everything else is
// plaintext. Production accepts only the verifying modes (see Validate).
func (d Database) mysqlTLSParam() string {
	switch strings.ToLower(strings.TrimSpace(d.SSLMode)) {
	case "verify-full", "verify-ca", "verify_identity":
		serverName := strings.TrimSpace(d.SSLServerName)
		if serverName == "" {
			serverName = d.Host
		}
		if err := registerMySQLVerifyTLS(serverName, strings.TrimSpace(d.SSLRootCert)); err != nil {
			// DSN has no error return and Validate rejects unreadable CA
			// paths at boot; fall back to unverified rather than plaintext
			// so a late failure still encrypts.
			return "skip-verify"
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

// DSN assembles the GORM DSN from parts. MySQL is built with the driver's
// mysql.Config so credentials follow driver parsing rules (passwords may
// contain @ : / ? % and spaces; the username portion may not contain ':',
// which is the user/password separator by DSN grammar); Postgres uses URL
// form with url.UserPassword (required for app-role swapping on pgsql).
// Transport encryption follows DB_SSLMODE.
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
