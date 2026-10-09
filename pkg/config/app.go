package config

import (
	"strings"

	"github.com/spf13/viper"
)

// App holds application-level settings.
type App struct {
	Name  string `mapstructure:"APP_NAME"`
	Env   string `mapstructure:"APP_ENV"`
	Debug bool   `mapstructure:"APP_DEBUG"`
	Port  string `mapstructure:"APP_PORT"`
	URL   string `mapstructure:"APP_URL"`

	HTTP    HTTP `mapstructure:",squash"`
	Swagger bool `mapstructure:"ENABLE_SWAGGER"`
	// Metrics exposes /metrics publicly; disable it when scrapes run behind
	// a private network or sidecar instead. /livez and /readyz stay public
	// for orchestrator probes.
	Metrics     bool        `mapstructure:"ENABLE_METRICS"`
	Maintenance Maintenance `mapstructure:",squash"`
}

// HTTP holds server timeouts and edge hardening.
type HTTP struct {
	ReadTimeoutSec     int `mapstructure:"HTTP_READ_TIMEOUT_SEC"`
	WriteTimeoutSec    int `mapstructure:"HTTP_WRITE_TIMEOUT_SEC"`
	IdleTimeoutSec     int `mapstructure:"HTTP_IDLE_TIMEOUT_SEC"`
	ShutdownTimeoutSec int `mapstructure:"SHUTDOWN_TIMEOUT_SEC"`
	// MaxBodyBytes caps request body size (0 disables the limit).
	MaxBodyBytes       int64   `mapstructure:"HTTP_MAX_BODY_BYTES"`
	CORSAllowedOrigins string  `mapstructure:"CORS_ALLOWED_ORIGINS"`
	RateLimitRPS       float64 `mapstructure:"RATE_LIMIT_RPS"`
	EnableGzip         bool    `mapstructure:"ENABLE_GZIP"`
	TLSCertFile        string  `mapstructure:"TLS_CERT_FILE"`
	TLSKeyFile         string  `mapstructure:"TLS_KEY_FILE"`
}

// Maintenance holds maintenance-mode settings (file driver only).
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
	v.SetDefault("HTTP_MAX_BODY_BYTES", 1<<20)
	v.SetDefault("CORS_ALLOWED_ORIGINS", "")
	v.SetDefault("RATE_LIMIT_RPS", 10)
	v.SetDefault("ENABLE_GZIP", true)
	v.SetDefault("TLS_CERT_FILE", "")
	v.SetDefault("TLS_KEY_FILE", "")
	v.SetDefault("ENABLE_SWAGGER", false)
	v.SetDefault("ENABLE_METRICS", false)
	v.SetDefault("APP_MAINTENANCE_DRIVER", "file")
	v.SetDefault("APP_MAINTENANCE_PATH", "storage/framework/down")
}

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
