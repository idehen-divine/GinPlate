package config

import (
	"github.com/spf13/viper"
)

type Logging struct {
	Level string `mapstructure:"LOG_LEVEL"`
	// Output is the log directory (daily files, 7-day retention); empty means stdout only.
	Output string `mapstructure:"LOG_OUTPUT"`
}

func applyLoggingDefaults(v *viper.Viper) {
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_OUTPUT", "storage/logs")
}
