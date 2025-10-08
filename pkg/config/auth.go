package config

import (
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/spf13/viper"
)

// Auth holds authentication settings. The JWT signing secret comes from
// APP_KEY (Laravel convention): either a raw string or `base64:...` encoded.
// Refresh tokens live a fixed 30 days (RefreshTTL); only the access TTL is
// configurable via APP_TTL_MIN.
type Auth struct {
	JWT JWT `mapstructure:",squash"`
}

// JWT holds token settings. Secret is never used raw when it carries the
// `base64:` prefix — call KeyBytes.
type JWT struct {
	Secret       string `mapstructure:"APP_KEY"`
	AccessTTLMin int    `mapstructure:"APP_TTL_MIN"`
}

// KeyBytes resolves the signing key: strips an optional `base64:` prefix
// and requires at least 32 bytes. Load calls this eagerly so a missing or
// weak APP_KEY fails startup instead of minting weak tokens.
func (j JWT) KeyBytes() ([]byte, error) {
	raw := strings.TrimSpace(j.Secret)
	if raw == "" {
		return nil, fmt.Errorf("APP_KEY is empty: run `ginplate key:generate` or set APP_KEY")
	}
	if s, ok := strings.CutPrefix(raw, "base64:"); ok {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, fmt.Errorf("APP_KEY base64 decode: %w", err)
		}
		raw = string(decoded)
	}
	if len(raw) < 32 {
		return nil, fmt.Errorf("APP_KEY must decode to at least 32 bytes, got %d", len(raw))
	}
	return []byte(raw), nil
}

func applyAuthDefaults(v *viper.Viper) {
	v.SetDefault("APP_TTL_MIN", 60)
}
