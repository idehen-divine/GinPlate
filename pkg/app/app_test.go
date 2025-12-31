package app

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	"github.com/gin-gonic/gin"
)

// TestApp is the single entry point for every app test: storage route-path
// mapping and static file serving.
func TestApp(t *testing.T) {
	t.Run("storage-route-path", func(t *testing.T) {
		cases := map[string]string{
			"https://app.com/storage":      "/storage",
			"https://app.com/storage/":     "/storage/",
			"https://app.com/files/public": "/files/public",
			"":                             "/storage",
			"not a url with spaces %%":     "/storage",
		}
		for in, want := range cases {
			if got := storageRoutePath(in); got != want {
				t.Errorf("storageRoutePath(%q) = %q, want %q", in, got, want)
			}
		}
	})

	t.Run("static-serves-public-files", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		root := t.TempDir()
		if err := os.MkdirAll(filepath.Join(root, "avatars"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(root, "avatars", "ada.png"), []byte("png-bytes"), 0o644); err != nil {
			t.Fatal(err)
		}
		router := gin.New()
		router.Static("/storage", root)

		get := func(target string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			req := httptest.NewRequest("GET", target, nil)
			router.ServeHTTP(w, req)
			return w
		}

		w := get("/storage/avatars/ada.png")
		if w.Code != http.StatusOK || w.Body.String() != "png-bytes" {
			t.Fatalf("file: got %d %q", w.Code, w.Body.String())
		}
		if w := get("/storage/nope.png"); w.Code != http.StatusNotFound {
			t.Fatalf("missing: got %d, want 404", w.Code)
		}
		if w := get("/storage/../app.go"); w.Code == http.StatusOK {
			t.Fatal("escape served with 200")
		}
	})
}
