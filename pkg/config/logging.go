package config

import (
	"github.com/spf13/viper"
)

// Logging holds log verbosity and output.
type Logging struct {
	// Level is debug, info, warn, or error.
	Level string `mapstructure:"LOG_LEVEL"`
	// Output is the log directory holding YYYY-MM-DD.logs daily files
	// (retained 7 days). Empty means stdout only.
	Output string `mapstructure:"LOG_OUTPUT"`
}

func applyLoggingDefaults(v *viper.Viper) {
	v.SetDefault("LOG_LEVEL", "info")
	v.SetDefault("LOG_OUTPUT", "storage/logs")
}
