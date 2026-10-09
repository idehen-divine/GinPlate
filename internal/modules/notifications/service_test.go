package notifications

import (
	"context"
	"testing"
	"time"

	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

// stubNotificationStore is an in-memory notify.Store for service tests.
type stubNotificationStore struct{ rows []notify.Record }

func (s *stubNotificationStore) Create(_ context.Context, to notify.Notifiable, notifType string, _ map[string]any) (*notify.Record, error) {
	rec := &notify.Record{NotifiableType: to.Type, NotifiableID: to.ID, Type: notifType, CreatedAt: time.Now()}
	_ = rec.BeforeCreate(nil)
	s.rows = append(s.rows, *rec)
	return rec, nil
}

func (s *stubNotificationStore) List(_ context.Context, _ notify.Notifiable, _, _ int) ([]notify.Record, int64, error) {
	return s.rows, int64(len(s.rows)), nil
}

func (s *stubNotificationStore) MarkRead(_ context.Context, to notify.Notifiable, id uuid.UUID) error {
	for i := range s.rows {
		if s.rows[i].ID == id && s.rows[i].NotifiableID == to.ID {
			now := time.Now()
			s.rows[i].ReadAt = &now
			return nil
		}
	}
	return gorm.ErrRecordNotFound
}

func (s *stubNotificationStore) MarkAllRead(_ context.Context, _ notify.Notifiable) error { return nil }

func (s *stubNotificationStore) UnreadCount(_ context.Context, _ notify.Notifiable) (int64, error) {
	return int64(len(s.rows)), nil
}

func TestNotificationsService(t *testing.T) {
	ctx := context.Background()
	to := notify.Notifiable{Type: "user", ID: "u1"}
	service := NewService(&stubNotificationStore{})

	t.Run("list-paginates-with-unread", func(t *testing.T) {
		result, err := service.List(ctx, nil, to, web.ListFilter{Limit: 25})
		if err != nil {
			t.Fatal(err)
		}
		if result.Unread != 0 || result.Notifications.Total != 0 {
			t.Fatalf("empty inbox: %+v", result)
		}
	})

	t.Run("read-missing-is-not-found", func(t *testing.T) {
		if err := service.Read(ctx, nil, to, uuid.New()); err != gorm.ErrRecordNotFound {
			t.Fatalf("missing row: got %v, want ErrRecordNotFound", err)
		}
	})
}
