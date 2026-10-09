// Package modules is the root registry for API modules: it blank-imports
// every module package so their init() self-registrations
// (web.RegisterModule) fire. Adding a module = adding one line here;
// pkg/app/router.go imports only this package, so it never changes per
// module.
package modules

import (
	// Each module self-registers its routes (and route:list labels) via
	// init(). Keep sorted by module name.
	_ "github.com/idehen-divine/GinPlate/internal/modules/auth"
	_ "github.com/idehen-divine/GinPlate/internal/modules/notifications"
	_ "github.com/idehen-divine/GinPlate/internal/modules/users"
)
