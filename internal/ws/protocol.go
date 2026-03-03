package ws

import (
	"encoding/json"
	"fmt"
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

// 广播类型常量
const (
	BroadcastTypeAll        = "all"        // 广播给所有人
	BroadcastTypeDoctors    = "doctors"    // 广播给所有医生
	BroadcastTypeDepartment = "department" // 广播给指定科室
	BroadcastTypeUsers      = "users"      // 广播给指定用户
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

// BrokerMessage Broker 消息结构（从 MQ 接收的消息）
type BrokerMessage struct {
	// 基本信息
	MsgID    string `json:"msg_id"`   // 消息 ID
	BizType  string `json:"biz_type"` // 业务类型
	Priority int    `json:"priority"` // 优先级 (0-3)

	// 单播字段
	ToUserID string `json:"to_user_id,omitempty"` // 目标用户 ID（单播时必填）

	// 广播字段
	BroadcastType string   `json:"broadcast_type,omitempty"` // 广播类型：all, doctors, department, users
	DeptID        string   `json:"dept_id,omitempty"`        // 科室 ID（department 广播时必填）
	UserIDs       []string `json:"user_ids,omitempty"`       // 用户 ID 列表（users 广播时必填）

	// 消息内容
	Data json.RawMessage `json:"data"` // 实际业务数据

	// 元数据（可选）
	FromUserID string `json:"from_user_id,omitempty"` // 发送者 ID
	Timestamp  int64  `json:"timestamp,omitempty"`    // 时间戳
}

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

// IsUnicast 判断是否为单播消息
func (m *BrokerMessage) IsUnicast() bool {
	return m.ToUserID != "" && m.BroadcastType == ""
}

// IsBroadcast 判断是否为广播消息
func (m *BrokerMessage) IsBroadcast() bool {
	return m.BroadcastType != ""
}

// Validate 验证消息有效性
func (m *BrokerMessage) Validate() error {
	// 验证消息 ID
	if m.MsgID == "" {
		return fmt.Errorf("msg_id is required")
	}

	// 验证业务类型
	if m.BizType == "" {
		return fmt.Errorf("biz_type is required")
	}

	// 验证优先级
	if m.Priority < PriorityUrgent || m.Priority > PriorityLow {
		return fmt.Errorf("invalid priority: %d", m.Priority)
	}

	// 验证数据
	if len(m.Data) == 0 {
		return fmt.Errorf("data is required")
	}

	// 验证收件人
	if !m.IsUnicast() && !m.IsBroadcast() {
		return fmt.Errorf("either to_user_id or broadcast_type must be specified")
	}

	// 验证广播参数
	if m.IsBroadcast() {
		switch m.BroadcastType {
		case BroadcastTypeDepartment:
			if m.DeptID == "" {
				return fmt.Errorf("dept_id is required for department broadcast")
			}
		case BroadcastTypeUsers:
			if len(m.UserIDs) == 0 {
				return fmt.Errorf("user_ids is required for users broadcast")
			}
		case BroadcastTypeAll, BroadcastTypeDoctors:
			// 不需要额外参数
		default:
			return fmt.Errorf("invalid broadcast_type: %s", m.BroadcastType)
		}
	}

	return nil
}

// GetRecipientInfo 获取收件人信息（用于日志）
func (m *BrokerMessage) GetRecipientInfo() string {
	if m.IsUnicast() {
		return fmt.Sprintf("user:%s", m.ToUserID)
	}

	switch m.BroadcastType {
	case BroadcastTypeAll:
		return "broadcast:all"
	case BroadcastTypeDoctors:
		return "broadcast:doctors"
	case BroadcastTypeDepartment:
		return fmt.Sprintf("broadcast:dept:%s", m.DeptID)
	case BroadcastTypeUsers:
		return fmt.Sprintf("broadcast:users:%d", len(m.UserIDs))
	default:
		return "unknown"
	}
}
