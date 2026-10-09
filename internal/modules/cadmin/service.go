package cadmin

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"

	appmail "github.com/idehen-divine/GinPlate/internal/mail"
	"github.com/idehen-divine/GinPlate/internal/mail/password_reset"
	"github.com/idehen-divine/GinPlate/internal/modules/auth"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/resettoken"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// ControlTokenType separates control tokens from tenant access/refresh
// tokens at parse time.
const ControlTokenType = "control"

// AdminTTL is the fixed control-session lifetime: 12 hours. Control work
// is interactive and short-lived; rotation is re-login.
const AdminTTL = 12 * time.Hour

// ControlClaims is the parsed control JWT shape.
type ControlClaims struct {
	AdminID   uuid.UUID `json:"sub"`
	SessionID string    `json:"jti"`
	Role      string    `json:"role"`
	Type      string    `json:"type"`
}

// Service authenticates control admins and mints their tokens. An optional
// session store tracks control JTIs (link on login, unlink on logout,
// enforced by middleware); nil keeps the legacy stateless behavior.
type Service struct {
	key     []byte
	ttl     time.Duration
	store   session.Store
	mailer  mail.Sender
	appURL  string
	appName string
}

// NewService wires the control signing key (CONTROL_JWT_SECRET or the
// APP_KEY fallback resolved by the caller).
func NewService(key []byte) *Service {
	return &Service{key: key, ttl: AdminTTL}
}

// WithStore attaches session tracking for control tokens.
func (s *Service) WithStore(store session.Store) *Service {
	s.store = store
	return s
}

// WithMailer attaches the reset-mail sender with its branding.
func (s *Service) WithMailer(m mail.Sender, appURL, appName string) *Service {
	s.mailer = m
	s.appURL = appURL
	s.appName = appName
	return s
}

// ResolveSecret returns the control signing key: the explicit secret when
// set, else the app key with fallback=true so callers warn. Either way the
// key must decode to at least 32 bytes.
func ResolveSecret(controlSecret, appKey string) ([]byte, bool, error) {
	raw := strings.TrimSpace(controlSecret)
	fallback := false
	if raw == "" {
		raw = strings.TrimSpace(appKey)
		fallback = true
	}
	if raw == "" {
		return nil, fallback, fmt.Errorf("control secret is empty: set CONTROL_JWT_SECRET")
	}
	if s, ok := strings.CutPrefix(raw, "base64:"); ok {
		decoded, err := base64.StdEncoding.DecodeString(strings.TrimSpace(s))
		if err != nil {
			return nil, fallback, fmt.Errorf("control secret base64 decode: %w", err)
		}
		raw = string(decoded)
	}
	if len(raw) < 32 {
		return nil, fallback, fmt.Errorf("control secret must decode to at least 32 bytes, got %d", len(raw))
	}
	return []byte(raw), fallback, nil
}

// Login verifies a control admin and issues a control token. Failures share
// one message so callers can't probe which accounts exist.
func (s *Service) Login(db *gorm.DB, email, password string) (*ControlAdmin, string, error) {
	var a ControlAdmin
	email = strings.ToLower(strings.TrimSpace(email))
	if err := db.Where("email = ?", email).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, "", web.Unauthorized("Invalid credentials.")
		}
		return nil, "", web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
	}
	if err := bcrypt.CompareHashAndPassword([]byte(a.PasswordHash), []byte(password)); err != nil {
		return nil, "", web.Unauthorized("Invalid credentials.")
	}
	if !a.IsActive {
		return nil, "", web.Unauthorized("Invalid credentials.")
	}
	tok, err := s.Issue(&a)
	if err != nil {
		return nil, "", web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
	}
	if s.store != nil {
		claims, err := s.Parse(tok)
		if err != nil {
			return nil, "", web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
		}
		// Track the control JTI so logout revokes it; untracked tokens
		// are never handed out.
		if err := s.store.Link(context.Background(), claims.SessionID, claims.SessionID, a.ID.String(), s.ttl, s.ttl); err != nil {
			return nil, "", web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
		}
	}
	return &a, tok, nil
}

// Logout revokes a control token. Unknown or already-revoked tokens still
// report success (idempotent); backend failures surface.
func (s *Service) Logout(token string) error {
	if s.store == nil {
		return nil
	}
	claims, err := s.Parse(token)
	if err != nil {
		return nil
	}
	if err := s.store.Unlink(context.Background(), claims.SessionID, claims.SessionID); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not log out.", err)
	}
	return nil
}

// ForgotPassword issues a reset token for an active control admin and
// emails the link. Unknown or inactive addresses return nil (no oracle).
func (s *Service) ForgotPassword(db *gorm.DB, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	var a ControlAdmin
	if err := db.Where("email = ?", email).First(&a).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return web.Wrap(http.StatusInternalServerError, "Could not request reset.", err)
	}
	if !a.IsActive {
		return nil
	}
	ctx := context.Background()
	raw, err := auth.IssueResetToken(ctx, db, a.Email, auth.ResetKindControlAdmin, auth.ResetTokenTTL)
	if err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not request reset.", err)
	}
	if s.mailer == nil {
		return web.Wrap(http.StatusInternalServerError, "Could not request reset.", errors.New("mailer not configured"))
	}
	if err := appmail.Send(ctx, s.mailer, a.Email, passwordreset.PasswordReset{
		AppURL: s.appURL, Name: a.Name, Email: a.Email,
		Token: raw, ExpiresMinutes: int(auth.ResetTokenTTL.Minutes()),
	}); err != nil {
		slog.Warn("control password reset mail failed", "admin", a.ID.String(), "err", err)
	}
	return nil
}

