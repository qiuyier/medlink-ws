package ws

import (
	"encoding/json"
	"time"
)

// 消息类型
const (
	MessageTypePing        = "ping"
	MessageTypePong        = "pong"
	MessageTypeAuth        = "auth"
	MessageTypeAuthSuccess = "auth_success"
	MessageTypeAuthFailed  = "auth_failed"
	MessageTypeSubscribe   = "subscribe"
	MessageTypeUnsubscribe = "unsubscribe"
	MessageTypeMessage     = "message"
	MessageTypeAck         = "ack"
	MessageTypeError       = "error"
	MessageTypeKickout     = "kickout"
)

// 业务消息类型
const (
	BizTypePrescriptionAudit = "prescription_audit"
	BizTypeChat              = "chat"
	BizTypeSystem            = "system"
	BizTypeOnlineStatus      = "online_status"
)

// 优先级
const (
	PriorityUrgent = 0 // 紧急（处方审核不通过）
	PriorityHigh   = 1 // 高（新问诊消息）
	PriorityNormal = 2 // 普通（处方审核通过）
	PriorityLow    = 3 // 低（系统通知）
)

// WSMessage WebSocket 消息协议
type WSMessage struct {
	Type      string          `json:"type"`
	MsgID     string          `json:"msg_id,omitempty"`
	Timestamp int64           `json:"timestamp"`
	Payload   json.RawMessage `json:"payload,omitempty"`
}

// AuthPayload 认证载荷
type AuthPayload struct {
	Token string `json:"token"`
}

// AuthSuccessPayload 认证成功响应
type AuthSuccessPayload struct {
	UserID   string `json:"user_id"`
	Role     string `json:"role"`
	DeviceID string `json:"device_id"`
}

// SubscribePayload 订阅载荷
type SubscribePayload struct {
	Topics []string `json:"topics"`
}

// MessagePayload 业务消息载荷
type MessagePayload struct {
	BizType  string          `json:"biz_type"`
	Priority int             `json:"priority"`
	Data     json.RawMessage `json:"data"`

	// 可选字段
	FromUserID string `json:"from_user_id,omitempty"`
	ToUserID   string `json:"to_user_id,omitempty"`
}

// AckPayload ACK 载荷
type AckPayload struct {
	MsgID string `json:"msg_id"`
}

// ErrorPayload 错误载荷
type ErrorPayload struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// SendMessageRequest HTTP 发送消息请求
type SendMessageRequest struct {
	ToUserID string          `json:"to_user_id" binding:"required"`
	BizType  string          `json:"biz_type" binding:"required"`
	Priority int             `json:"priority"`
	Data     json.RawMessage `json:"data" binding:"required"`
}

// SendMessageResponse HTTP 发送消息响应
type SendMessageResponse struct {
	MsgID     string `json:"msg_id"`
	Status    string `json:"status"` // pending, delivered, offline
	Timestamp int64  `json:"timestamp"`
}

// NewWSMessage 构造 WebSocket 消息
func NewWSMessage(msgType string, payload any) (*WSMessage, error) {
	data, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}

	return &WSMessage{
		Type:      msgType,
		Timestamp: time.Now().Unix(),
		Payload:   data,
	}, nil
}

// ParsePayload 解析载荷
func (m *WSMessage) ParsePayload(v any) error {
	return json.Unmarshal(m.Payload, v)
}
