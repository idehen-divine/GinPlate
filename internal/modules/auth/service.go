package auth

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	appmail "github.com/idehen-divine/GinPlate/internal/mail"
	"github.com/idehen-divine/GinPlate/internal/mail/password_reset"
	"github.com/idehen-divine/GinPlate/internal/modules/users"
	notifwelcome "github.com/idehen-divine/GinPlate/internal/notifications/welcome"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

type Service struct {
	key        []byte
	accessTTL  time.Duration
	refreshTTL time.Duration
	store      session.Store
	mailer     mail.Sender
	notifier   *notify.Notifier
	notifQueue queue.Queue
	appName    string
	appURL     string
}

// RefreshTTL is the fixed refresh-token lifetime: 30 days. Only the access
// TTL is configurable (APP_TTL_MIN); refresh rotation keeps sessions alive.
const RefreshTTL = 30 * 24 * time.Hour

// NewService builds an auth Service. key is the raw HMAC signing key
// (resolved from APP_KEY); accessMin is minutes. store may be nil, which
// disables session tracking (tokens then validate by signature only).
func NewService(key []byte, accessMin int, store session.Store) *Service {
	return &Service{key: key, accessTTL: time.Duration(accessMin) * time.Minute, refreshTTL: RefreshTTL, store: store}
}

// WithMailer attaches a mail sender for transactional email (password
// reset). Nil is allowed and keeps the service usable without mail.
func (s *Service) WithMailer(m mail.Sender) *Service {
	s.mailer = m
	return s
}

// WithNotifications attaches the welcome-notification fan-out for new
// signups. Nil notifier/queue disables it; delivery failures are logged,
// never fatal to registration.
func (s *Service) WithNotifications(n *notify.Notifier, q queue.Queue, appName, appURL string) *Service {
	s.notifier = n
	s.notifQueue = q
	s.appName = appName
	s.appURL = appURL
	return s
}

// SendPasswordReset delivers the forgot-password mailable for email/token
// through the attached mailer. It reports an error when no mailer is
// configured, so callers fail loudly instead of silently dropping mail.
func (s *Service) SendPasswordReset(ctx context.Context, appURL, name, email, token string, expiresMinutes int) error {
	if s.mailer == nil {
		return web.Internal(errors.New("mailer not configured"))
	}
	return appmail.Send(ctx, s.mailer, email, passwordreset.PasswordReset{
		AppURL:         appURL,
		Name:           name,
		Email:          email,
		Token:          token,
		ExpiresMinutes: expiresMinutes,
	})
}

// Register hashes the password and creates a member account. A duplicate
// email surfaces as a database error, mapped to 409 by the handler.
func (s *Service) Register(db *gorm.DB, dto SignupDTO) (*users.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(dto.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &users.User{Name: dto.Name, Email: dto.Email, PasswordHash: string(hash), Role: "member", IsActive: true}
	if err := db.Create(u).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, web.Conflict("Email already taken.")
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not register.", err)
	}
	s.notifyWelcome(u)
	return u, nil
}

// notifyWelcome queues the welcome notification best-effort: delivery
// failures are logged, never fatal to registration.
func (s *Service) notifyWelcome(u *users.User) {
	if s.notifier == nil || s.notifQueue == nil {
		return
	}
	to := notify.UserNotifiable(u.ID.String(), u.Email)
	n := notifwelcome.Welcome{AppName: s.appName, Name: u.Name, Email: u.Email, AppURL: s.appURL}
	if _, err := s.notifier.Queue(context.Background(), s.notifQueue, to, n); err != nil {
		slog.Warn("welcome notification failed", "user", u.ID.String(), "err", err)
	}
}

// Login verifies email, password hash, and active status, then issues an
// access + refresh token pair. Failures share one message so callers can't
// probe which accounts exist.
func (s *Service) Login(db *gorm.DB, dto LoginDTO) (*users.User, *TokenPair, error) {
	var u users.User
	if err := db.Where("email = ?", dto.Email).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, web.Unauthorized("Invalid credentials.")
		}
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(u.PasswordHash), []byte(dto.Password)); err != nil {
		return nil, nil, web.Unauthorized("Invalid credentials.")
	}
	if !u.IsActive {
		return nil, nil, web.Unauthorized("Invalid credentials.")
	}
	pair, err := s.issue(&u)
	if err != nil {
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
	}
	return &u, pair, nil
}

