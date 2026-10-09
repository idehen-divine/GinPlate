package httpmw

import (
	"net/http"
	"strconv"
	"strings"

	"github.com/didip/tollbooth"
	"github.com/gin-gonic/gin"

	"github.com/idehen-divine/GinPlate/pkg/web"
)

type CORSConfig struct {
	AllowedOrigins   []string
	AllowCredentials bool
}

// CORS handles preflight. Empty allow-list means allow-all (dev only;
// production requires explicit origins).
func CORS(config CORSConfig) gin.HandlerFunc {
	allowAll := len(config.AllowedOrigins) == 0
	allowed := map[string]bool{}
	for _, o := range config.AllowedOrigins {
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
				allowOrigin = config.AllowedOrigins[0]
			}
		}
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", allowOrigin)
		h.Set("Vary", "Origin")
		if config.AllowCredentials && allowOrigin != "*" {
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

// RateLimit caps requests per second per instance (not distributed: use a
// gateway limiter for multi-replica fleets).
// MaxBodyBytes caps request body size via http.MaxBytesReader, so oversized
// payloads are rejected before handlers or validation read them. Reads past
// the limit fail binding, which handlers already render as 4xx.
func MaxBodyBytes(limit int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if limit > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, limit)
		}
		c.Next()
	}
}

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
