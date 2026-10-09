package config

import (
	"github.com/spf13/viper"
)

// Cache holds cache settings (Redis address comes from Database.Redis).
type Cache struct {
	Store  string `mapstructure:"CACHE_STORE"`
	Prefix string `mapstructure:"CACHE_PREFIX"`
}

func applyCacheDefaults(v *viper.Viper) {
	v.SetDefault("CACHE_STORE", "database")
	v.SetDefault("CACHE_PREFIX", "")
}
