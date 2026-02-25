package ws

import (
	"encoding/json"
	"errors"

	"github.com/qiuyier/medlink-ws/internal/consts"
	"go.uber.org/zap"
)

type MessageHandler interface {
	HandleMessage(conn *Connection, data []byte) error
}

type DefaultMessageHandler struct {
	manager *ConnectionManager
	logger  *zap.Logger
}

func NewDefaultMessageHandler(manager *ConnectionManager, logger *zap.Logger) *DefaultMessageHandler {
	return &DefaultMessageHandler{
		manager: manager,
		logger:  logger,
	}
}

func (h *DefaultMessageHandler) HandleMessage(conn *Connection, data []byte) error {
	var msg WSMessage
	if err := json.Unmarshal(data, &msg); err != nil {
		h.logger.Error("unmarshal message error", zap.Error(err))
		return h.sendError(conn, consts.ErrorCode, "invalid message format")
	}

	h.logger.Debug("received message",
		zap.String("user_id", conn.UserID),
		zap.String("type", msg.Type),
		zap.String("msg_id", msg.MsgID),
	)
	switch msg.Type {
	case MessageTypePing:
		return h.handlePing(conn)
	case MessageTypeSubscribe:
		return h.handleSubscribe(conn, &msg)
	case MessageTypeUnsubscribe:
		return h.handleUnsubscribe(conn, &msg)
	case MessageTypeAck:
		return h.handleAck(conn, &msg)
	default:
		return h.sendError(conn, consts.ErrorCode, "invalid message type")
	}
}

// 发送错误消息
func (h *DefaultMessageHandler) sendError(conn *Connection, code int, message string) error {
	errMsg, _ := NewWSMessage(MessageTypeError, &ErrorPayload{
		Code:    code,
		Message: message,
	})
	data, _ := json.Marshal(errMsg)
	conn.Send(data)
	return errors.New(message)
}

// 处理 Ping
func (h *DefaultMessageHandler) handlePing(conn *Connection) error {
	pongMsg, _ := NewWSMessage(MessageTypePong, nil)
	data, _ := json.Marshal(pongMsg)
	conn.Send(data)
	return nil
}

// 处理订阅
func (h *DefaultMessageHandler) handleSubscribe(conn *Connection, msg *WSMessage) error {
	var payload SubscribePayload
	if err := msg.ParsePayload(&payload); err != nil {
		return h.sendError(conn, consts.ErrorCode, "invalid subscribe payload")
	}

	// 验证主题权限
	for _, topic := range payload.Topics {
		if !h.validateTopicPermission(conn, topic) {
			return h.sendError(conn, consts.NotAuthorizedCode, "no permission for topic: "+topic)
		}
	}

	conn.Subscribe(payload.Topics)

	// 发送订阅成功响应
	successMsg, _ := NewWSMessage(consts.TopicSubscribeSuccess, map[string]any{
		"topics": payload.Topics,
	})
	data, _ := json.Marshal(successMsg)
	conn.Send(data)

	return nil
}

// 处理取消订阅
func (h *DefaultMessageHandler) handleUnsubscribe(conn *Connection, msg *WSMessage) error {
	var payload SubscribePayload
	if err := msg.ParsePayload(&payload); err != nil {
		return h.sendError(conn, consts.ErrorCode, "invalid unsubscribe payload")
	}

	conn.Unsubscribe(payload.Topics)

	// 发送取消订阅成功响应
	successMsg, _ := NewWSMessage(consts.TopicUnsubscribeSuccess, map[string]any{
		"topics": payload.Topics,
	})
	data, _ := json.Marshal(successMsg)
	conn.Send(data)

	return nil
}

// 处理 ACK
func (h *DefaultMessageHandler) handleAck(conn *Connection, msg *WSMessage) error {
	var payload AckPayload
	if err := msg.ParsePayload(&payload); err != nil {
		return h.sendError(conn, consts.ErrorCode, "invalid ack payload")
	}

	h.logger.Info("message acked",
		zap.String("user_id", conn.UserID),
		zap.String("msg_id", payload.MsgID),
	)

	// TODO: 更新数据库消息状态为已送达
	// 这里应该调用 service 层更新消息状态

	return nil
}

// 验证主题订阅权限
func (h *DefaultMessageHandler) validateTopicPermission(conn *Connection, topic string) bool {
	switch topic {
	case BizTypePrescriptionAudit:
		// 只有医生可以订阅处方审核
		return conn.Role == consts.DoctorRole
	case BizTypeChat:
		// 所有人都可以订阅聊天消息
		return true
	case BizTypeSystem:
		// 所有人都可以订阅系统消息
		return true
	case BizTypeOnlineStatus:
		// 只有医生可以订阅在线状态
		return conn.Role == consts.DoctorRole
	default:
		return false
	}
}
