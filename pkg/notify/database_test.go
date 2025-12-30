package notify

import (
	"context"
	"os"
	"testing"

	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/database"
)

// TestGormStoreLive runs the store against a live database only when
// TEST_MYSQL_DSN or TEST_PGSQL_DSN is set; otherwise it skips so CI stays
// hermetic. The table is created raw (mirroring the goose migration)
// because migrations run through the CLI, not the test binary.
func TestGormStoreLive(t *testing.T) {
	dsn, driver := os.Getenv("TEST_MYSQL_DSN"), "mysql"
	if dsn == "" {
		dsn, driver = os.Getenv("TEST_PGSQL_DSN"), "pgsql"
	}
	if dsn == "" {
		t.Skip("set TEST_MYSQL_DSN or TEST_PGSQL_DSN for the live notification store test")
	}
	db, err := database.Connect(driver, dsn)
	if err != nil {
		t.Fatalf("connect: %v", err)
	}
	if err := db.Exec(`DROP TABLE IF EXISTS notifications`).Error; err != nil {
		t.Fatal(err)
	}
	idCol := "id CHAR(36) PRIMARY KEY"
	ts := "TIMESTAMP NULL DEFAULT NULL"
	created := "created TIMESTAMP NOT NULL DEFAULT CURRENT_TIMESTAMP"
	if driver == "pgsql" {
		idCol = "id UUID PRIMARY KEY DEFAULT gen_random_uuid()"
		ts = "TIMESTAMPTZ"
		created = "created_at TIMESTAMPTZ NOT NULL DEFAULT now()"
	}
	if err := db.Exec(`CREATE TABLE notifications (
		` + idCol + `,
		notifiable_type VARCHAR(64) NOT NULL,
		notifiable_id VARCHAR(64) NOT NULL,
		type VARCHAR(128) NOT NULL,
		data TEXT NOT NULL,
		read_at ` + ts + `,
		` + created + `
	)`).Error; err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = db.Exec(`DROP TABLE IF EXISTS notifications`).Error })

	ctx := context.Background()
	store := NewStore(db)
	alice, bob := UserNotifiable("u-alice", "a@example.com"), UserNotifiable("u-bob", "b@example.com")

	rec, err := store.Create(ctx, alice, "welcome", map[string]any{"title": "Hi"})
	if err != nil {
		t.Fatal(err)
	}
	if rec.ID == uuid.Nil {
		t.Fatal("expected a UUID id")
	}
	if _, err := store.Create(ctx, alice, "welcome", map[string]any{"title": "Again"}); err != nil {
		t.Fatal(err)
	}

	rows, total, err := store.List(ctx, alice, 25, 0)
	if err != nil || total != 2 || len(rows) != 2 {
		t.Fatalf("list = %d rows, total=%d, err=%v", len(rows), total, err)
	}
	if rows[0].DecodedData()["title"] == "" {
		t.Fatal("data did not round-trip")
	}
	if _, total, err := store.List(ctx, bob, 25, 0); err != nil || total != 0 {
		t.Fatalf("bob sees alice rows: total=%d err=%v", total, err)
	}

	n, err := store.UnreadCount(ctx, alice)
	if err != nil || n != 2 {
		t.Fatalf("unread = %d, err=%v", n, err)
	}
	if err := store.MarkRead(ctx, alice, rows[0].ID); err != nil {
		t.Fatal(err)
	}
	// Bob cannot mark alice's row: scoped miss, not a silent success.
	if err := store.MarkRead(ctx, bob, rows[1].ID); err == nil {
		t.Fatal("cross-notifiable mark: expected error")
	}
	if n, _ := store.UnreadCount(ctx, alice); n != 1 {
		t.Fatalf("unread after one read = %d, want 1", n)
	}
	if err := store.MarkAllRead(ctx, alice); err != nil {
		t.Fatal(err)
	}
	if n, _ := store.UnreadCount(ctx, alice); n != 0 {
		t.Fatalf("unread after read-all = %d, want 0", n)
	}
}
