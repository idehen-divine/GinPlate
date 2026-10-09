package config

import (
	"github.com/spf13/viper"
)

// Filesystem holds file storage settings.
type Filesystem struct {
	// Disk selects the default disk: "local", "public", or "s3".
	Disk       string `mapstructure:"FILESYSTEM_DISK"`
	Root       string `mapstructure:"FILESYSTEM_ROOT"`
	PublicRoot string `mapstructure:"FILESYSTEM_PUBLIC_ROOT"`
	// PublicURL is the public disk base URL (empty resolves to APP_URL + "/storage").
	PublicURL string `mapstructure:"FILESYSTEM_PUBLIC_URL"`

	S3 S3 `mapstructure:",squash"`
}

// S3 holds object-storage credentials (standard AWS_* keys; empty Key falls
// back to the SDK default credential chain).
type S3 struct {
	Key       string `mapstructure:"AWS_ACCESS_KEY_ID"`
	Secret    string `mapstructure:"AWS_SECRET_ACCESS_KEY"`
	Region    string `mapstructure:"AWS_DEFAULT_REGION"`
	Bucket    string `mapstructure:"AWS_BUCKET"`
	PathStyle bool   `mapstructure:"AWS_USE_PATH_STYLE_ENDPOINT"`
}

func applyFilesystemDefaults(v *viper.Viper) {
	v.SetDefault("FILESYSTEM_DISK", "local")
	v.SetDefault("FILESYSTEM_ROOT", "storage/app")
	v.SetDefault("FILESYSTEM_PUBLIC_ROOT", "storage/app/public")
	v.SetDefault("FILESYSTEM_PUBLIC_URL", "")
	v.SetDefault("AWS_ACCESS_KEY_ID", "")
	v.SetDefault("AWS_SECRET_ACCESS_KEY", "")
	v.SetDefault("AWS_DEFAULT_REGION", "")
	v.SetDefault("AWS_BUCKET", "")
	v.SetDefault("AWS_USE_PATH_STYLE_ENDPOINT", false)
}
