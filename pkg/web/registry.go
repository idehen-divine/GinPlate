package web

import (
	"sort"
	"strings"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/idehen-divine/GinPlate/pkg/session"
)

// ModuleDeps carries the shared backends for API modules. Capture at
// registration, touch only when serving (nil-safe for `route:list`).
type ModuleDeps struct {
	DB           *gorm.DB
	Key          []byte
	Store        session.Store
	Sender       pkgmail.Sender
	Notifier     *notify.Notifier
	Queue        queue.Queue
	Debug        bool
	AppName      string
	AppURL       string
	AccessTTLMin int
}

// ModuleFunc mounts one module's routes onto the versioned group.
type ModuleFunc func(r *gin.RouterGroup, d *ModuleDeps)

// RegisteredModule pairs a registry name with its constructor.
type RegisteredModule struct {
	Name string
	Fn   ModuleFunc
}

var (
	moduleNames []string
	modules     = map[string]ModuleFunc{}
)

// RegisterModule registers a mount function under name; later calls replace.
func RegisterModule(name string, fn ModuleFunc) {
	if _, ok := modules[name]; !ok {
		moduleNames = append(moduleNames, name)
	}
	modules[name] = fn
}

// RegisteredModules returns modules sorted by name.
func RegisteredModules() []RegisteredModule {
	names := append([]string(nil), moduleNames...)
	sort.Strings(names)
	out := make([]RegisteredModule, 0, len(names))
	for _, n := range names {
		if fn, ok := modules[n]; ok && fn != nil {
			out = append(out, RegisteredModule{Name: n, Fn: fn})
		}
	}
	return out
}

// Per-route middleware labels for `route:list` ("-", "auth", "auth+admin"...).
var routeMiddleware = map[string]string{}

// RegisterRouteMeta records the middleware label for METHOD + path.
func RegisterRouteMeta(method, path, middleware string) {
	method = strings.ToUpper(strings.TrimSpace(method))
	if method == "" || path == "" {
		return
	}
	routeMiddleware[method+" "+path] = middleware
}

// RouteMiddleware returns the label for METHOD + path, or "-" when unregistered.
func RouteMiddleware(method, path string) string {
	if m := routeMiddleware[strings.ToUpper(strings.TrimSpace(method))+" "+path]; m != "" {
		return m
	}
	return "-"
}

// GlobalMiddlewareFunc installs one cross-cutting middleware (nil-safe for `route:list`).
type GlobalMiddlewareFunc func(router *gin.Engine, d *ModuleDeps)

// RegisteredGlobalMiddleware pairs a registry name and order with its installer.
type RegisteredGlobalMiddleware struct {
	Name  string
	Order int
	Fn    GlobalMiddlewareFunc
}

var (
	globalMiddlewareNames []string
	globalMiddlewares     = map[string]RegisteredGlobalMiddleware{}
)

// DefaultGlobalMiddlewareOrder runs custom globals after all built-ins.
const DefaultGlobalMiddlewareOrder = 1000

// RegisterGlobalMiddleware registers a global installer; later calls replace.
// Lower order runs first, ties break by name.
func RegisterGlobalMiddleware(name string, order int, fn GlobalMiddlewareFunc) {
	if _, ok := globalMiddlewares[name]; !ok {
		globalMiddlewareNames = append(globalMiddlewareNames, name)
	}
	globalMiddlewares[name] = RegisteredGlobalMiddleware{Name: name, Order: order, Fn: fn}
}

// RegisteredGlobalMiddlewares returns globals sorted by order, then name.
func RegisteredGlobalMiddlewares() []RegisteredGlobalMiddleware {
	names := append([]string(nil), globalMiddlewareNames...)
	sort.Slice(names, func(i, j int) bool {
		a, b := globalMiddlewares[names[i]], globalMiddlewares[names[j]]
		if a.Order == b.Order {
			return a.Name < b.Name
		}
		return a.Order < b.Order
	})
	out := make([]RegisteredGlobalMiddleware, 0, len(names))
	for _, n := range names {
		if m, ok := globalMiddlewares[n]; ok && m.Fn != nil {
			out = append(out, m)
		}
	}
	return out
}