// ResetPassword consumes a control reset token and sets a new password,
// revoking every session for the admin. Invalid and expired tokens share
// one generic failure so tokens can't be probed.
func (s *Service) ResetPassword(db *gorm.DB, rawToken, newPassword string) error {
	if len(newPassword) < 8 {
		return web.Wrap(http.StatusBadRequest, "Password must be at least 8 characters.", nil)
	}
	if strings.TrimSpace(rawToken) == "" {
		return web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
	}
	ctx := context.Background()
	email, err := s.emailForResetToken(ctx, db, rawToken)
	if err != nil {
		return err
	}
	var a ControlAdmin
	if err := db.WithContext(ctx).Where("email = ?", email).First(&a).Error; err != nil {
		return web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
	}
	if s.store != nil {
		if err := s.store.RevokeUser(ctx, a.ID.String()); err != nil {
			return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
		}
	}
	a.PasswordHash = string(hash)
	if err := db.WithContext(ctx).Save(&a).Error; err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
	}
	if err := auth.ConsumeResetToken(ctx, db, email, auth.ResetKindControlAdmin, rawToken); err != nil {
		if errors.Is(err, auth.ErrResetInvalid) || errors.Is(err, auth.ErrResetExpired) {
			return web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
		}
		return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
	}
	return nil
}

// emailForResetToken resolves the admin address for a presented raw token
// without consuming it, mirroring the tenant flow.
func (s *Service) emailForResetToken(ctx context.Context, db *gorm.DB, rawToken string) (string, error) {
	generic := web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
	var row auth.ResetToken
	if err := db.WithContext(ctx).
		Where("kind = ? AND token_hash = ?", auth.ResetKindControlAdmin, resettoken.Hash(rawToken)).
		First(&row).Error; err != nil {
		return "", generic
	}
	if !time.Now().Before(row.ExpiresAt) {
		_ = db.WithContext(ctx).Where("email = ? AND kind = ?", row.Email, auth.ResetKindControlAdmin).Delete(&auth.ResetToken{}).Error
		return "", generic
	}
	return row.Email, nil
}

// Issue mints a control token for an admin.
func (s *Service) Issue(a *ControlAdmin) (string, error) {
	now := time.Now()
	claims := jwt.MapClaims{
		"sub": a.ID.String(), "jti": uuid.NewString(),
		"type": ControlTokenType, "role": a.Role,
		"iat": now.Unix(), "exp": now.Add(s.ttl).Unix(),
	}
	return jwt.NewWithClaims(jwt.SigningMethodHS256, claims).SignedString(s.key)
}

// Parse verifies a control token and returns its claims. Tenant tokens fail
// the type check even when signed by the same fallback key.
func (s *Service) Parse(token string) (*ControlClaims, error) {
	parsed, err := jwt.ParseWithClaims(token, jwt.MapClaims{}, func(t *jwt.Token) (interface{}, error) {
		if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
			return nil, errors.New("unexpected signing method")
		}
		return s.key, nil
	}, jwt.WithValidMethods([]string{"HS256"}))
	if err != nil || !parsed.Valid {
		return nil, errors.New("invalid token")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, errors.New("invalid token")
	}
	typ, _ := claims["type"].(string)
	if typ != ControlTokenType {
		return nil, errors.New("not a control token")
	}
	sub, _ := claims["sub"].(string)
	id, err := uuid.Parse(sub)
	if err != nil {
		return nil, errors.New("invalid subject")
	}
	jti, _ := claims["jti"].(string)
	if jti == "" {
		return nil, errors.New("missing session")
	}
	role, _ := claims["role"].(string)
	return &ControlClaims{AdminID: id, SessionID: jti, Role: role, Type: typ}, nil
}

// SeedAdmin idempotently ensures a super_admin: created once, then a no-op
// returning created=false so reruns are safe.
func SeedAdmin(db *gorm.DB, name, email, password string) (*ControlAdmin, bool, error) {
	email = strings.ToLower(strings.TrimSpace(email))
	var existing ControlAdmin
	if err := db.Where("email = ?", email).First(&existing).Error; err == nil {
		return &existing, false, nil
	} else if !errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, false, err
	}
	if len(password) < 12 {
		return nil, false, errors.New("seed password must be at least 12 characters")
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(password), bcrypt.DefaultCost)
	if err != nil {
		return nil, false, err
	}
	a := &ControlAdmin{Name: name, Email: email, PasswordHash: string(hash), Role: "super_admin", IsActive: true}
	if err := db.Create(a).Error; err != nil {
		return nil, false, err
	}
	return a, true, nil
}

// LoginDTO carries the control login payload.
type LoginDTO struct {
	Email    string `json:"email" binding:"required,email"`
	Password string `json:"password" binding:"required"`
}
