package storage

import (
	"context"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/credentials"
	"github.com/idehen-divine/GinPlate/pkg/config"
)

// TestStorage is the single entry point for every storage test: local disk
// round-trip, traversal guards, and URL rules, plus offline S3 presigning
// and key validation.
func TestStorage(t *testing.T) {
	t.Run("local/round-trip", func(t *testing.T) {
		ctx := context.Background()
		d, err := Open(config.Filesystem{Disk: "local", Root: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if ok, err := d.Exists(ctx, "a/b.txt"); err != nil || ok {
			t.Fatalf("exists before put = %v, %v", ok, err)
		}
		if err := d.Put(ctx, "a/b.txt", strings.NewReader("hello")); err != nil {
			t.Fatal(err)
		}
		if ok, err := d.Exists(ctx, "a/b.txt"); err != nil || !ok {
			t.Fatalf("exists after put = %v, %v", ok, err)
		}
		rc, err := d.Get(ctx, "a/b.txt")
		if err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, 5)
		if _, err := rc.Read(buf); err != nil {
			t.Fatal(err)
		}
		_ = rc.Close()
		if string(buf) != "hello" {
			t.Fatalf("got %q", buf)
		}
		if err := d.Delete(ctx, "a/b.txt"); err != nil {
			t.Fatal(err)
		}
		if _, err := d.Get(ctx, "a/b.txt"); err != ErrNotFound {
			t.Fatalf("get after delete = %v, want ErrNotFound", err)
		}
		if err := d.Delete(ctx, "a/b.txt"); err != nil {
			t.Fatalf("second delete should succeed: %v", err)
		}
	})

	t.Run("local/traversal-guard", func(t *testing.T) {
		ctx := context.Background()
		d, err := Open(config.Filesystem{Disk: "local", Root: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		for _, bad := range []string{"", "/etc/passwd", "../out", "a/../../out", ".."} {
			if err := d.Put(ctx, bad, strings.NewReader("x")); err == nil {
				t.Errorf("Put(%q) accepted", bad)
			}
			if _, err := d.Get(ctx, bad); err == nil {
				t.Errorf("Get(%q) accepted", bad)
			}
			if _, err := d.Exists(ctx, bad); err == nil {
				t.Errorf("Exists(%q) accepted", bad)
			}
			if err := d.Delete(ctx, bad); err == nil {
				t.Errorf("Delete(%q) accepted", bad)
			}
		}
	})

	t.Run("local/urls", func(t *testing.T) {
		ctx := context.Background()
		priv, err := Open(config.Filesystem{Disk: "local", Root: t.TempDir()})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := priv.(URLer).URL(ctx, "a.txt"); err == nil {
			t.Fatal("private disk should not issue URLs")
		}
		pub, err := Open(config.Filesystem{Disk: "public", PublicRoot: t.TempDir(), PublicURL: "https://cdn.example.com/storage"})
		if err != nil {
			t.Fatal(err)
		}
		u, err := pub.(URLer).URL(ctx, "avatars/ada.png")
		if err != nil {
			t.Fatal(err)
		}
		if u != "https://cdn.example.com/storage/avatars/ada.png" {
			t.Fatalf("unexpected public URL %q", u)
		}
	})

	t.Run("open/unknown-driver", func(t *testing.T) {
		if _, err := Open(config.Filesystem{Disk: "s4"}); err == nil {
			t.Fatal("expected error for unknown disk")
		}
	})

	t.Run("s3/requires-bucket", func(t *testing.T) {
		if _, err := NewS3(config.S3{Region: "us-east-1"}); err == nil {
			t.Fatal("expected error for missing bucket")
		}
	})

	t.Run("s3/presign-offline", func(t *testing.T) {
		sdk := aws.Config{
			Region:      "us-east-1",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		}
		d := newS3(sdk, "my-bucket", false)
		u, err := d.URL(context.Background(), "avatars/ada.png")
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(u, "my-bucket") || !strings.Contains(u, "avatars/ada.png") {
			t.Fatalf("presigned URL missing bucket/key: %q", u)
		}
	})

	t.Run("s3/key-validation", func(t *testing.T) {
		sdk := aws.Config{
			Region:      "us-east-1",
			Credentials: credentials.NewStaticCredentialsProvider("AKID", "SECRET", ""),
		}
		d := newS3(sdk, "my-bucket", false)
		ctx := context.Background()
		for _, bad := range []string{"", "/abs.png", "../out.png"} {
			if err := d.Put(ctx, bad, strings.NewReader("x")); err == nil {
				t.Errorf("Put(%q) accepted", bad)
			}
			if _, err := d.URL(ctx, bad); err == nil {
				t.Errorf("URL(%q) accepted", bad)
			}
		}
	})
}
