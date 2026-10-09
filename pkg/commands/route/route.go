package route

import (
	"encoding/json"
	"sort"
	"strings"

	"github.com/spf13/cobra"

	"github.com/idehen-divine/GinPlate/pkg/app"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

type routeEntry struct {
	Method     string `json:"method"`
	Path       string `json:"path"`
	Middleware string `json:"middleware"`
	Handler    string `json:"handler"`
}

// NewRouteListCmd builds `ginplate route:list`: same routes as RunAPI, no
// backends touched. Config-free (lists the debug+swagger superset).
func NewRouteListCmd(config *config.Config) *cobra.Command {
	var methodFilter, pathFilter, middlewareFilter string
	var asJSON bool
	cmd := &cobra.Command{
		Use:   "route:list",
		Short: "List all registered routes",
		Long: `List every HTTP route the API serves (method, path, middleware, handler).

Builds the same gin engine RunAPI boots, but never connects to MySQL,
Postgres, Redis, or mail drivers, so it works config-free:

  ginplate route:list
  ginplate route:list --method GET --path /api/v1
  ginplate route:list --middleware auth
  ginplate route:list --json`,
		RunE: func(cmd *cobra.Command, _ []string) error {
			// No config: show the superset so nothing hides.
			publicURL, publicRoot := "/storage", "storage/app/public"
			swagger, metrics, debug := true, true, true
			appName, appURL := "GinPlate", "http://localhost:8080"
			if config != nil {
				if config.Filesystem.PublicURL != "" {
					publicURL = config.Filesystem.PublicURL
				}
				if config.Filesystem.PublicRoot != "" {
					publicRoot = config.Filesystem.PublicRoot
				}
				swagger = config.App.Swagger
				metrics = config.App.Metrics
				debug = config.App.Debug
				if config.App.Name != "" {
					appName = config.App.Name
				}
				if config.App.URL != "" {
					appURL = config.App.URL
				}
			}
			router := app.NewListingRouter(publicURL, publicRoot, swagger, metrics, debug, appName, appURL)
			routes := router.Routes()
			sort.Slice(routes, func(i, j int) bool {
				if routes[i].Path == routes[j].Path {
					return routes[i].Method < routes[j].Method
				}
				return routes[i].Path < routes[j].Path
			})
			var out []routeEntry
			for _, r := range routes {
				if methodFilter != "" && !strings.EqualFold(r.Method, strings.TrimSpace(methodFilter)) {
					continue
				}
				if pathFilter != "" && !strings.Contains(r.Path, pathFilter) {
					continue
				}
				mw := web.RouteMiddleware(r.Method, r.Path)
				if middlewareFilter != "" && !strings.Contains(strings.ToLower(mw), strings.ToLower(strings.TrimSpace(middlewareFilter))) {
					continue
				}
				out = append(out, routeEntry{Method: r.Method, Path: r.Path, Middleware: mw, Handler: r.Handler})
			}
			if asJSON {
				enc, err := json.MarshalIndent(out, "", "  ")
				if err != nil {
					return err
				}
				cmd.Println(string(enc))
				return nil
			}
			if len(out) == 0 {
				cmd.Println("no routes")
				return nil
			}
			cmd.Printf("%-7s %-40s %-11s %s\n", "METHOD", "PATH", "MIDDLEWARE", "HANDLER")
			for _, r := range out {
				cmd.Printf("%-7s %-40s %-11s %s\n", r.Method, r.Path, r.Middleware, r.Handler)
			}
			return nil
		},
	}
	cmd.Flags().StringVar(&methodFilter, "method", "", "Filter by HTTP method (GET, POST, ...)")
	cmd.Flags().StringVar(&pathFilter, "path", "", "Filter by path substring (e.g. /api/v1)")
	cmd.Flags().StringVar(&middlewareFilter, "middleware", "", "Filter by middleware label substring (-, auth, admin, active)")
	cmd.Flags().BoolVar(&asJSON, "json", false, "Output routes as JSON")
	return cmd
}
