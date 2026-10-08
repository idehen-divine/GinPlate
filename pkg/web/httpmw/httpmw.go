package httpmw

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/didip/tollbooth"
	"github.com/gin-gonic/gin"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

// CORSConfig controls cross-origin behavior. Use "*" for local dev;
// in production set explicit origins (e.g. https://app.example.com).
type CORSConfig struct {
	AllowedOrigins   []string
	AllowCredentials bool
}

// CORS handles preflight and sets ACAO headers. An empty allow-list means
// allow-all and is only appropriate for local development; production must
// set explicit origins (validated at startup by config.Validate).
func CORS(cfg CORSConfig) gin.HandlerFunc {
	allowAll := len(cfg.AllowedOrigins) == 0
	allowed := map[string]bool{}
	for _, o := range cfg.AllowedOrigins {
		o = strings.TrimSpace(o)
		if o != "" {
			allowed[o] = true
		}
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		allowOrigin := "*"
		if !allowAll {
			if allowed[origin] {
				allowOrigin = origin
			} else if origin != "" {
				c.Next()
				return
			} else {
				allowOrigin = cfg.AllowedOrigins[0]
			}
		}
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", allowOrigin)
		// Caches must key on Origin when the value varies per caller.
		h.Set("Vary", "Origin")
		if cfg.AllowCredentials && allowOrigin != "*" {
			h.Set("Access-Control-Allow-Credentials", "true")
		}
		h.Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With")
		h.Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, PATCH, DELETE")
		h.Set("Access-Control-Max-Age", "86400")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// Security sets baseline security headers.
func Security() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-XSS-Protection", "1; mode=block")
		h.Set("Strict-Transport-Security", "max-age=31536000; includeSubDomains; preload")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("Content-Security-Policy", "default-src 'self';")
		h.Set("X-Permitted-Cross-Domain-Policies", "none")
		h.Set("Referrer-Policy", "no-referrer")
		if c.Request.Method == http.MethodOptions {
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}

// RateLimit caps requests per second per instance (tollbooth, in-memory).
// NOTE: per-instance only, not a distributed defense. For multi-replica
// deployments add a Redis sliding-window limiter or enforce equivalent
// controls at the API gateway, with stricter per-endpoint limits on
// login, signup, refresh, password reset, and token-check endpoints.
func RateLimit(requestsPerSecond float64) gin.HandlerFunc {
	if requestsPerSecond <= 0 {
		requestsPerSecond = 10
	}
	lmt := tollbooth.NewLimiter(requestsPerSecond, nil)
	return func(c *gin.Context) {
		httpErr := tollbooth.LimitByRequest(lmt, c.Writer, c.Request)
		if httpErr != nil {
			c.Header("Retry-After", strconv.Itoa(60))
			web.Fail(c, httpErr.StatusCode, httpErr.Message, nil)
			c.Abort()
			return
		}
		c.Next()
	}
}
