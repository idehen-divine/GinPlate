package web

import (
	"crypto/subtle"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
)

type DownState struct {
	Secret  string `json:"secret,omitempty"`
	Retry   int    `json:"retry,omitempty"`
	Message string `json:"message,omitempty"`
}

// ReadDownFile returns the state when path exists; missing file means up.
func ReadDownFile(path string) (*DownState, bool, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, false, nil
		}
		return nil, false, err
	}
	var st DownState
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, false, err
	}
	return &st, true, nil
}

func WriteDownFile(path string, st DownState) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	raw, err := json.Marshal(st)
	if err != nil {
		return err
	}
	return os.WriteFile(path, raw, 0o644)
}

func ClearDownFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Maintenance 503s every request while the down file exists (bypass via
// X-Maintenance-Bypass header only). Mount before auth.
func Maintenance(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		st, down, err := ReadDownFile(path)
		if err != nil {
			// Fail closed: an unreadable marker must not silently serve
			// traffic that should be under maintenance.
			Fail(c, http.StatusServiceUnavailable, "Service temporarily unavailable.", nil)
			c.Abort()
			return
		}
		if !down {
			c.Next()
			return
		}
		// Bypass travels in a header only: query strings leak into access
		// logs, proxy logs, browser history, and referrer telemetry.
		// Compared in constant time; rotate the secret per incident.
		if st.Secret != "" && subtle.ConstantTimeCompare([]byte(c.GetHeader("X-Maintenance-Bypass")), []byte(st.Secret)) == 1 {
			c.Next()
			return
		}
		retry := st.Retry
		if retry <= 0 {
			retry = 60
		}
		c.Header("Retry-After", strconv.Itoa(retry))
		msg := st.Message
		if msg == "" {
			msg = "Service temporarily unavailable for maintenance."
		}
		Fail(c, http.StatusServiceUnavailable, msg, nil)
		c.Abort()
	}
}
