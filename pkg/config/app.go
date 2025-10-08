package config

import (
	"strings"

	"github.com/spf13/viper"
)

// App holds application-level settings: identity, server, HTTP hardening,
// timeouts, Swagger, and maintenance mode.
// Maintenance lives here (not its own file): it is an app-level concern
// with two env keys.
type App struct {
	Name  string `mapstructure:"APP_NAME"`
	Env   string `mapstructure:"APP_ENV"`
	Debug bool   `mapstructure:"APP_DEBUG"`
	Port  string `mapstructure:"APP_PORT"`
	URL   string `mapstructure:"APP_URL"`

	HTTP        HTTP        `mapstructure:",squash"`
	Swagger     bool        `mapstructure:"ENABLE_SWAGGER"`
	Maintenance Maintenance `mapstructure:",squash"`
}

// HTTP holds server timeouts and edge hardening. CORSAllowedOrigins is a
// comma-separated list; empty means allow all (local dev only).
type HTTP struct {
	ReadTimeoutSec     int     `mapstructure:"HTTP_READ_TIMEOUT_SEC"`
	WriteTimeoutSec    int     `mapstructure:"HTTP_WRITE_TIMEOUT_SEC"`
	IdleTimeoutSec     int     `mapstructure:"HTTP_IDLE_TIMEOUT_SEC"`
	ShutdownTimeoutSec int     `mapstructure:"SHUTDOWN_TIMEOUT_SEC"`
	CORSAllowedOrigins string  `mapstructure:"CORS_ALLOWED_ORIGINS"`
	RateLimitRPS       float64 `mapstructure:"RATE_LIMIT_RPS"`
	EnableGzip         bool    `mapstructure:"ENABLE_GZIP"`
}

// Maintenance holds maintenance-mode settings. Only the file driver is
// supported: `ginplate down` writes Path, the middleware 503s while it
// exists, `ginplate up` removes it.
type Maintenance struct {
	Driver string `mapstructure:"APP_MAINTENANCE_DRIVER"`
	Path   string `mapstructure:"APP_MAINTENANCE_PATH"`
}

func applyAppDefaults(v *viper.Viper) {
	v.SetDefault("APP_NAME", "GinPlate")
	v.SetDefault("APP_ENV", "local")
	v.SetDefault("APP_DEBUG", false)
	v.SetDefault("APP_PORT", "8080")
	v.SetDefault("APP_URL", "http://127.0.0.1:8080")
	v.SetDefault("HTTP_READ_TIMEOUT_SEC", 15)
	v.SetDefault("HTTP_WRITE_TIMEOUT_SEC", 15)
	v.SetDefault("HTTP_IDLE_TIMEOUT_SEC", 60)
	v.SetDefault("SHUTDOWN_TIMEOUT_SEC", 5)
	v.SetDefault("CORS_ALLOWED_ORIGINS", "")
	v.SetDefault("RATE_LIMIT_RPS", 10)
	v.SetDefault("ENABLE_GZIP", true)
	v.SetDefault("ENABLE_SWAGGER", false)
	v.SetDefault("APP_MAINTENANCE_DRIVER", "file")
	v.SetDefault("APP_MAINTENANCE_PATH", "storage/framework/down")
}

// CORSOrigins parses CORSAllowedOrigins into a list. Empty means allow all.
func (h HTTP) CORSOrigins() []string {
	if h.CORSAllowedOrigins == "" {
		return nil
	}
	var out []string
	for _, o := range strings.Split(h.CORSAllowedOrigins, ",") {
		o = strings.TrimSpace(o)
		if o != "" {
			out = append(out, o)
		}
	}
	return out
}
