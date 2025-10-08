package config

import (
	"github.com/spf13/viper"
)

// Filesystem holds file storage settings: one default disk plus per-disk
// roots. Only implemented drivers serve traffic; the rest fail fast.
type Filesystem struct {
	// Disk selects the default disk: "local" (private), "public"
	// (URL-servable local files), or "s3".
	Disk string `mapstructure:"FILESYSTEM_DISK"`
	// Root is the local disk base directory.
	Root string `mapstructure:"FILESYSTEM_ROOT"`
	// PublicRoot is the public disk base directory, served under PublicURL.
	PublicRoot string `mapstructure:"FILESYSTEM_PUBLIC_ROOT"`
	// PublicURL is the public disk base URL. Empty resolves to
	// APP_URL + "/storage" at load.
	PublicURL string `mapstructure:"FILESYSTEM_PUBLIC_URL"`

	S3 S3 `mapstructure:",squash"`
}

// S3 holds object-storage credentials for the s3 disk driver.
// Values come from the standard AWS_* environment keys. Empty Key falls
// back to the SDK default credential chain (env, shared config, IAM role).
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
