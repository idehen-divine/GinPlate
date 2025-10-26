package web

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strconv"

	"github.com/gin-gonic/gin"
)

// DownState is the maintenance marker written by `ginplate down` and read
// by Maintenance. Secret enables the bypass; Retry feeds the Retry-After
// header; Message overrides the default 503 body.
type DownState struct {
	Secret  string `json:"secret,omitempty"`
	Retry   int    `json:"retry,omitempty"`
	Message string `json:"message,omitempty"`
}

// ReadDownFile returns the maintenance state when path exists. A missing
// file is not an error — it simply means the app is up.
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

// WriteDownFile creates parent dirs and writes the maintenance marker.
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

// ClearDownFile removes the maintenance marker. A missing file is success.
func ClearDownFile(path string) error {
	if err := os.Remove(path); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

// Maintenance rejects every request with 503 while the down file exists,
// except callers presenting the bypass secret via ?secret= or the
// X-Maintenance-Bypass header. Mount it before auth so maintenance covers
// the whole API surface.
func Maintenance(path string) gin.HandlerFunc {
	return func(c *gin.Context) {
		st, down, err := ReadDownFile(path)
		if err != nil || !down {
			c.Next()
			return
		}
		if st.Secret != "" && (c.Query("secret") == st.Secret || c.GetHeader("X-Maintenance-Bypass") == st.Secret) {
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
