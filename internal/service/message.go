package service

import (
	"context"
	"time"

	"github.com/qiuyier/medlink-ws/internal/model"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"
)

type MessageService struct {
	db     *gorm.DB
	logger *zap.Logger
}

func NewMessageService(db *gorm.DB, logger *zap.Logger) *MessageService {
	return &MessageService{
		db:     db,
		logger: logger,
	}
}

// SaveOfflineMessage 保存离线消息
func (s *MessageService) SaveOfflineMessage(ctx context.Context, userID, msgID string) error {
	offlineMsg := &model.OfflineMessage{
		UserID:    userID,
		MsgID:     msgID,
		CreatedAt: time.Now(),
	}

	// 使用 Clauses 处理冲突（ON CONFLICT DO NOTHING）
	err := s.db.WithContext(ctx).
		Clauses(clause.OnConflict{DoNothing: true}).
		Create(offlineMsg).Error

	if err != nil {
		s.logger.Error("save offline message error", zap.Error(err))
		return err
	}

	return nil
}
