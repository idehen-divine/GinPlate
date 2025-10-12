package redis

import (
	"net"
	"testing"
	"time"

	"github.com/alicebob/miniredis/v2"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// TestRedis is the single entry point for every redis test: client option
// wiring plus dial behavior against dead and live servers.
func TestRedis(t *testing.T) {
	t.Run("client-options", func(t *testing.T) {
		c := NewClient("127.0.0.1:6379", "alice", "s3cret", 3)
		defer func() { _ = c.Close() }()
		opt := c.Options()
		if opt.Addr != "127.0.0.1:6379" || opt.Username != "alice" || opt.Password != "s3cret" || opt.DB != 3 {
			t.Fatalf("options not applied: %+v", opt)
		}
	})

	t.Run("dial-unreachable-returns-nil", func(t *testing.T) {
		cfg := config.Redis{Host: "127.0.0.1", Port: "1"}
		if got := DialOrNil(cfg, 500*time.Millisecond); got != nil {
			_ = got.Close()
			t.Fatal("expected nil for unreachable redis")
		}
	})

	t.Run("dial-live-returns-client", func(t *testing.T) {
		mr, err := miniredis.Run()
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(mr.Close)
		host, port, err := net.SplitHostPort(mr.Addr())
		if err != nil {
			t.Fatal(err)
		}
		c := DialOrNil(config.Redis{Host: host, Port: port}, 2*time.Second)
		if c == nil {
			t.Fatal("expected client for live redis")
		}
		defer func() { _ = c.Close() }()
	})
}
