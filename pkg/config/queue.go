package config

import (
	"github.com/spf13/viper"
)

// Queue holds background-job settings.
type Queue struct {
	// Connection names the queue backend: "sync", "redis", or "database".
	Connection string `mapstructure:"QUEUE_CONNECTION"`
	// Tries caps total runs per job, first attempt included.
	Tries int `mapstructure:"QUEUE_TRIES"`
}

func applyQueueDefaults(v *viper.Viper) {
	v.SetDefault("QUEUE_CONNECTION", "sync")
	v.SetDefault("QUEUE_TRIES", 3)
}
