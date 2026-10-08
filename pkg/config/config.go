// Package config loads the application configuration from `.env`,
// environment variables, and code defaults.
//
// Layout mirrors Laravel's config directory: one file per domain, each
// exposing a nested struct decoded from flat `ENV_KEY` names. Existing key
// names are stable; new domains add new keys. `Load` is the only entry
// point: it reads `.env` (optional, overridden by real environment
// variables), applies per-domain defaults, normalizes `"null"` placeholders
// to unset, decodes once, then resolves secrets.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"reflect"
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// Config is the whole application configuration, one nested struct per
// config/<domain>.go file.
type Config struct {
	App         App         `mapstructure:",squash"`
	Auth        Auth        `mapstructure:",squash"`
	Database    Database    `mapstructure:",squash"`
	Cache       Cache       `mapstructure:",squash"`
	Filesystem  Filesystem  `mapstructure:",squash"`
	Mail        Mail        `mapstructure:",squash"`
	Queue       Queue       `mapstructure:",squash"`
	Logging     Logging     `mapstructure:",squash"`
	Session     Session     `mapstructure:",squash"`
	Services    Services    `mapstructure:",squash"`
	Maintenance Maintenance `mapstructure:",squash"`
}

// bindEnvKeys registers every mapstructure key so Unmarshal sees
// environment variables. AutomaticEnv alone only affects Get — without
// this, env-only keys would silently decode as zero values. Nested structs
// use `,squash`, so their fields decode from top-level keys; this walks
// into them (a bare `,squash` tag carries no name to bind). It descends
// only into structs that carry mapstructure tags, so stdlib types like
// time.Time are never touched.
func bindEnvKeys(v *viper.Viper, t reflect.Type) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if tag, ok := f.Tag.Lookup("mapstructure"); ok {
			if name, _, _ := strings.Cut(tag, ","); name != "" {
				_ = v.BindEnv(name)
				continue
			}
		}
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && hasMapstructureTags(ft) {
			bindEnvKeys(v, ft)
		}
	}
}

// hasMapstructureTags reports whether any direct field of t carries a
// mapstructure tag.
func hasMapstructureTags(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if tag, ok := t.Field(i).Tag.Lookup("mapstructure"); ok && tag != "" {
			return true
		}
	}
	return false
}

// nullHook decodes the literal string "null" (and "") as the zero value.
// `.env` files use `KEY=null` for intentionally-unset optionals, but viper
// would otherwise hand e.g. MAIL_PORT the string "null" and decoding it
// into an int would fail the whole Load.
func nullHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		s, ok := data.(string)
		if !ok || (s != "" && !strings.EqualFold(s, "null")) {
			return data, nil
		}
		return reflect.Zero(t).Interface(), nil
	}
}

// Load reads `.env` (optional), overlays environment variables, applies
// defaults, and decodes the result into Config. It fails fast when APP_KEY
// is missing or too short, so the app can never sign tokens with an empty
// or weak secret.
func Load() (*Config, error) {
	v := viper.New()
	if envFile := strings.TrimSpace(os.Getenv("DOTENV_PATH")); envFile != "" {
		v.SetConfigFile(envFile)
	} else {
		v.SetConfigFile(".env")
	}
	v.SetConfigType("env")
	// .env is optional, but a present-but-malformed file must fail loudly
	// instead of silently falling back to defaults.
	if err := v.ReadInConfig(); err != nil {
		var notFound viper.ConfigFileNotFoundError
		if !errors.As(err, &notFound) && !os.IsNotExist(err) {
			return nil, fmt.Errorf("read config: %w", err)
		}
	}
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	applyAppDefaults(v)
	applyAuthDefaults(v)
	applyDatabaseDefaults(v)
	applyCacheDefaults(v)
	applyFilesystemDefaults(v)
	applyMailDefaults(v)
	applyQueueDefaults(v)
	applyLoggingDefaults(v)
	applySessionDefaults(v)

	bindEnvKeys(v, reflect.TypeOf(Config{}))

	var c Config
	if err := v.Unmarshal(&c, viper.DecodeHook(nullHook())); err != nil {
		return nil, err
	}
	// Expand ${VAR} against merged config first (so .env values reference
	// each other, e.g. MAIL_FROM_NAME="${APP_NAME} Team"), OS env second.
	lookup := func(key string) string {
		if s := v.GetString(key); s != "" {
			return s
		}
		return os.Getenv(key)
	}
	c.Mail.From.Name = os.Expand(c.Mail.From.Name, lookup)
	c.Mail.From.Address = os.Expand(c.Mail.From.Address, lookup)
	if c.Filesystem.PublicURL == "" {
		c.Filesystem.PublicURL = strings.TrimSuffix(c.App.URL, "/") + "/storage"
	}
	if _, err := c.Auth.JWT.KeyBytes(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	if err := c.Validate(); err != nil {
		return nil, err
	}
	return &c, nil
}

// isProd reports production mode case-insensitively so APP_ENV=Prod,
// PRODUCTION, and prod cannot bypass production guardrails.
func isProd(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "production", "prod":
		return true
	}
	return false
}

