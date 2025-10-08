package config

import (
	"github.com/spf13/viper"
)

// Cache holds cache settings. Only the store name and key prefix are
// configured here; the Redis address comes from Database.Redis.
type Cache struct {
	Store  string `mapstructure:"CACHE_STORE"`
	Prefix string `mapstructure:"CACHE_PREFIX"`
}

func applyCacheDefaults(v *viper.Viper) {
	v.SetDefault("CACHE_STORE", "database")
	v.SetDefault("CACHE_PREFIX", "")
}
