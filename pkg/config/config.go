// Package config loads the application configuration from `.env`,
// environment variables, and code defaults.
//
// Layout mirrors Laravel's config directory: one file per domain, each
// exposing a nested struct decoded from flat `ENV_KEY` names. Existing key
// names are stable; new domains add new keys. `Load` is the only entry
// point: it reads `.env` (optional, overridden by real environment
// variables), applies per-domain defaults, normalizes `"null"` placeholders
// to unset, decodes once, then resolves secrets.
package config

import (
	"fmt"
	"os"
	"reflect"
	"strings"

	"github.com/mitchellh/mapstructure"
	"github.com/spf13/viper"
)

// Config is the whole application configuration, one nested struct per
// config/<domain>.go file.
type Config struct {
	App         App         `mapstructure:",squash"`
	Auth        Auth        `mapstructure:",squash"`
	Database    Database    `mapstructure:",squash"`
	Cache       Cache       `mapstructure:",squash"`
	Filesystem  Filesystem  `mapstructure:",squash"`
	Mail        Mail        `mapstructure:",squash"`
	Queue       Queue       `mapstructure:",squash"`
	Logging     Logging     `mapstructure:",squash"`
	Session     Session     `mapstructure:",squash"`
	Services    Services    `mapstructure:",squash"`
	Maintenance Maintenance `mapstructure:",squash"`
}

// bindEnvKeys registers every mapstructure key so Unmarshal sees
// environment variables. AutomaticEnv alone only affects Get — without
// this, env-only keys would silently decode as zero values. Nested structs
// use `,squash`, so their fields decode from top-level keys; this walks
// into them (a bare `,squash` tag carries no name to bind). It descends
// only into structs that carry mapstructure tags, so stdlib types like
// time.Time are never touched.
func bindEnvKeys(v *viper.Viper, t reflect.Type) {
	if t.Kind() == reflect.Ptr {
		t = t.Elem()
	}
	if t.Kind() != reflect.Struct {
		return
	}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		if tag, ok := f.Tag.Lookup("mapstructure"); ok {
			if name, _, _ := strings.Cut(tag, ","); name != "" {
				_ = v.BindEnv(name)
				continue
			}
		}
		ft := f.Type
		if ft.Kind() == reflect.Ptr {
			ft = ft.Elem()
		}
		if ft.Kind() == reflect.Struct && hasMapstructureTags(ft) {
			bindEnvKeys(v, ft)
		}
	}
}

// hasMapstructureTags reports whether any direct field of t carries a
// mapstructure tag.
func hasMapstructureTags(t reflect.Type) bool {
	for i := 0; i < t.NumField(); i++ {
		if tag, ok := t.Field(i).Tag.Lookup("mapstructure"); ok && tag != "" {
			return true
		}
	}
	return false
}

// nullHook decodes the literal string "null" (and "") as the zero value.
// `.env` files use `KEY=null` for intentionally-unset optionals, but viper
// would otherwise hand e.g. MAIL_PORT the string "null" and decoding it
// into an int would fail the whole Load.
func nullHook() mapstructure.DecodeHookFunc {
	return func(f reflect.Type, t reflect.Type, data any) (any, error) {
		s, ok := data.(string)
		if !ok || (s != "" && !strings.EqualFold(s, "null")) {
			return data, nil
		}
		return reflect.Zero(t).Interface(), nil
	}
}

// Load reads `.env` (optional), overlays environment variables, applies
// defaults, and decodes the result into Config. It fails fast when APP_KEY
// is missing or too short, so the app can never sign tokens with an empty
// or weak secret.
func Load() (*Config, error) {
	v := viper.New()
	v.SetConfigFile(".env")
	v.SetConfigType("env")
	_ = v.ReadInConfig() // .env optional; env vars win
	v.AutomaticEnv()
	v.SetEnvKeyReplacer(strings.NewReplacer(".", "_"))

	applyAppDefaults(v)
	applyAuthDefaults(v)
	applyDatabaseDefaults(v)
	applyCacheDefaults(v)
	applyFilesystemDefaults(v)
	applyMailDefaults(v)
	applyQueueDefaults(v)
	applyLoggingDefaults(v)
	applySessionDefaults(v)

	bindEnvKeys(v, reflect.TypeOf(Config{}))

	var c Config
	if err := v.Unmarshal(&c, viper.DecodeHook(nullHook())); err != nil {
		return nil, err
	}
	// Expand ${VAR} against merged config first (so .env values reference
	// each other, e.g. MAIL_FROM_NAME="${APP_NAME} Team"), OS env second.
	lookup := func(key string) string {
		if s := v.GetString(key); s != "" {
			return s
		}
		return os.Getenv(key)
	}
	c.Mail.From.Name = os.Expand(c.Mail.From.Name, lookup)
	c.Mail.From.Address = os.Expand(c.Mail.From.Address, lookup)
	if c.Filesystem.PublicURL == "" {
		c.Filesystem.PublicURL = strings.TrimSuffix(c.App.URL, "/") + "/storage"
	}
	if _, err := c.Auth.JWT.KeyBytes(); err != nil {
		return nil, fmt.Errorf("config: %w", err)
	}
	return &c, nil
}
