// Package config loads `.env`, environment variables, and code defaults
// into one Config. `Load` is the only entry point.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// pathWithin reports whether target sits inside root (or equals it).
func pathWithin(root, target string) (bool, error) {
	absRoot, err := filepath.Abs(root)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", root, err)
	}
	absTarget, err := filepath.Abs(target)
	if err != nil {
		return false, fmt.Errorf("resolve %q: %w", target, err)
	}
	rel, err := filepath.Rel(absRoot, absTarget)
	if err != nil {
		return false, err
	}
	return rel != ".." && !strings.HasPrefix(rel, ".."+string(filepath.Separator)), nil
}

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

// bindEnvKeys registers every mapstructure key so Unmarshal sees env vars
// (AutomaticEnv alone only affects Get).
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

func hasMapstructureTags(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if tag, ok := t.Field(i).Tag.Lookup("mapstructure"); ok && tag != "" {
			return true
		}
	}
	return false
}

// nullHook decodes "null"/"" as the zero value (for intentionally-unset optionals).
func nullHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		s, ok := data.(string)
		if !ok || (s != "" && !strings.EqualFold(s, "null")) {
			return data, nil
		}
		return reflect.Zero(t).Interface(), nil
	}
}

// Load reads `.env` (optional) + env vars + defaults into Config. Fails fast
// on a missing/short APP_KEY.
func Load() (*Config, error) {
	v := viper.New()
	if envFile := strings.TrimSpace(os.Getenv("DOTENV_PATH")); envFile != "" {
		v.SetConfigFile(envFile)
	} else {
		v.SetConfigFile(".env")
	}
	v.SetConfigType("env")
	// A present-but-malformed .env fails loudly instead of falling back.
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
	// Expand ${VAR} against merged config first, OS env second.
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

// IsHardenedEnv reports environments that must run hardened: production and
// staging alike (staging often holds real data, so it gets no weaker rules).
func IsHardenedEnv(env string) bool {
	switch strings.ToLower(strings.TrimSpace(env)) {
	case "production", "prod", "staging", "stage":
		return true
	}
	return false
}

// Validate rejects unsafe configuration with actionable errors in hardened
// environments.
func (c *Config) Validate() error {
	if rc := strings.TrimSpace(c.Database.SSLRootCert); rc != "" {
		if st, err := os.Stat(rc); err != nil || st.IsDir() {
			return fmt.Errorf("config: DB_SSLROOTCERT %q unreadable: %w", rc, err)
		}
	}
	// Verifying TLS modes register at boot in every environment: a broken
	// CA/server name must fail startup, never silently downgrade.
	if err := c.Database.ValidateTLS(); err != nil {
		return err
	}
	// The public disk subtree is served over HTTP: sessions, the maintenance
	// marker, and logs must never live under it, or they become downloadable.
	publicRoot := strings.TrimSpace(c.Filesystem.PublicRoot)
	if publicRoot == "" {
		publicRoot = "storage/app/public"
	}
	for _, p := range []string{
		strings.TrimSpace(c.App.Maintenance.Path),
		strings.TrimSpace(c.Logging.Output),
	} {
		if p == "" {
			continue
		}
		inside, err := pathWithin(publicRoot, p)
		if err != nil {
			return fmt.Errorf("config: %w", err)
		}
		if inside {
			return fmt.Errorf("config: %q must not live under the public disk root %q", p, publicRoot)
		}
	}
	if !IsHardenedEnv(c.App.Env) {
		return nil
	}
	var errs []string
	fail := func(format string, args ...any) { errs = append(errs, fmt.Sprintf(format, args...)) }
	if c.App.Debug {
		fail("APP_DEBUG=true with APP_ENV=%s (hardened)", c.App.Env)
	}
	if c.App.Swagger {
		fail("ENABLE_SWAGGER=true with APP_ENV=%s (hardened; disable or gate behind an internal boundary)", c.App.Env)
	}
	u, err := url.Parse(strings.TrimSpace(c.App.URL))
	if err != nil || !strings.EqualFold(u.Scheme, "https") || u.Host == "" {
		fail("APP_URL must be an absolute https URL in a hardened environment, got %q", c.App.URL)
	}
	if len(c.App.HTTP.CORSOrigins()) == 0 {
		fail("CORS_ALLOWED_ORIGINS must list explicit origins in a hardened environment (empty means allow-all)")
	}
	for _, o := range c.App.HTTP.CORSOrigins() {
		ou, err := url.Parse(o)
		if err != nil || !strings.EqualFold(ou.Scheme, "https") || ou.Host == "" {
			fail("CORS origin %q must be an absolute https URL", o)
		}
	}
	// Only verifying modes count: "encrypted" must never mean "verified".
	switch strings.ToLower(strings.TrimSpace(c.Database.SSLMode)) {
	case "verify-full", "verify-ca", "verify_identity":
	default:
		fail("DB_SSLMODE must verify certificates in a hardened environment (verify-full or verify-ca), got %q", c.Database.SSLMode)
	}
	if c.Database.LogMode == "info" {
		fail("DB_LOG_MODE=info logs query parameters in a hardened environment (use warn or error)")
	}
	if c.Mail.Mailer == "log" {
		fail("MAIL_MAILER=log discards mail in a hardened environment")
	}
	if c.Session.Driver == "redis" && strings.TrimSpace(c.Database.Redis.Host) == "" {
		fail("SESSION_DRIVER=redis needs REDIS_HOST in a hardened environment")
	}
	if c.App.HTTP.ReadTimeoutSec <= 0 || c.App.HTTP.WriteTimeoutSec <= 0 ||
		c.App.HTTP.IdleTimeoutSec <= 0 || c.App.HTTP.ShutdownTimeoutSec <= 0 {
		fail("HTTP_*_TIMEOUT_SEC and SHUTDOWN_TIMEOUT_SEC must be positive in a hardened environment")
	}
	if c.Database.MaxOpenConns <= 0 || c.Database.MaxIdleConns <= 0 ||
		c.Database.ConnMaxLifetime <= 0 || c.Database.ConnMaxIdleTime <= 0 {
		fail("DB pool settings must be positive in a hardened environment")
	}
	if p, err := strconv.Atoi(strings.TrimSpace(c.App.Port)); err != nil || p < 1 || p > 65535 {
		fail("APP_PORT must be a valid port in a hardened environment, got %q", c.App.Port)
	}
	if (c.App.HTTP.TLSCertFile == "") != (c.App.HTTP.TLSKeyFile == "") {
		fail("TLS_CERT_FILE and TLS_KEY_FILE must both be set for direct HTTPS")
	}
	if len(errs) > 0 {
		return fmt.Errorf("config: unsafe %s configuration:\n - %s", c.App.Env, strings.Join(errs, "\n - "))
	}
	return nil
}
