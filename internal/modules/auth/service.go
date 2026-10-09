package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	appmail "github.com/idehen-divine/GinPlate/internal/mail"
	"github.com/idehen-divine/GinPlate/internal/mail/password_reset"
	"github.com/idehen-divine/GinPlate/internal/middleware"
	"github.com/idehen-divine/GinPlate/internal/modules/users"
	notifwelcome "github.com/idehen-divine/GinPlate/internal/notifications/welcome"
	"github.com/idehen-divine/GinPlate/pkg/mail"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/queue"
	"github.com/idehen-divine/GinPlate/pkg/resettoken"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"golang.org/x/crypto/bcrypt"
	"gorm.io/gorm"
)

// AuthService is the behavior boundary handlers depend on.
type AuthService interface {
	Register(db *gorm.DB, dto SignupDTO) (*users.User, error)
	Login(db *gorm.DB, dto LoginDTO, tenantSlug string) (*users.User, *TokenPair, error)
	Logout(sessionID string) error
	Refresh(db *gorm.DB, refreshToken, tenantSlug string) (*users.User, *TokenPair, error)
	ForgotPassword(db *gorm.DB, email string) error
	ResetPassword(db *gorm.DB, rawToken, newPassword string) error
	Check(token string) CheckResult
}

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

// RefreshTTL is the fixed refresh-token lifetime (access TTL comes from config).
const RefreshTTL = 30 * 24 * time.Hour

// NewService builds an auth Service. A nil store disables session tracking.
func NewService(key []byte, accessMin int, store session.Store) *Service {
	return &Service{key: key, accessTTL: time.Duration(accessMin) * time.Minute, refreshTTL: RefreshTTL, store: store}
}

// WithMailer attaches a mail sender (nil keeps the service usable without mail).
func (s *Service) WithMailer(m mail.Sender) *Service {
	s.mailer = m
	return s
}

// WithNotifications attaches welcome fan-out for signups (best-effort, never fatal).
func (s *Service) WithNotifications(n *notify.Notifier, q queue.Queue, appName, appURL string) *Service {
	s.notifier = n
	s.notifQueue = q
	s.appName = appName
	s.appURL = appURL
	return s
}

// SendPasswordReset delivers the forgot-password mailable. token is the raw
// one-time secret for the email only: persist HashResetToken(token), never
// the raw token.
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

// MintResetToken creates a random password-reset secret for emailed links.
func MintResetToken() (string, error) {
	var raw [32]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}

// HashResetToken returns the digest to persist for a reset token.
func HashResetToken(rawToken string) string {
	sum := sha256.Sum256([]byte(rawToken))
	return hex.EncodeToString(sum[:])
}

// Register hashes the password and creates a member account (duplicate → 409).
func (s *Service) Register(db *gorm.DB, dto SignupDTO) (*users.User, error) {
	hash, err := bcrypt.GenerateFromPassword([]byte(dto.Password), bcrypt.DefaultCost)
	if err != nil {
		return nil, err
	}
	u := &users.User{Name: dto.Name, Email: dto.Email, PasswordHash: string(hash), Role: "member", IsActive: true, AuthVersion: 1}
	if err := db.Create(u).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return nil, web.Conflict("Email already taken.")
		}
		return nil, web.Wrap(http.StatusInternalServerError, "Could not register.", err)
	}
	s.notifyWelcome(u)
	return u, nil
}

// notifyWelcome queues the welcome notification (best-effort).
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

// Login verifies credentials and active status, then issues a token pair
// bound to tenantSlug. Failures share one message so callers can't probe
// which accounts exist.
func (s *Service) Login(db *gorm.DB, dto LoginDTO, tenantSlug string) (*users.User, *TokenPair, error) {
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
	pair, err := s.issue(&u, tenantSlug)
	if err != nil {
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not log in.", err)
	}
	return &u, pair, nil
}

