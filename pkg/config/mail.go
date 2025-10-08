package config

import (
	"github.com/spf13/viper"
)

// Mail holds outgoing mail settings, delivered by pkg/mail.
//
// Supported MAIL_MAILER values: "log" (dev/test default, writes to logs),
// "smtp" (real delivery via MAIL_HOST/PORT/USERNAME/PASSWORD/ENCRYPTION),
// "ses" (AWS SESv2 raw send, reusing the standard AWS_* credential chain).
type Mail struct {
	Mailer   string `mapstructure:"MAIL_MAILER"`
	Host     string `mapstructure:"MAIL_HOST"`
	Port     int    `mapstructure:"MAIL_PORT"`
	Username string `mapstructure:"MAIL_USERNAME"`
	Password string `mapstructure:"MAIL_PASSWORD"`
	// Encryption is "" (plain), "tls" (STARTTLS), or "ssl" (implicit TLS).
	// The literal "null" in .env files normalizes to unset (see nullHook
	// in config.go).
	Encryption string `mapstructure:"MAIL_ENCRYPTION"`
	// TimeoutSec bounds SMTP dials and SES API calls.
	TimeoutSec int `mapstructure:"MAIL_TIMEOUT_SEC"`

	From MailFrom `mapstructure:",squash"`
}

// MailFrom holds the default sender identity. ${VAR} references in these
// values are expanded at load time, so FROM_NAME can reuse APP_NAME.
type MailFrom struct {
	Address string `mapstructure:"MAIL_FROM_ADDRESS"`
	Name    string `mapstructure:"MAIL_FROM_NAME"`
}

func applyMailDefaults(v *viper.Viper) {
	v.SetDefault("MAIL_MAILER", "log")
	v.SetDefault("MAIL_HOST", "localhost")
	v.SetDefault("MAIL_PORT", 1025)
	v.SetDefault("MAIL_USERNAME", "")
	v.SetDefault("MAIL_PASSWORD", "")
	v.SetDefault("MAIL_ENCRYPTION", "")
	v.SetDefault("MAIL_TIMEOUT_SEC", 10)
	v.SetDefault("MAIL_FROM_ADDRESS", "hello@example.com")
	v.SetDefault("MAIL_FROM_NAME", "GinPlate")
}
