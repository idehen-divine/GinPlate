package web

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"gorm.io/gorm"
)

// Claims mirrors the JWT payload: jti (session id), ver.
type Claims struct {
	UserID    uuid.UUID `json:"sub"`
	SessionID string    `json:"jti"`
	Version   string    `json:"ver"`
	Role      Role      `json:"role"`
}

// Role is an account role. Compare against RoleAdmin/RoleMember, never raw
// strings: a typo'd literal fails silently, a typo'd identifier fails to
// compile.
type Role string

const (
	RoleAdmin  Role = "admin"
	RoleMember Role = "member"
)

type ctxKey string

const userKey ctxKey = "user"

// WithClaims stores auth claims in a context (non-HTTP call chains).
func WithClaims(ctx context.Context, c *Claims) context.Context {
	return context.WithValue(ctx, userKey, c)
}

// ClaimsFromContext extracts auth claims stored by WithClaims.
func ClaimsFromContext(ctx context.Context) (*Claims, bool) {
	c, ok := ctx.Value(userKey).(*Claims)
	return c, ok
}

// TokenVersion is the current token shape version. Bump it to invalidate
// every outstanding token at once (old tokens fail the version check).
const TokenVersion = "v1"

// TokenClaims is the parsed JWT shape: session id, version, type, role.
type TokenClaims struct {
	jwt.RegisteredClaims
	SessionID string `json:"jti"`
	Version   string `json:"ver"`
	Type      string `json:"type"`
	Role      Role   `json:"role"`
}

// RequireAuth validates `Authorization: Bearer <jwt>` then checks the
// session store for the access half, so logout invalidation takes effect
// immediately. key is the raw HMAC signing key. Only HS256 access tokens
// with the current version are accepted; refresh tokens are rejected.
// A nil store disables the session check (tokens validate by signature
// only) and must only be used for local development.
func RequireAuth(key []byte, store session.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tokenStr := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if tokenStr == "" {
			Fail(c, http.StatusUnauthorized, "Unauthenticated.", nil)
			c.Abort()
			return
		}
		var claims TokenClaims
		tok, err := jwt.ParseWithClaims(tokenStr, &claims, func(t *jwt.Token) (interface{}, error) {
			if _, ok := t.Method.(*jwt.SigningMethodHMAC); !ok {
				return nil, fmt.Errorf("unexpected signing method %v", t.Header["alg"])
			}
			return key, nil
		}, jwt.WithValidMethods([]string{"HS256"}))
		if err != nil || !tok.Valid || claims.Version != TokenVersion || claims.Type != "access" {
			Fail(c, http.StatusUnauthorized, "Invalid token.", nil)
			c.Abort()
			return
		}
		if store != nil && claims.SessionID != "" {
			if _, ok := store.AccessValid(c.Request.Context(), claims.SessionID); !ok {
				Fail(c, http.StatusUnauthorized, "Session expired.", nil)
				c.Abort()
				return
			}
		}
		uid, err := uuid.Parse(claims.Subject)
		if err != nil {
			Fail(c, http.StatusUnauthorized, "Invalid subject.", nil)
			c.Abort()
			return
		}
		c.Set("claims", &Claims{
			UserID:    uid,
			SessionID: claims.SessionID, Version: claims.Version, Role: claims.Role,
		})
		c.Next()
	}
}

// CurrentClaims returns the authenticated claims.
func CurrentClaims(c *gin.Context) *Claims {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*Claims); ok {
			return cl
		}
	}
	return nil
}

// RequireRole rejects callers whose role claim is not listed. Mount after
// RequireAuth so claims exist: missing claims mean 401, a wrong role means
// 403. Example: RequireRole(web.RoleAdmin) for admin-only routes.
func RequireRole(roles ...Role) gin.HandlerFunc {
	return func(c *gin.Context) {
		cl := CurrentClaims(c)
		if cl == nil {
			Fail(c, http.StatusUnauthorized, "Unauthenticated.", nil)
			c.Abort()
			return
		}
		for _, r := range roles {
			if cl.Role == r {
				c.Next()
				return
			}
		}
		Fail(c, http.StatusForbidden, "Forbidden.", nil)
		c.Abort()
	}
}

// MustDB returns the request's *gorm.DB. It panics when no provider ran —
// mount ProvideDB before any handler that touches data.
func MustDB(c *gin.Context) *gorm.DB {
	return c.MustGet("db").(*gorm.DB)
}

// ProvideDB pins one *gorm.DB on every request.
func ProvideDB(db *gorm.DB) gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set("db", db)
		c.Next()
	}
}
