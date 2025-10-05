// Command ginplate is the single main binary.
//
//	ginplate serve              # run the API
//	ginplate queue:work          # run the background worker
//	ginplate schedule:work      # push due schedule entries to the queue
//	ginplate queue:failed       # list buried jobs (retry/forget/flush too)
//	ginplate migrate up         # create the database (if missing) + run the schema
//	ginplate migrate status     # show applied/pending migrations
//	ginplate make:command Foo   # scaffold a clonable command (auto-registered)
//
// @title GinPlate API
// @version 1.0
// @description GinPlate single-database API.
// @BasePath /api/v1
// @securityDefinitions.apikey Bearer
// @in header
// @name Authorization
package main

import (
	"fmt"
	"log"
	"os"

	"github.com/idehen-divine/GinPlate/pkg/commands"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// main loads config and runs the ginplate command tree. Commands that touch
// services (serve, migrate) fail fast on bad config; generators, key
// tooling, and help run config-free so bootstrapping (e.g. key:generate
// before APP_KEY exists) always works.
func main() {
	cfg, err := config.Load()
	if err != nil && needsConfig(os.Args[1:]) {
		log.Fatalf("config: %v", err)
	}
	if err := commands.NewRoot(cfg).Execute(); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// needsConfig reports whether the requested command touches services.
// Help flags always win: `ginplate migrate --help` must print help even
// when config is broken (checked first, before the command match).
func needsConfig(args []string) bool {
	if len(args) == 0 {
		return false
	}
	for _, a := range args {
		if a == "-h" || a == "--help" {
			return false
		}
	}
	if args[0] == "serve" || args[0] == "migrate" || args[0] == "queue:work" || args[0] == "mail:test" || args[0] == "schedule:work" ||
		args[0] == "queue:failed" || args[0] == "queue:retry" || args[0] == "queue:forget" || args[0] == "queue:flush" {
		return true
	}
	return false
}