// mint signs a token pair without touching the store. The caller must persist
// the JTIs: unstored JTIs never validate (fail closed). tid binds both
// tokens to one tenant (see RequireAuth).
func (s *Service) mint(u *users.User, tenantSlug string) (pair *TokenPair, accessJti, refreshJti string, err error) {
	jti := uuid.NewString()
	rjti := uuid.NewString()
	now := time.Now()
	accessClaims := jwt.MapClaims{
		"sub": u.ID.String(),
		"jti": jti, "ver": "v1", "type": "access", "role": u.Role, "tid": tenantSlug, "aver": u.AuthVersion,
		"iat": now.Unix(), "exp": now.Add(s.accessTTL).Unix(),
	}
	access, err := jwt.NewWithClaims(jwt.SigningMethodHS256, accessClaims).SignedString(s.key)
	if err != nil {
		return nil, "", "", err
	}
	refreshClaims := jwt.MapClaims{
		"sub": u.ID.String(),
		"jti": rjti, "ver": "v1", "type": "refresh", "role": u.Role, "tid": tenantSlug, "aver": u.AuthVersion,
		"iat": now.Unix(), "exp": now.Add(s.refreshTTL).Unix(),
	}
	refresh, err := jwt.NewWithClaims(jwt.SigningMethodHS256, refreshClaims).SignedString(s.key)
	if err != nil {
		return nil, "", "", err
	}
	return &TokenPair{AccessToken: access, RefreshToken: refresh, TokenType: "Bearer", ExpiresIn: int(s.accessTTL.Seconds())}, jti, rjti, nil
}

// issue mints a pair and links both session halves. A Link failure is
// returned so untracked tokens are never handed out.
func (s *Service) issue(u *users.User, tenantSlug string) (*TokenPair, error) {
	pair, jti, rjti, err := s.mint(u, tenantSlug)
	if err != nil {
		return nil, err // caller wraps with its operation message
	}
	if s.store != nil {
		if err := s.store.Link(context.Background(), jti, rjti, u.ID.String(), s.accessTTL, s.refreshTTL); err != nil {
			return nil, web.Wrap(http.StatusInternalServerError, "Could not create session.", err)
		}
	}
	return pair, nil
}

// Logout destroys the whole session (idempotent; backend failures returned).
func (s *Service) Logout(sessionID string) error {
	if s.store == nil || sessionID == "" {
		return nil
	}
	ctx := context.Background()
	rjti, _ := s.store.AccessValid(ctx, sessionID)
	if err := s.store.Unlink(ctx, sessionID, rjti); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not log out.", err)
	}
	return nil
}

// Refresh rotates a refresh token into a new pair in one atomic replacement,
// so exactly one concurrent use wins. Revoked or replayed tokens get 401.
// The token's tid must match tenantSlug: cross-tenant replay gets 401
// before any session or database access. An empty tenantSlug is a server
// misconfiguration (unresolved tenant), so it fails 500 instead of
// allowing tid=="" to match slug=="".
func (s *Service) Refresh(db *gorm.DB, refreshToken, tenantSlug string) (*users.User, *TokenPair, error) {
	if tenantSlug == "" {
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not refresh.", errors.New("tenant required"))
	}
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
	// The refresh token is bound to the tenant that minted it: presenting
	// it under another tenant is a cross-tenant replay, not a revocation.
	if tid, _ := claims["tid"].(string); tid != tenantSlug {
		return nil, nil, web.Unauthorized("Invalid refresh token.")
	}
	sub, _ := claims["sub"].(string)
	if sub == "" {
		return nil, nil, web.Unauthorized("Invalid refresh token.")
	}
	ctx := context.Background()
	if s.store != nil {
		// Early rejection for revoked tokens; ReplaceRefresh below arbitrates races.
		if _, ok := s.store.RefreshValid(ctx, rjti); !ok {
			return nil, nil, web.Unauthorized("Session revoked.")
		}
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
	// A refresh token minted before a privilege change (role bump) must not
	// mint fresh tokens: its aver no longer matches the row.
	if aver, _ := claims["aver"].(float64); int(aver) != u.AuthVersion {
		return nil, nil, web.Unauthorized("Session revoked.")
	}
	pair, newAccessJti, newRefreshJti, err := s.mint(&u, tenantSlug)
	if err != nil {
		return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not refresh.", err)
	}
	if s.store != nil {
		// Only hand out the pair when the replacement commits (fail closed).
		_, ok, err := s.store.ReplaceRefresh(ctx, rjti, newAccessJti, newRefreshJti, u.ID.String(), s.accessTTL, s.refreshTTL)
		if err != nil {
			return nil, nil, web.Wrap(http.StatusInternalServerError, "Could not refresh.", err)
		}
		if !ok {
			return nil, nil, web.Unauthorized("Session revoked.")
		}
	}
	return &u, pair, nil
}

// ForgotPassword issues a reset token for an active account and emails the
// link. Unknown, inactive, or missing accounts return nil: callers always
// report success so addresses can't be probed. Only transient backend
// failures surface as errors.
func (s *Service) ForgotPassword(db *gorm.DB, email string) error {
	email = strings.ToLower(strings.TrimSpace(email))
	if email == "" {
		return nil
	}
	var u users.User
	if err := db.Where("email = ?", email).First(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return web.Wrap(http.StatusInternalServerError, "Could not request reset.", err)
	}
	if !u.IsActive {
		return nil
	}
	ctx := context.Background()
	raw, err := IssueResetToken(ctx, db, u.Email, ResetKindUser, ResetTokenTTL)
	if err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not request reset.", err)
	}
	if err := s.SendPasswordReset(ctx, s.appURL, u.Name, u.Email, raw, int(ResetTokenTTL.Minutes())); err != nil {
		slog.Warn("password reset mail failed", "user", u.ID.String(), "err", err)
	}
	return nil
}

