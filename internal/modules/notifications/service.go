package notifications

import (
	"context"
	"errors"
	"net/http"

	"github.com/google/uuid"
	"github.com/idehen-divine/GinPlate/pkg/notify"
	"github.com/idehen-divine/GinPlate/pkg/web"
	"gorm.io/gorm"
)

type Item struct {
	ID        uuid.UUID      `json:"id"`
	Type      string         `json:"type"`
	Data      map[string]any `json:"data"`
	ReadAt    *string        `json:"read_at"`
	CreatedAt string         `json:"created_at"`
}

type ListResult struct {
	Notifications web.ListResult[Item] `json:"notifications"`
	Unread        int64                `json:"unread"`
}

// NotificationService is the behavior boundary handlers depend on.
type NotificationService interface {
	List(ctx context.Context, db *gorm.DB, to notify.Notifiable, filter web.ListFilter) (ListResult, error)
	Read(ctx context.Context, db *gorm.DB, to notify.Notifiable, id uuid.UUID) error
	ReadAll(ctx context.Context, db *gorm.DB, to notify.Notifiable) error
}

type Service struct{ store notify.Store }

// NewService wires a Store; nil selects the GORM implementation.
func NewService(store notify.Store) *Service { return &Service{store: store} }

func (s *Service) storeFor(db *gorm.DB) notify.Store {
	if s.store != nil {
		return s.store
	}
	return notify.NewStore(db)
}

func toItem(r notify.Record) Item {
	var readAt *string
	if r.ReadAt != nil {
		s := r.ReadAt.Format("2006-01-02T15:04:05Z07:00")
		readAt = &s
	}
	return Item{
		ID:        r.ID,
		Type:      r.Type,
		Data:      r.DecodedData(),
		ReadAt:    readAt,
		CreatedAt: r.CreatedAt.Format("2006-01-02T15:04:05Z07:00"),
	}
}

func (s *Service) List(ctx context.Context, db *gorm.DB, to notify.Notifiable, filter web.ListFilter) (ListResult, error) {
	store := s.storeFor(db)
	rows, total, err := store.List(ctx, to, filter.Limit, filter.Offset)
	if err != nil {
		return ListResult{}, web.Wrap(http.StatusInternalServerError, "Could not list notifications.", err)
	}
	unread, err := store.UnreadCount(ctx, to)
	if err != nil {
		return ListResult{}, web.Wrap(http.StatusInternalServerError, "Could not list notifications.", err)
	}
	items := make([]Item, 0, len(rows))
	for _, r := range rows {
		items = append(items, toItem(r))
	}
	return ListResult{Notifications: web.PagedResult(items, total, filter), Unread: unread}, nil
}

// Read marks one row read. Cross-user rows are indistinguishable from
// missing ones (404 either way).
func (s *Service) Read(ctx context.Context, db *gorm.DB, to notify.Notifiable, id uuid.UUID) error {
	if err := s.storeFor(db).MarkRead(ctx, to, id); err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return err
		}
		return web.Wrap(http.StatusInternalServerError, "Could not mark notification read.", err)
	}
	return nil
}

func (s *Service) ReadAll(ctx context.Context, db *gorm.DB, to notify.Notifiable) error {
	if err := s.storeFor(db).MarkAllRead(ctx, to); err != nil {
		return web.Wrap(http.StatusInternalServerError, "Could not mark notifications read.", err)
	}
	return nil
}
