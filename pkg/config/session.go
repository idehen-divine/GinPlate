package config

import (
	"github.com/spf13/viper"
)

// Session holds session settings.
type Session struct {
	Driver      string `mapstructure:"SESSION_DRIVER"`
	LifetimeMin int    `mapstructure:"SESSION_LIFETIME"`
	Encrypt     bool   `mapstructure:"SESSION_ENCRYPT"`
	Path        string `mapstructure:"SESSION_PATH"`
	Domain      string `mapstructure:"SESSION_DOMAIN"`
}

func applySessionDefaults(v *viper.Viper) {
	v.SetDefault("SESSION_DRIVER", "database")
	v.SetDefault("SESSION_LIFETIME", 120)
	v.SetDefault("SESSION_ENCRYPT", false)
	v.SetDefault("SESSION_PATH", "/")
	v.SetDefault("SESSION_DOMAIN", "")
}
