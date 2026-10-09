package notify

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/google/uuid"
	"gorm.io/gorm"
)

const (
	ChannelDatabase = "database"
	ChannelMail     = "mail"
)

type Record struct {
	ID             uuid.UUID  `gorm:"type:char(36);primaryKey" json:"id"`
	NotifiableType string     `gorm:"not null;index" json:"-"`
	NotifiableID   string     `gorm:"not null;index" json:"-"`
	Type           string     `gorm:"not null" json:"type"`
	Data           string     `gorm:"type:text;not null" json:"-"`
	ReadAt         *time.Time `json:"read_at"`
	CreatedAt      time.Time  `json:"created_at"`
}

func (Record) TableName() string { return "notifications" }

func (r *Record) BeforeCreate(_ *gorm.DB) error {
	if r.ID == uuid.Nil {
		r.ID = uuid.New()
	}
	return nil
}

func (r Record) DecodedData() map[string]any {
	var out map[string]any
	if err := json.Unmarshal([]byte(r.Data), &out); err != nil {
		return map[string]any{}
	}
	return out
}

type Store interface {
	Create(ctx context.Context, to Notifiable, notifType string, data map[string]any) (*Record, error)
	List(ctx context.Context, to Notifiable, limit, offset int) (rows []Record, total int64, err error)
	MarkRead(ctx context.Context, to Notifiable, id uuid.UUID) error
	MarkAllRead(ctx context.Context, to Notifiable) error
	UnreadCount(ctx context.Context, to Notifiable) (int64, error)
}

type GormStore struct{ db *gorm.DB }

func NewStore(db *gorm.DB) *GormStore { return &GormStore{db: db} }

// scope narrows queries to one notifiable's rows.
func scope(db *gorm.DB, to Notifiable) *gorm.DB {
	return db.Where("notifiable_type = ? AND notifiable_id = ?", to.Type, to.ID)
}

func (s *GormStore) Create(ctx context.Context, to Notifiable, notifType string, data map[string]any) (*Record, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return nil, fmt.Errorf("notify: encode data: %w", err)
	}
	rec := &Record{
		NotifiableType: to.Type,
		NotifiableID:   to.ID,
		Type:           notifType,
		Data:           string(raw),
	}
	if err := s.db.WithContext(ctx).Create(rec).Error; err != nil {
		return nil, err
	}
	return rec, nil
}

func (s *GormStore) List(ctx context.Context, to Notifiable, limit, offset int) ([]Record, int64, error) {
	if limit <= 0 || limit > 100 {
		limit = 25
	}
	if offset < 0 {
		offset = 0
	}
	var total int64
	if err := scope(s.db.WithContext(ctx).Model(&Record{}), to).Count(&total).Error; err != nil {
		return nil, 0, err
	}
	var rows []Record
	if err := scope(s.db.WithContext(ctx), to).
		Order("created_at DESC").Limit(limit).Offset(offset).Find(&rows).Error; err != nil {
		return nil, 0, err
	}
	return rows, total, nil
}

// MarkRead stamps one row read (foreign/missing rows report not-found).
func (s *GormStore) MarkRead(ctx context.Context, to Notifiable, id uuid.UUID) error {
	res := scope(s.db.WithContext(ctx).Model(&Record{}), to).
		Where("id = ? AND read_at IS NULL", id.String()).Update("read_at", time.Now())
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return gorm.ErrRecordNotFound
	}
	return nil
}

func (s *GormStore) MarkAllRead(ctx context.Context, to Notifiable) error {
	return scope(s.db.WithContext(ctx).Model(&Record{}), to).
		Where("read_at IS NULL").Update("read_at", time.Now()).Error
}

func (s *GormStore) UnreadCount(ctx context.Context, to Notifiable) (int64, error) {
	var n int64
	if err := scope(s.db.WithContext(ctx).Model(&Record{}), to).
		Where("read_at IS NULL").Count(&n).Error; err != nil {
		return 0, err
	}
	return n, nil
}

type DatabaseChannel struct{}

func (DatabaseChannel) Name() string { return ChannelDatabase }

func (DatabaseChannel) Send(ctx context.Context, deps Deps, to Notifiable, n Notification) error {
	store, err := deps.storeFor()
	if err != nil {
		return err
	}
	data, err := n.ToDatabase()
	if err != nil {
		return err
	}
	if data == nil {
		data = map[string]any{}
	}
	_, err = store.Create(ctx, to, n.Type(), data)
	return err
}

var (
	_ Store   = (*GormStore)(nil)
	_ Channel = DatabaseChannel{}
)