// ResetPassword consumes a reset token and sets a new password, revoking
// every session for the account. Invalid and expired tokens share one
// generic failure so tokens can't be probed.
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
	var u users.User
	if err := db.WithContext(ctx).Where("email = ?", email).First(&u).Error; err != nil {
		return web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
	}
	hash, err := bcrypt.GenerateFromPassword([]byte(newPassword), bcrypt.DefaultCost)
	if err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
	}
	if s.store != nil {
		// Revoke first: revocation failure aborts before anything is
		// consumed, so the token stays valid for a retry.
		if err := s.store.RevokeUser(ctx, u.ID.String()); err != nil {
			return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
		}
	}
	u.PasswordHash = string(hash)
	if err := db.WithContext(ctx).Save(&u).Error; err != nil {
		if errors.Is(err, gorm.ErrDuplicatedKey) {
			return web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
		}
		return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
	}
	if err := ConsumeResetToken(ctx, db, email, ResetKindUser, rawToken); err != nil {
		if errors.Is(err, ErrResetInvalid) || errors.Is(err, ErrResetExpired) {
			return web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
		}
		return web.Wrap(http.StatusInternalServerError, "Could not reset password.", err)
	}
	return nil
}

// emailForResetToken resolves the account address for a presented raw
// token without consuming it, so revocation and the password update can
// run first with a retry-safe token still in place.
func (s *Service) emailForResetToken(ctx context.Context, db *gorm.DB, rawToken string) (string, error) {
	generic := web.Wrap(http.StatusBadRequest, "Invalid or expired reset token.", nil)
	var row ResetToken
	if err := db.WithContext(ctx).
		Where("kind = ? AND token_hash = ?", ResetKindUser, resettoken.Hash(rawToken)).
		First(&row).Error; err != nil {
		return "", generic
	}
	if !time.Now().Before(row.ExpiresAt) {
		_ = db.WithContext(ctx).Where("email = ? AND kind = ?", row.Email, ResetKindUser).Delete(&ResetToken{}).Error
		return "", generic
	}
	return row.Email, nil
}

type CheckResult struct {
	Valid     bool   `json:"valid"`
	Type      string `json:"type,omitempty"`
	Role      string `json:"role,omitempty"`
	ExpiresAt int64  `json:"expires_at,omitempty"`
}

func (s *Service) Check(token string) CheckResult {
	claims, err := s.parse(token)
	if err != nil {
		return CheckResult{Valid: false}
	}
	if ver, _ := claims["ver"].(string); ver != middleware.TokenVersion {
		return CheckResult{Valid: false}
	}
	exp, _ := claims["exp"].(float64)
	typ, _ := claims["type"].(string)
	role, _ := claims["role"].(string)
	return CheckResult{Valid: true, Type: typ, Role: role, ExpiresAt: int64(exp)}
}

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
