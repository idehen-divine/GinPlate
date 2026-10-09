package route

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/idehen-divine/GinPlate/pkg/app"
	"github.com/idehen-divine/GinPlate/pkg/config"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

func execute(args ...string) (string, error) {
	cmd := NewRouteListCmd(nil)
	cmd.SetArgs(args)
	var sb strings.Builder
	cmd.SetOut(&sb)
	cmd.SetErr(&sb)
	err := cmd.Execute()
	return sb.String(), err
}

func TestRouteListContainsCoreRoutes(t *testing.T) {
	out, err := execute()
	if err != nil {
		t.Fatalf("route:list: %v", err)
	}
	for _, want := range []string{
		"POST",
		"/api/v1/auth/signup",
		"/api/v1/auth/login",
		"/api/v1/users",
		"/api/v1/notifications",
		"/livez",
		"/readyz",
		"/health",
		"/metrics",
	} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRouteListFilters(t *testing.T) {
	out, err := execute("--method", "GET", "--path", "/api/v1/users")
	if err != nil {
		t.Fatalf("route:list with filters: %v", err)
	}
	if !strings.Contains(out, "/api/v1/users") {
		t.Errorf("expected users route, got:\n%s", out)
	}
	if strings.Contains(out, "/api/v1/auth/signup") {
		t.Errorf("signup should be filtered out, got:\n%s", out)
	}
}

func TestRouteListJSON(t *testing.T) {
	out, err := execute("--json")
	if err != nil {
		t.Fatalf("route:list --json: %v", err)
	}
	var entries []routeEntry
	if err := json.Unmarshal([]byte(out), &entries); err != nil {
		t.Fatalf("invalid JSON: %v\n%s", err, out)
	}
	if len(entries) == 0 {
		t.Fatal("expected at least one route")
	}
	found := false
	for _, e := range entries {
		if e.Path == "/api/v1/auth/login" && e.Method == "POST" {
			found = true
			if e.Middleware != "-" {
				t.Errorf("login should be public (-), got %q", e.Middleware)
			}
		}
		if e.Path == "/api/v1/users" && e.Method == "GET" && e.Middleware != "auth+admin" {
			t.Errorf("users should be auth+admin, got %+v", e)
		}
		if e.Method == "" || e.Path == "" || e.Handler == "" || e.Middleware == "" {
			t.Errorf("entry missing fields: %+v", e)
		}
	}
	if !found {
		t.Errorf("login route missing in JSON output")
	}
}

func TestRouteListMiddlewareColumn(t *testing.T) {
	out, err := execute()
	if err != nil {
		t.Fatalf("route:list: %v", err)
	}
	for _, want := range []string{"MIDDLEWARE", "auth+admin", "auth"} {
		if !strings.Contains(out, want) {
			t.Errorf("expected output to contain %q, got:\n%s", want, out)
		}
	}
}

func TestRouteListMiddlewareFilter(t *testing.T) {
	out, err := execute("--middleware", "auth+admin")
	if err != nil {
		t.Fatalf("route:list --middleware: %v", err)
	}
	if !strings.Contains(out, "/api/v1/users") {
		t.Errorf("expected users route, got:\n%s", out)
	}
	if strings.Contains(out, "/api/v1/auth/login") {
		t.Errorf("public login should be filtered out, got:\n%s", out)
	}
}

func TestEveryAPIRouteHasMiddlewareMeta(t *testing.T) {
	router := app.NewListingRouter("/storage", "storage/app/public", true, true, true, "GinPlate", "http://localhost:8080")
	for _, r := range router.Routes() {
		if !strings.HasPrefix(r.Path, "/api/v1/") {
			continue
		}
		if got := web.RouteMiddleware(r.Method, r.Path); got == "" {
			t.Errorf("%s %s has empty middleware label", r.Method, r.Path)
		}
	}
}

func TestRouteListRespectsConfig(t *testing.T) {
	config := &config.Config{}
	config.App.Swagger = false
	config.App.Debug = false
	config.App.Name = "Test"
	config.App.URL = "http://localhost:8080"
	config.Filesystem.PublicURL = "http://localhost:8080/storage"
	config.Filesystem.PublicRoot = "storage/app/public"
	cmd := NewRouteListCmd(config)
	var sb strings.Builder
	cmd.SetOut(&sb)
	cmd.SetArgs([]string{})
	if err := cmd.Execute(); err != nil {
		t.Fatalf("route:list with config: %v", err)
	}
	// Swagger route must be hidden when disabled, preview still registered
	// (handler 404s at runtime).
	if strings.Contains(sb.String(), "/swagger") {
		t.Errorf("swagger route should be hidden when disabled, got:\n%s", sb.String())
	}
}
