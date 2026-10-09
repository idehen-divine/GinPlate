package appmail

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/internal/mail/welcome"
	"github.com/idehen-divine/GinPlate/pkg/config"
	pkgmail "github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/queue"
)

// recordingMailSender captures the message it is given, for helper tests.
type recordingMailSender struct {
	got *pkgmail.Message
}

func (r recordingMailSender) Send(_ context.Context, msg pkgmail.Message) error {
	*r.got = msg
	return nil
}

// bareMailable builds a message with no recipients, so tests prove the
// helpers fill To from their argument.
type bareMailable struct{}

func (bareMailable) Build() (pkgmail.Message, error) {
	return pkgmail.Message{Subject: "Hi", Text: "hello"}, nil
}

func TestHelpers(t *testing.T) {
	t.Run("send-addresses-and-delivers", func(t *testing.T) {
		var got pkgmail.Message
		if err := Send(context.Background(), recordingMailSender{&got}, "q@example.com", bareMailable{}); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != "q@example.com" {
			t.Fatalf("helper did not address the message: %+v", got)
		}
	})

	t.Run("send-keeps-mailable-recipients", func(t *testing.T) {
		var got pkgmail.Message
		m := bareMailableWithTo{to: "keep@example.com"}
		if err := Send(context.Background(), recordingMailSender{&got}, "other@example.com", m); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != "keep@example.com" {
			t.Fatalf("helper overwrote mailable recipients: %+v", got)
		}
	})

	t.Run("queue-addresses-and-queues", func(t *testing.T) {
		var got pkgmail.Message
		reg := queue.NewRegistry()
		pkgmail.Register(reg, recordingMailSender{&got})
		m, err := pkgmail.Open(
			config.Mail{Mailer: "log", From: config.MailFrom{Address: "h@example.com"}},
			config.S3{},
			queue.NewSync(reg),
		)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := Queue(context.Background(), m, "q@example.com", bareMailable{}); err != nil {
			t.Fatal(err)
		}
		if len(got.To) != 1 || got.To[0] != "q@example.com" {
			t.Fatalf("queued job did not carry the address: %+v", got)
		}
	})
}

// bareMailableWithTo builds a message that already sets recipients.
type bareMailableWithTo struct{ to string }

func (m bareMailableWithTo) Build() (pkgmail.Message, error) {
	return pkgmail.Message{To: []string{m.to}, Subject: "Hi", Text: "hello"}, nil
}

// previewRouter builds a gin engine with the preview routes mounted behind
// an admin JWT, for handler tests without booting the app.
func previewRouter(t *testing.T, sender pkgmail.Sender, enabled bool, mailables map[string]func(to string) pkgmail.Mailable) (*gin.Engine, string) {
	t.Helper()
	key := []byte("0123456789abcdef0123456789abcdef")
	tok := jwt.NewWithClaims(jwt.SigningMethodHS256, jwt.MapClaims{
		"sub": uuid.NewString(), "jti": uuid.NewString(), "ver": "v1",
		"type": "access", "role": "admin",
		"iat": time.Now().Unix(), "exp": time.Now().Add(time.Hour).Unix(),
	})
	signed, err := tok.SignedString(key)
	if err != nil {
		t.Fatal(err)
	}
	gin.SetMode(gin.TestMode)
	router := gin.New()
	RegisterPreviewRoutes(router.Group("/api/v1"), sender, key, nil, enabled, mailables)
	return router, signed
}

func postPreview(t *testing.T, router *gin.Engine, token, name, body string) *httptest.ResponseRecorder {
	t.Helper()
	w := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/v1/mail/preview/"+name, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	if token != "" {
		req.Header.Set("Authorization", "Bearer "+token)
	}
	router.ServeHTTP(w, req)
	return w
}

func TestPreviewRoutes(t *testing.T) {
	stub := func(to string) pkgmail.Mailable { return bareMailableWithTo{to: to} }
	real := map[string]func(to string) pkgmail.Mailable{
		"welcome": func(to string) pkgmail.Mailable {
			return welcome.Welcome{AppName: "App", Name: "Preview", Email: to, AppURL: "http://localhost:8080"}
		},
	}

	t.Run("known-mailable-sends", func(t *testing.T) {
		var got pkgmail.Message
		router, token := previewRouter(t, recordingMailSender{&got}, true, map[string]func(to string) pkgmail.Mailable{"stub": stub})
		w := postPreview(t, router, token, "stub", `{"to":"q@example.com"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		if len(got.To) != 1 || got.To[0] != "q@example.com" {
			t.Fatalf("not addressed: %+v", got)
		}
	})

	t.Run("real-mailable-renders", func(t *testing.T) {
		var got pkgmail.Message
		router, token := previewRouter(t, recordingMailSender{&got}, true, real)
		w := postPreview(t, router, token, "welcome", `{"to":"q@example.com"}`)
		if w.Code != http.StatusOK {
			t.Fatalf("status = %d, body = %s", w.Code, w.Body.String())
		}
		if got.Subject == "" || got.HTML == "" {
			t.Fatalf("welcome did not render: %+v", got)
		}
	})

	t.Run("unknown-name-404s", func(t *testing.T) {
		var got pkgmail.Message
		router, token := previewRouter(t, recordingMailSender{&got}, true, real)
		w := postPreview(t, router, token, "nope", `{"to":"q@example.com"}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("disabled-404s", func(t *testing.T) {
		var got pkgmail.Message
		router, token := previewRouter(t, recordingMailSender{&got}, false, real)
		w := postPreview(t, router, token, "welcome", `{"to":"q@example.com"}`)
		if w.Code != http.StatusNotFound {
			t.Fatalf("status = %d, want 404", w.Code)
		}
	})

	t.Run("invalid-body-422", func(t *testing.T) {
		var got pkgmail.Message
		router, token := previewRouter(t, recordingMailSender{&got}, true, real)
		w := postPreview(t, router, token, "welcome", `{"to":"not-an-email"}`)
		if w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("status = %d, want 422", w.Code)
		}
	})

	t.Run("unauthenticated-401", func(t *testing.T) {
		var got pkgmail.Message
		router, _ := previewRouter(t, recordingMailSender{&got}, true, real)
		w := postPreview(t, router, "", "welcome", `{"to":"q@example.com"}`)
		if w.Code != http.StatusUnauthorized {
			t.Fatalf("status = %d, want 401", w.Code)
		}
	})
}
