package web

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"
)

func testCtx(t *testing.T, method, target string) (*gin.Context, *httptest.ResponseRecorder) {
	t.Helper()
	gin.SetMode(gin.TestMode)
	w := httptest.NewRecorder()
	c, _ := gin.CreateTestContext(w)
	c.Request = httptest.NewRequest(method, target, nil)
	return c, w
}

func bodyMap(t *testing.T, w *httptest.ResponseRecorder) map[string]interface{} {
	t.Helper()
	var body map[string]interface{}
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
		t.Fatalf("decode body: %v", err)
	}
	return body
}

var errTestBoom = errTestType("boom")

type errTestType string

func (e errTestType) Error() string { return string(e) }

// errTestMapped is a custom domain error for mapper-registry tests.
type errTestMapped string

func (e errTestMapped) Error() string { return string(e) }

// wrapTestErr wraps a record-miss the way repository code does.
func wrapTestErr() error {
	return fmt.Errorf("users: %w", gorm.ErrRecordNotFound)
}

// TestWeb is the single entry point for every web test: paging filters,
// error envelopes, recovery, render mapping, and maintenance. Auth guard
// tests live in internal/middleware (auth_test.go).
func TestWeb(t *testing.T) {
	t.Run("paging/defaults", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/users", nil)
		f := BindFilter(c)
		if f.Limit != 25 || f.Offset != 0 || f.Sort != "id" || f.Order != "DESC" {
			t.Fatalf("bad defaults: %+v", f)
		}
	})

	t.Run("paging/caps-and-parses", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/users?limit=500&offset=10&sort=email&order=asc&search=ada", nil)
		f := BindFilter(c)
		if f.Limit != 100 {
			t.Fatalf("limit not capped: %+v", f)
		}
		if f.Offset != 10 || f.Sort != "email" || f.Order != "ASC" || f.Search != "ada" {
			t.Fatalf("bad parse: %+v", f)
		}
	})

	t.Run("paging/empty-result", func(t *testing.T) {
		res := PagedResult[string](nil, 0, ListFilter{Limit: 25})
		if res.Data == nil || len(res.Data) != 0 || res.Total != 0 {
			t.Fatalf("bad empty result: %+v", res)
		}
	})

	t.Run("fail/shows-4xx-always", func(t *testing.T) {
		for _, dbg := range []bool{false, true} {
			SetDebug(dbg)
			c, w := testCtx(t, "GET", "/x")
			Fail(c, http.StatusBadRequest, "Bad.", gin.H{"f": "x"})
			if _, ok := bodyMap(t, w)["errors"]; !ok {
				t.Fatalf("debug=%v: 4xx payload missing", dbg)
			}
		}
		SetDebug(false)
	})

	t.Run("fail/gates-5xx", func(t *testing.T) {
		SetDebug(false)
		c, w := testCtx(t, "GET", "/x")
		Fail(c, http.StatusInternalServerError, "Server Error.", gin.H{"exception": "boom"})
		if bodyMap(t, w)["errors"] != nil {
			t.Fatal("production 5xx leaked errors payload")
		}
		SetDebug(true)
		c, w = testCtx(t, "GET", "/x")
		Fail(c, http.StatusInternalServerError, "Server Error.", gin.H{"exception": "boom"})
		if _, ok := bodyMap(t, w)["errors"]; !ok {
			t.Fatal("debug 5xx missing errors payload")
		}
		SetDebug(false)
	})

	t.Run("fail/validation-shape", func(t *testing.T) {
		type payload struct {
			Email string `json:"email" binding:"required,email"`
		}
		c, w := testCtx(t, "POST", "/auth/signup")
		c.Request = httptest.NewRequest("POST", "/auth/signup", strings.NewReader(`{"email":"nope"}`))
		c.Request.Header.Set("Content-Type", "application/json")
		var p payload
		if err := c.ShouldBindJSON(&p); err == nil {
			t.Fatal("expected a binding error")
		} else {
			ValidationErrors(c, err)
		}
		body := bodyMap(t, w)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d", w.Code)
		}
		errs, ok := body["errors"].(map[string]interface{})
		if !ok || errs["email"] == nil {
			t.Fatalf("expected email field error, got %v", body["errors"])
		}
		for _, v := range errs {
			if s, ok := v.(string); ok && strings.Contains(s, "payload") {
				t.Fatalf("leaked Go internals: %q", s)
			}
		}
	})

	t.Run("recovery/modes", func(t *testing.T) {
		for _, dbg := range []bool{false, true} {
			SetDebug(dbg)
			gin.SetMode(gin.TestMode)
			var logged int
			r := gin.New()
			r.Use(Recovery(func(format string, args ...interface{}) { logged++ }))
			r.GET("/x", func(c *gin.Context) { panic("kaboom") })
			w := httptest.NewRecorder()
			r.ServeHTTP(w, httptest.NewRequest("GET", "/x", nil))
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("debug=%v: status = %d", dbg, w.Code)
			}
			// Recovery never exposes panic detail to clients, even in
			// debug mode; detail is logged server-side only.
			if bodyMap(t, w)["errors"] != nil {
				t.Fatalf("debug=%v: recovery leaked errors payload", dbg)
			}
			if logged == 0 {
				t.Fatalf("debug=%v: panic was not logged server-side", dbg)
			}
		}
		SetDebug(false)
	})

	t.Run("render/mapping", func(t *testing.T) {
		c, w := testCtx(t, "GET", "/x")
		Render(c, nil)
		if w.Body.Len() != 0 {
			t.Fatal("Render(nil) should write nothing")
		}
		for _, dbg := range []bool{false, true} {
			SetDebug(dbg)
			c, w = testCtx(t, "GET", "/x")
			Render(c, Conflict("Taken."))
			body := bodyMap(t, w)
			if w.Code != http.StatusConflict || body["message"] != "Taken." {
				t.Fatalf("debug=%v: conflict = %d %v", dbg, w.Code, body)
			}
			c, w = testCtx(t, "GET", "/x")
			Render(c, &AppError{})
			if w.Code != http.StatusInternalServerError {
				t.Fatalf("debug=%v: zero-status = %d, want 500", dbg, w.Code)
			}
			c, w = testCtx(t, "GET", "/x")
			Render(c, errTestBoom)
			body = bodyMap(t, w)
			if w.Code != http.StatusInternalServerError || body["message"] != "Server Error" {
				t.Fatalf("debug=%v: unknown = %d %v", dbg, w.Code, body)
			}
			if hasDetail := body["errors"] != nil; hasDetail != dbg {
				t.Fatalf("debug=%v: unknown-error detail present = %v", dbg, hasDetail)
			}
		}
		SetDebug(false)
	})

	t.Run("render/record-not-found", func(t *testing.T) {
		c, w := testCtx(t, "GET", "/users/1")
		Render(c, wrapTestErr())
		body := bodyMap(t, w)
		if w.Code != http.StatusNotFound || body["message"] != "Not found." {
			t.Fatalf("got %d %v", w.Code, body)
		}
	})

	t.Run("render/custom-mapper-first", func(t *testing.T) {
		RegisterErrorMapper("test-mapping", func(err error) (*AppError, bool) {
			var target errTestMapped
			if !errors.As(err, &target) {
				return nil, false
			}
			ae := New(http.StatusPaymentRequired, "Pay up.")
			ae.Err = errors.New(string(target))
			return ae, true
		})
		// Wrapped errors resolve through errors.As.
		c, w := testCtx(t, "GET", "/x")
		Render(c, fmt.Errorf("charge: %w", errTestMapped("card declined")))
		body := bodyMap(t, w)
		if w.Code != http.StatusPaymentRequired || body["message"] != "Pay up." {
			t.Fatalf("got %d %v", w.Code, body)
		}
		// Mapped causes stay debug-gated like any 5xx detail: unmatched
		// errors still fall through to the built-in 500.
		c, w = testCtx(t, "GET", "/x")
		Render(c, errTestBoom)
		if w.Code != http.StatusInternalServerError {
			t.Fatalf("fallback = %d, want 500", w.Code)
		}
	})

	t.Run("render/mapper-dup-replaces", func(t *testing.T) {
		// Uses errTestType so the earlier "test-mapping" mapper (which
		// matches errTestMapped) cannot shadow this assertion: registry
		// order is global and first-match-wins.
		RegisterErrorMapper("test-replace", func(err error) (*AppError, bool) {
			return nil, false
		})
		RegisterErrorMapper("test-replace", func(err error) (*AppError, bool) {
			var target errTestType
			if !errors.As(err, &target) {
				return nil, false
			}
			return New(http.StatusTeapot, "Teapot."), true
		})
		c, w := testCtx(t, "GET", "/x")
		Render(c, errTestBoom)
		body := bodyMap(t, w)
		if w.Code != http.StatusTeapot || body["message"] != "Teapot." {
			t.Fatalf("second registration must win: got %d %v", w.Code, body)
		}
	})

	t.Run("maintenance/passes-when-up", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		w := httptest.NewRecorder()
		c, _ := gin.CreateTestContext(w)
		c.Request = httptest.NewRequest("GET", "/api/v1/me", nil)
		Maintenance(filepath.Join(t.TempDir(), "down"))(c)
		if w.Code != 200 {
			t.Fatalf("expected passthrough, got %d", w.Code)
		}
	})

	t.Run("maintenance/blocks-when-down", func(t *testing.T) {
		gin.SetMode(gin.TestMode)
		path := filepath.Join(t.TempDir(), "down")
		if err := WriteDownFile(path, DownState{Retry: 30, Message: "Upgrading."}); err != nil {
			t.Fatal(err)
		}
		newReq := func(target string, header string) *httptest.ResponseRecorder {
			w := httptest.NewRecorder()
			c, _ := gin.CreateTestContext(w)
			c.Request = httptest.NewRequest("GET", target, nil)
			if header != "" {
				c.Request.Header.Set("X-Maintenance-Bypass", header)
			}
			Maintenance(path)(c)
			return w
		}
		w := newReq("/api/v1/me", "")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503, got %d", w.Code)
		}
		if w.Header().Get("Retry-After") != "30" {
			t.Fatalf("expected Retry-After 30, got %q", w.Header().Get("Retry-After"))
		}
		if err := WriteDownFile(path, DownState{Secret: "s3cr3t"}); err != nil {
			t.Fatal(err)
		}
		// Query-string secrets are rejected: bypass travels in the header
		// only, since query strings leak into logs and history.
		w = newReq("/api/v1/me?secret=s3cr3t", "")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("query secret must not bypass: got %d", w.Code)
		}
		w = newReq("/api/v1/me", "s3cr3t")
		if w.Code != 200 {
			t.Fatalf("expected bypass via header, got %d", w.Code)
		}
		w = newReq("/api/v1/me", "wrong")
		if w.Code != http.StatusServiceUnavailable {
			t.Fatalf("expected 503 for wrong secret, got %d", w.Code)
		}
		if err := ClearDownFile(path); err != nil {
			t.Fatal(err)
		}
		if _, err := os.Stat(path); !os.IsNotExist(err) {
			t.Fatal("down file should be gone")
		}
	})

	t.Run("envelope/shape", func(t *testing.T) {
		for _, key := range []string{"success", "code", "message", "data", "errors"} {
			c, w := testCtx(t, "GET", "/x")
			Success(c, http.StatusOK, "Fine.", gin.H{"a": "b"})
			if _, ok := bodyMap(t, w)[key]; !ok {
				t.Fatalf("success missing key %q: %s", key, w.Body.String())
			}
			c, w = testCtx(t, "GET", "/x")
			Fail(c, http.StatusBadRequest, "Bad.", gin.H{"f": "x"})
			if _, ok := bodyMap(t, w)[key]; !ok {
				t.Fatalf("failure missing key %q: %s", key, w.Body.String())
			}
		}
		c, w := testCtx(t, "GET", "/x")
		Success(c, http.StatusCreated, "Made.", gin.H{"a": "b"})
		body := bodyMap(t, w)
		if body["success"] != true || body["code"] != float64(http.StatusCreated) {
			t.Fatalf("success flags = %v", body)
		}
		c, w = testCtx(t, "GET", "/x")
		Fail(c, http.StatusConflict, "Taken.", nil)
		body = bodyMap(t, w)
		if body["success"] != false || body["code"] != float64(http.StatusConflict) {
			t.Fatalf("failure flags = %v", body)
		}
		if _, ok := body["errors"]; !ok {
			t.Fatalf("errors key must exist even when empty: %v", body)
		}
	})
}
