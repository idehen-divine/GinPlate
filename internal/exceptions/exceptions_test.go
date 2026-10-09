package exceptions

// This is the package's only test file: shipped mapper coverage lives here.

import (
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/gin-gonic/gin"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

func renderCode(t *testing.T, err error) (int, map[string]interface{}) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(http.MethodGet, "/x", nil)
	web.Render(c, err)
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatal(err)
	}
	return w.Code, body
}

func TestShippedMappers(t *testing.T) {
	t.Run("unavailable-maps-503", func(t *testing.T) {
		code, body := renderCode(t, &UnavailableError{Message: "DB down."})
		if code != http.StatusServiceUnavailable || body["message"] != "DB down." {
			t.Fatalf("got %d %v", code, body)
		}
	})

	t.Run("unavailable-default-message", func(t *testing.T) {
		code, body := renderCode(t, &UnavailableError{})
		if code != 503 || body["message"] != "Service temporarily unavailable." {
			t.Fatalf("got %d %v", code, body)
		}
	})

	t.Run("timeout-maps-504-wrapped", func(t *testing.T) {
		code, body := renderCode(t, errors.Join(errors.New("op"), &TimeoutError{Message: "Slow."}))
		if code != http.StatusGatewayTimeout || body["message"] != "Slow." {
			t.Fatalf("got %d %v", code, body)
		}
	})

	t.Run("unmatched-still-500", func(t *testing.T) {
		code, _ := renderCode(t, errors.New("boom"))
		if code != http.StatusInternalServerError {
			t.Fatalf("got %d, want 500", code)
		}
	})
}
