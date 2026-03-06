package service

import (
	"context"
	"time"

	"github.com/google/uuid"
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

// SaveMessage 保存消息
func (s *MessageService) SaveMessage(ctx context.Context, msg *model.Message) error {
	// 生成消息ID
	if msg.MsgID == "" {
		msg.MsgID = uuid.New().String()
	}

	msg.Status = model.MessageStatusPending

	if err := s.db.WithContext(ctx).Create(msg).Error; err != nil {
		s.logger.Error("save message error", zap.Error(err))
		return err
	}

	return nil
}

// BatchSaveMessages 批量保存消息
func (s *MessageService) BatchSaveMessages(ctx context.Context, msg []*model.Message) error {
	if len(msg) == 0 {
		return nil
	}

	if err := s.db.WithContext(ctx).CreateInBatches(msg, 100).Error; err != nil {
		s.logger.Error("batch save messages error", zap.Error(err))
		return err
	}

	return nil
}

// MarkAsDelivered 更新消息状态为已送达
func (s *MessageService) MarkAsDelivered(ctx context.Context, msgID string) error {
	now := time.Now()

	result := s.db.WithContext(ctx).Model(&model.Message{}).
		Where("msg_id = ? AND status = ?", msgID, model.MessageStatusPending).
		Updates(map[string]any{
			"status":       model.MessageStatusDelivered,
			"delivered_at": now,
		})

	if result.Error != nil {
		s.logger.Error("mark as delivered error", zap.Error(result.Error))
		return result.Error
	}

	return nil
}

// BatchMarkAsDelivered 批量更新消息状态为已送达
func (s *MessageService) BatchMarkAsDelivered(ctx context.Context, msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}

	now := time.Now()

	result := s.db.WithContext(ctx).Model(&model.Message{}).
		Where("msg_id IN ? AND status = ?", msgIDs, model.MessageStatusPending).
		Updates(map[string]any{
			"status":       model.MessageStatusDelivered,
			"delivered_at": now,
		})

	if result.Error != nil {
		s.logger.Error("batch mark as delivered error", zap.Error(result.Error))
		return result.Error
	}

	s.logger.Info("batch marked as delivered", zap.Int("count", int(result.RowsAffected)))

	return nil
}

