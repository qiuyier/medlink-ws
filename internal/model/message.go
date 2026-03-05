// internal/model/message.go

package model

import (
	"database/sql/driver"
	"encoding/json"
	"fmt"
	"time"

	"gorm.io/gorm"
)

// ============================================
// JSONB 类型
// ============================================

type JSONB map[string]any

func (j JSONB) Value() (driver.Value, error) {
	if j == nil {
		return nil, nil
	}
	return json.Marshal(j)
}

func (j *JSONB) Scan(value any) error {
	if value == nil {
		*j = nil
		return nil
	}

	var bytes []byte
	switch v := value.(type) {
	case []byte:
		bytes = v
	case string:
		bytes = []byte(v)
	default:
		return fmt.Errorf("failed to unmarshal JSONB value: %v", value)
	}

	var result map[string]any
	if err := json.Unmarshal(bytes, &result); err != nil {
		return err
	}

	*j = JSONB(result)
	return nil
}

func (j JSONB) Get(key string) (any, bool) {
	if j == nil {
		return nil, false
	}
	val, ok := j[key]
	return val, ok
}

func (j JSONB) GetString(key string) string {
	if v, ok := j[key]; ok {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

func (j *JSONB) Set(key string, value any) {
	if *j == nil {
		*j = make(JSONB)
	}
	(*j)[key] = value
}

// ============================================
// Message 模型
// ============================================

const (
	MessageStatusPending   = "pending"
	MessageStatusDelivered = "delivered"
	MessageStatusRead      = "read"
	MessageStatusFailed    = "failed"
)

type Message struct {
	ID          uint      `gorm:"primaryKey"`
	MsgID       string    `gorm:"uniqueIndex;type:varchar(64);not null"`
	FromUserID  string    `gorm:"index;type:varchar(64)"`
	ToUserID    string    `gorm:"index;type:varchar(64);not null"`
	BizType     string    `gorm:"index;type:varchar(32);not null"`
	Priority    int       `gorm:"default:2"`
	Content     JSONB     `gorm:"type:jsonb"`
	Status      string    `gorm:"type:varchar(16);default:'pending'"`
	CreatedAt   time.Time `gorm:"index"`
	DeliveredAt *time.Time
	ReadAt      *time.Time
	DeletedAt   gorm.DeletedAt `gorm:"index"`
}

func (Message) TableName() string {
	return "messages"
}

func (m *Message) BeforeCreate(tx *gorm.DB) error {
	if m.Status == "" {
		m.Status = MessageStatusPending
	}
	if m.Priority < 0 || m.Priority > 3 {
		m.Priority = 2
	}
	return nil
}

func (m Message) IsDelivered() bool {
	return m.Status == MessageStatusDelivered || m.Status == MessageStatusRead
}

func (m Message) IsRead() bool {
	return m.Status == MessageStatusRead
}

func (m *Message) MarkAsDelivered() {
	if m.Status == MessageStatusPending {
		m.Status = MessageStatusDelivered
		now := time.Now()
		m.DeliveredAt = &now
	}
}

func (m *Message) MarkAsRead() {
	if m.Status != MessageStatusRead {
		m.Status = MessageStatusRead
		now := time.Now()
		m.ReadAt = &now
	}
}

// ============================================
// OfflineMessage 模型
// ============================================

type OfflineMessage struct {
	ID        uint      `gorm:"primaryKey"`
	UserID    string    `gorm:"index;type:varchar(64);not null"`
	MsgID     string    `gorm:"index;type:varchar(64);not null"`
	CreatedAt time.Time `gorm:"index"`
}

func (OfflineMessage) TableName() string {
	return "offline_messages"
}

// ============================================
// WSSession 模型
// ============================================

type WSSession struct {
	ID             uint      `gorm:"primaryKey"`
	UserID         string    `gorm:"index;type:varchar(64);not null"`
	DeviceID       string    `gorm:"index;type:varchar(64);not null"`
	Role           string    `gorm:"type:varchar(16)"`
	DeptID         string    `gorm:"index;type:varchar(64)"`
	ConnectionID   string    `gorm:"uniqueIndex;type:varchar(128);not null"`
	ConnectedAt    time.Time `gorm:"index"`
	DisconnectedAt *time.Time
	IPAddress      string `gorm:"type:varchar(45)"`
}

func (WSSession) TableName() string {
	return "ws_sessions"
}

func (s WSSession) IsActive() bool {
	return s.DisconnectedAt == nil
}

func (s *WSSession) Disconnect() {
	if s.DisconnectedAt == nil {
		now := time.Now()
		s.DisconnectedAt = &now
	}
}

func (s WSSession) Duration() time.Duration {
	if s.DisconnectedAt == nil {
		return time.Since(s.ConnectedAt)
	}
	return s.DisconnectedAt.Sub(s.ConnectedAt)
}
