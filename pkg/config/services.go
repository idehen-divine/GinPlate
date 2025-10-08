package config

// Services holds third-party service credentials. It ships empty on
// purpose: add one nested struct per vendor when an integration lands,
// e.g.
//
//	Type Slack struct {
//	    Webhook string `mapstructure:"SERVICES_SLACK_WEBHOOK"`
//	}
//
// and a matching applyServicesDefaults entry. Services stays decoupled
// from the mail/filesystem structs that reference vendors by name.
type Services struct {
}