// MarkAsRead 更新消息状态为已读
func (s *MessageService) MarkAsRead(ctx context.Context, msgID string) error {
	now := time.Now()

	result := s.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("msg_id in ? AND status IN ?", msgID, []string{
			model.MessageStatusPending,
			model.MessageStatusDelivered,
		}).
		Updates(map[string]any{
			"status":  model.MessageStatusRead,
			"read_at": now,
		})

	if result.Error != nil {
		s.logger.Error("mark as read error", zap.Error(result.Error))
		return result.Error
	}

	return nil
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

// GetOfflineMessage 获取离线消息
func (s *MessageService) GetOfflineMessage(ctx context.Context, userID string, limit int) ([]*model.Message, error) {
	var messages []*model.Message

	err := s.db.WithContext(ctx).
		Table("messages m").
		Select("m.*").
		Joins("INNER JOIN offline_messages om ON m.msg_id = om.msg_id").
		Where("om.user_id = ?", userID).
		Order("m.created_at DESC").
		Limit(limit).
		Find(&messages).Error
	if err != nil {
		s.logger.Error("get offline message error", zap.Error(err))
		return nil, err
	}
	return messages, nil
}

// DeleteOfflineMessages 删除离线消息
func (s *MessageService) DeleteOfflineMessages(ctx context.Context, userID string, msgIDs []string) error {
	if len(msgIDs) == 0 {
		return nil
	}

	result := s.db.WithContext(ctx).
		Where("user_id = ? AND msg_id IN ?", userID, msgIDs).
		Delete(&model.OfflineMessage{})

	if result.Error != nil {
		s.logger.Error("delete offline messages error", zap.Error(result.Error))
		return result.Error
	}

	s.logger.Info("deleted offline messages",
		zap.String("user_id", userID),
		zap.Int("count", int(result.RowsAffected)),
	)

	return nil
}

// RecordSession 记录 WebSocket 会话
func (s *MessageService) RecordSession(ctx context.Context, session *model.WSSession) error {
	if err := s.db.WithContext(ctx).Create(session).Error; err != nil {
		s.logger.Error("record session error", zap.Error(err))
		return err
	}

	return nil
}

// UpdateSessionDisconnect 更新会话断开信息
func (s *MessageService) UpdateSessionDisconnect(ctx context.Context, sessionID, reason string) error {
	now := time.Now()

	result := s.db.WithContext(ctx).
		Model(&model.WSSession{}).
		Where("session_id = ?", sessionID).
		Updates(map[string]any{
			"disconnect_time":   now,
			"disconnect_reason": reason,
		})

	if result.Error != nil {
		s.logger.Error("update session disconnect error", zap.Error(result.Error))
		return result.Error
	}

	return nil
}

// GetPendingMessages 获取未送达的消息（定时任务用）
func (s *MessageService) GetPendingMessages(ctx context.Context, olderThan time.Duration, limit int) ([]*model.Message, error) {
	var messages []*model.Message

	cutoffTime := time.Now().Add(-olderThan)

	err := s.db.WithContext(ctx).
		Where("status = ? AND created_at < ?", model.MessageStatusPending, cutoffTime).
		Order("priority ASC, created_at ASC").
		Limit(limit).
		Find(&messages).Error

	if err != nil {
		s.logger.Error("get pending messages error", zap.Error(err))
		return nil, err
	}

	return messages, nil
}

// CountMessagesByBizType 根据业务类型统计消息数量
func (s *MessageService) CountMessagesByBizType(ctx context.Context, startTime, endTime time.Time) (map[string]int64, error) {
	var results []struct {
		BizType string
		Count   int64
	}

	err := s.db.WithContext(ctx).
		Model(&model.Message{}).
		Select("biz_type, COUNT(*) as count").
		Where("created_at BETWEEN ? AND ?", startTime, endTime).
		Group("biz_type").
		Find(&results).Error

	if err != nil {
		s.logger.Error("count messages by biz type error", zap.Error(err))
		return nil, err
	}

	counts := make(map[string]int64)
	for _, r := range results {
		counts[r.BizType] = r.Count
	}

	return counts, nil
}

// GetUserMessageHistory 获取用户的消息历史（分页）
func (s *MessageService) GetUserMessageHistory(ctx context.Context, userID string, page, pageSize int) ([]*model.Message, int64, error) {
	var messages []*model.Message
	var total int64

	// 计算总数
	if err := s.db.WithContext(ctx).
		Model(&model.Message{}).
		Where("to_user_id = ?", userID).
		Count(&total).Error; err != nil {
		return nil, 0, err
	}

	// 分页查询
	offset := (page - 1) * pageSize
	err := s.db.WithContext(ctx).
		Where("to_user_id = ?", userID).
		Order("created_at DESC").
		Offset(offset).
		Limit(pageSize).
		Find(&messages).Error

	if err != nil {
		s.logger.Error("get user message history error", zap.Error(err))
		return nil, 0, err
	}

	return messages, total, nil
}

// CleanOldMessages 清理历史数据
func (s *MessageService) CleanOldMessages(ctx context.Context, retentionDays int) (int64, error) {
	cutoffTime := time.Now().AddDate(0, 0, -retentionDays)

	// 删除已读的旧消息
	result := s.db.WithContext(ctx).
		Where("status = ? AND read_at < ?", model.MessageStatusRead, cutoffTime).
		Delete(&model.Message{})

	if result.Error != nil {
		s.logger.Error("clean old messages error", zap.Error(result.Error))
		return 0, result.Error
	}

	s.logger.Info("cleaned old messages", zap.Int64("count", result.RowsAffected))
	return result.RowsAffected, nil
}

// CleanOldSessions 清理旧会话记录
func (s *MessageService) CleanOldSessions(ctx context.Context, retentionDays int) (int64, error) {
	cutoffTime := time.Now().AddDate(0, 0, -retentionDays)

	result := s.db.WithContext(ctx).
		Where("disconnect_time IS NOT NULL AND disconnect_time < ?", cutoffTime).
		Delete(&model.WSSession{})

	if result.Error != nil {
		s.logger.Error("clean old sessions error", zap.Error(result.Error))
		return 0, result.Error
	}

	s.logger.Info("cleaned old sessions", zap.Int64("count", result.RowsAffected))
	return result.RowsAffected, nil
}
