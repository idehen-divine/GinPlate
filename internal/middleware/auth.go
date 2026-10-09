package middleware

import (
	"context"
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/session"
	"github.com/idehen-divine/GinPlate/pkg/web"
)

// Claims is the authenticated caller identity from the JWT.
type Claims struct {
	UserID      uuid.UUID `json:"sub"`
	SessionID   string    `json:"jti"`
	Version     string    `json:"ver"`
	Role        Role      `json:"role"`
	AuthVersion int       `json:"aver"`
}

// ActiveCheck runs after signature and session checks inside RequireAuth.
// It is set by the users module to enforce active status and auth-version
// revocation. Nil skips the check (tests, listing, local dev).
var ActiveCheck func(c *gin.Context, userID uuid.UUID) error

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

// TokenVersion is the current token shape. Bump it to invalidate all tokens.
const TokenVersion = "v1"

type TokenClaims struct {
	jwt.RegisteredClaims
	SessionID   string `json:"jti"`
	Version     string `json:"ver"`
	Type        string `json:"type"`
	Role        Role   `json:"role"`
	AuthVersion int    `json:"aver"`
}

// RequireAuth validates `Authorization: Bearer <jwt>` (HS256 access tokens
// only) and the session store, so logout takes effect immediately. A nil
// store skips the session check (local development only).
func RequireAuth(key []byte, store session.Store) gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.GetHeader("Authorization")
		tokenStr := strings.TrimSpace(strings.TrimPrefix(h, "Bearer "))
		if tokenStr == "" {
			web.Fail(c, http.StatusUnauthorized, "Unauthenticated.", nil)
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
			web.Fail(c, http.StatusUnauthorized, "Invalid token.", nil)
			c.Abort()
			return
		}
		if store != nil && claims.SessionID != "" {
			if _, ok := store.AccessValid(c.Request.Context(), claims.SessionID); !ok {
				web.Fail(c, http.StatusUnauthorized, "Session expired.", nil)
				c.Abort()
				return
			}
		}
		uid, err := uuid.Parse(claims.Subject)
		if err != nil {
			web.Fail(c, http.StatusUnauthorized, "Invalid subject.", nil)
			c.Abort()
			return
		}
		c.Set("claims", &Claims{
			UserID: uid,
			SessionID: claims.SessionID, Version: claims.Version, Role: claims.Role,
			AuthVersion: claims.AuthVersion,
		})
		if ActiveCheck != nil {
			if err := ActiveCheck(c, uid); err != nil {
				web.Fail(c, http.StatusUnauthorized, "Unauthenticated.", nil)
				c.Abort()
				return
			}
		}
		c.Next()
	}
}

func CurrentClaims(c *gin.Context) *Claims {
	if v, ok := c.Get("claims"); ok {
		if cl, ok := v.(*Claims); ok {
			return cl
		}
	}
	return nil
}
