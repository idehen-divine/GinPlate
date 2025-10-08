package config

import (
	"github.com/spf13/viper"
)

// Session holds session settings. Token sessions live in Redis today;
// these values are read for future cookie/database session transports.
// Config first, implementation later.
type Session struct {
	// Driver names the session backend: "redis" or "database".
	Driver string `mapstructure:"SESSION_DRIVER"`
	// LifetimeMin is the session lifetime in minutes.
	LifetimeMin int `mapstructure:"SESSION_LIFETIME"`
	// Encrypt toggles payload encryption for cookie transports.
	Encrypt bool `mapstructure:"SESSION_ENCRYPT"`
	// Path and Domain scope session cookies. The literal "null" in .env
	// files normalizes to unset (see nullKeys in config.go).
	Path   string `mapstructure:"SESSION_PATH"`
	Domain string `mapstructure:"SESSION_DOMAIN"`
}

func applySessionDefaults(v *viper.Viper) {
	v.SetDefault("SESSION_DRIVER", "database")
	v.SetDefault("SESSION_LIFETIME", 120)
	v.SetDefault("SESSION_ENCRYPT", false)
	v.SetDefault("SESSION_PATH", "/")
	v.SetDefault("SESSION_DOMAIN", "")
}
