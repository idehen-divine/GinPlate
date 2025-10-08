package config

import (
	"github.com/spf13/viper"
)

// Queue holds background-job settings: which broker and how often a job
// may run before it is buried.
type Queue struct {
	// Connection names the queue backend: "sync" (run inline, no broker),
	// "redis" (list-based), or "database" (jobs table).
	Connection string `mapstructure:"QUEUE_CONNECTION"`
	// Tries caps total runs per job, first attempt included (min 1).
	Tries int `mapstructure:"QUEUE_TRIES"`
}

func applyQueueDefaults(v *viper.Viper) {
	v.SetDefault("QUEUE_CONNECTION", "sync")
	v.SetDefault("QUEUE_TRIES", 3)
}