// Validate rejects unsafe production configuration with actionable errors.
// Non-production environments keep permissive local defaults, except for
// the TLS CA path: a set-but-unreadable DB_SSLROOTCERT fails fast in every
// environment so the error surfaces at boot, not at first connection.
func (c *Config) Validate() error {
	if rc := strings.TrimSpace(c.Database.SSLRootCert); rc != "" {
		if st, err := os.Stat(rc); err != nil || st.IsDir() {
			return fmt.Errorf("config: DB_SSLROOTCERT %q unreadable: %w", rc, err)
		}
	}
	if !isProd(c.App.Env) {
		return nil
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if c.App.Debug {
		fail("APP_DEBUG=true with APP_ENV=production")
	}
	if c.App.Swagger {
		fail("ENABLE_SWAGGER=true with APP_ENV=production (disable or gate behind an internal boundary)")
	}
	u, err := url.Parse(strings.TrimSpace(c.App.URL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		fail("APP_URL must be an absolute https URL in production, got %q", c.App.URL)
	}
	if len(c.App.HTTP.CORSOrigins()) == 0 {
		fail("CORS_ALLOWED_ORIGINS must list explicit origins in production (empty means allow-all)")
	}
	for _, o := range c.App.HTTP.CORSOrigins() {
		ou, err := url.Parse(o)
		if err != nil || !strings.EqualFold(ou.Scheme, "https") || ou.Host == "" {
			fail("CORS origin %q must be an absolute https URL", o)
		}
	}
	// Only verifying modes count in production: require/skip-verify/true
	// encrypt without verifying (MySQL) or leave verification ambiguous,
	// so "encrypted" can never be mistaken for "verified".
	switch strings.ToLower(strings.TrimSpace(c.Database.SSLMode)) {
	case "verify-full", "verify-ca", "verify_identity":
	default:
		fail("DB_SSLMODE must verify certificates in production (verify-full or verify-ca), got %q", c.Database.SSLMode)
	}
	if c.Database.LogMode == "info" {
		fail("DB_LOG_MODE=info logs query parameters in production (use warn or error)")
	}
	if c.Mail.Mailer == "log" {
		fail("MAIL_MAILER=log discards mail in production")
	}
	if c.Session.Driver == "redis" && strings.TrimSpace(c.Database.Redis.Host) == "" {
		fail("SESSION_DRIVER=redis needs REDIS_HOST in production")
	}
	if c.App.HTTP.ReadTimeoutSec <= 0 || c.App.HTTP.WriteTimeoutSec <= 0 ||
		c.App.HTTP.IdleTimeoutSec <= 0 || c.App.HTTP.ShutdownTimeoutSec <= 0 {
		fail("HTTP_*_TIMEOUT_SEC and SHUTDOWN_TIMEOUT_SEC must be positive in production")
	}
	if p, err := strconv.Atoi(strings.TrimSpace(c.App.Port)); err != nil || p < 1 || p > 65535 {
		fail("APP_PORT must be a valid port in production, got %q", c.App.Port)
	}
	if (c.App.HTTP.TLSCertFile == "") != (c.App.HTTP.TLSKeyFile == "") {
		fail("TLS_CERT_FILE and TLS_KEY_FILE must both be set for direct HTTPS")
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: unsafe production configuration:\n - %s", strings.Join(errs, "\n - "))
	}
	return nil
}