// issue mints an access + refresh pair and links both session halves in
// Redis: session:{accessJti} -> refreshJti and refresh:{refreshJti} ->
// accessJti. Either half suffices to find and destroy the whole session.
func (s *Service) issue(u *users.User) (*TokenPair, error) {
	ctx := context.Background()
	jti := uuid.NewString()
	rjti := uuid.NewString()
	now := time.Now()
	accessClaims := jwt.MapClaims{
		"sub": u.ID.String(),
		"jti": jti, "ver": "v1", "type": "access", "role": u.Role,
		"iat": now.Unix(), "exp": now.Add(s.accessTTL).Unix(),
	}
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.key)
	if err != nil {
		return nil, err
	}
	refreshClaims := jwt.MapClaims{
		"sub": u.ID.String(),
		"jti": rjti, "ver": "v1", "type": "refresh", "role": u.Role,
		"iat": now.Unix(), "exp": now.Add(s.refreshTTL).Unix(),
	}
	refresh, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.key)
	if err != nil {
		return nil, err
	}
	if s.store != nil {
		_ = s.store.Link(ctx, jti, rjti, u.ID.String(), s.accessTTL, s.refreshTTL)
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int(s.accessTTL.Seconds())}, nil
}

// Logout destroys the whole session: the access half and its linked refresh
// half. Missing halves are fine, so logout stays idempotent.
func (s *Service) Logout(sessionID string) {
	if s.store == nil || sessionID == "" {
		return
	}
	ctx := context.Background()
	rjti, _ := s.store.AccessValid(ctx, sessionID)
	_ = s.store.Unlink(ctx, sessionID, rjti)
}

// Refresh validates a refresh token and rotates the session: the presented
// refresh half must still exist in Redis (logout or a prior rotation deletes
// it), then a fresh pair is issued and the old halves are destroyed. A
// revoked or replayed refresh token is rejected before any database access.
func (s *Service) Refresh(db *gorm.DB, refreshToken string) (*users.User, *TokenPair, error) {
	claims, err := s.parse(refreshToken)
	if err != nil {
		return nil, nil, web.Unauthorized("Invalid refresh token.")
	}
	if claims["type"] != "refresh" {
		return nil, nil, web.Unauthorized("Invalid refresh token.")
	}
	rjti, _ := claims["jti"].(string)
	if rjti == "" {
		return nil, nil, web.Unauthorized("Invalid refresh token.")
	}
	var oldAccess string
	if s.store != nil {
		var ok bool
		if oldAccess, ok = s.store.RefreshValid(context.Background(), rjti); !ok {
			return nil, nil, web.Unauthorized("Session revoked.")
		}
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, nil, web.Unauthorized("Invalid refresh token.")
	}
	var u users.User
	if err := db.Where("id = ?", sub).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, nil, web.Unauthorized("Invalid credentials.")
		}
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not refresh.", err)
	}
	if !u.IsActive {
		return nil, nil, web.Unauthorized("Invalid credentials.")
	}
	pair, err := s.issue(&u)
	if err != nil {
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not refresh.", err)
	}
	if s.store != nil {
		_ = s.store.Unlink(context.Background(), oldAccess, rjti)
	}
	return &u, pair, nil
}

// CheckResult is the outcome of validating an arbitrary token.
type CheckResult struct {
	Valid     bool   `json:"valid"`
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

// Check validates a token without side effects.
func (s *Service) Check(token string) CheckResult {
	claims, err := s.parse(token)
	if err != nil {
		return CheckResult{Valid: false}
	}
	if ver, _ := claims["ver"].(string); ver != web.TokenVersion {
		return CheckResult{Valid: false}
	}
	exp, _ := claims["exp"].(float64)
	typ, _ := claims["type"].(string)
	role, _ := claims["role"].(string)
	return CheckResult{Valid: true, Type: typ, Role: role, ExpiresAt: int64(exp)}
}

// parse verifies the HMAC signature and expiry, returning the claims.
func (s *Service) parse(token string) (jwt.MapClaims, error) {
	parsed, err := jwt.ParseWithClaims(token, jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.key, nil
	})
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token")
	}
	return claims, nil
}
