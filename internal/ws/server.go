package ws

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"time"

	"github.com/gorilla/websocket"
	"github.com/qiuyier/medlink-ws/internal/auth"
	"github.com/qiuyier/medlink-ws/internal/broker"
	"github.com/qiuyier/medlink-ws/internal/service"
	"go.uber.org/zap"
)

var upgrader = websocket.Upgrader{
	CheckOrigin: func(r *http.Request) bool {
		return true
	},
}

type Server struct {
	manager    *ConnectionManager
	handler    MessageHandler
	msgService *service.MessageService
	jwtAuth    *auth.JWTAuth
	broker     broker.MessageBroker
	logger     *zap.Logger

	readBufferSize   int
	writeBufferSize  int
	handshakeTimeout time.Duration
	pingInterval     time.Duration
	pongTimeout      time.Duration
	maxMessageSize   int64
	sendChannelSize  int

	ctx    context.Context
	cancel context.CancelFunc
}

func NewServer(
	jwt *auth.JWTAuth,
	broker broker.MessageBroker,
	msgService *service.MessageService,
	maxConnPerUser int,
	readBufferSize int,
	writeBufferSize int,
	handshakeTimeout time.Duration,
	pingInterval time.Duration,
	pongTimeout time.Duration,
	maxMessageSize int64,
	sendChannelSize int,
	logger *zap.Logger,
) *Server {
	ctx, cancel := context.WithCancel(context.Background())

	manager := NewConnectionManager(maxConnPerUser, logger)
	handler := NewDefaultMessageHandler(manager, logger)

	upgrader.ReadBufferSize = readBufferSize
	upgrader.WriteBufferSize = writeBufferSize
	upgrader.HandshakeTimeout = handshakeTimeout

	s := &Server{
		manager:          manager,
		handler:          handler,
		jwtAuth:          jwt,
		broker:           broker,
		msgService:       msgService,
		logger:           logger,
		readBufferSize:   readBufferSize,
		writeBufferSize:  writeBufferSize,
		handshakeTimeout: handshakeTimeout,
		pingInterval:     pingInterval,
		pongTimeout:      pongTimeout,
		maxMessageSize:   maxMessageSize,
		sendChannelSize:  sendChannelSize,
		ctx:              ctx,
		cancel:           cancel,
	}

	// 订阅消息队列
	s.subscribeBroker()

	return s
}

func (s *Server) subscribeBroker() {
	// 订阅主题
	topics := map[string]broker.MessageHandler{
		BizTypePrescriptionAudit: s.handlePrescriptionAuditMessage,
		BizTypeChat:              s.handleChatMessage,
		BizTypeOnlineStatus:      s.handleOnlineStatusMessage,
		BizTypeSystem:            s.handleSystemMessage,
	}

	for topic, handler := range topics {
		err := s.broker.Subscribe(s.ctx, topic, handler)

		if err != nil {
			s.logger.Error("subscribe topic failed",
				zap.String("topic", topic),
				zap.Error(err),
			)
		} else {
			s.logger.Info("subscribed to topic successfully",
				zap.String("topic", topic),
			)
		}
	}
}

// 处方审核消息 handler
func (s *Server) handlePrescriptionAuditMessage(data []byte) error {
	s.logger.Debug("received prescription audit message",
		zap.Int("size", len(data)),
	)

	return s.handleBrokerMessage(data, BizTypePrescriptionAudit)
}

// 聊天消息 handler
func (s *Server) handleChatMessage(data []byte) error {
	s.logger.Debug("received chat message",
		zap.Int("size", len(data)),
	)

	return s.handleBrokerMessage(data, BizTypeChat)
}

// 在线状态消息 handler
func (s *Server) handleOnlineStatusMessage(data []byte) error {
	s.logger.Debug("received online status message",
		zap.Int("size", len(data)),
	)

	return s.handleBrokerMessage(data, BizTypeOnlineStatus)
}

// 系统消息 handler
func (s *Server) handleSystemMessage(data []byte) error {
	s.logger.Debug("received system message",
		zap.Int("size", len(data)),
	)

	return s.handleBrokerMessage(data, BizTypeSystem)
}

// 处理来自消息队列的消息
func (s *Server) handleBrokerMessage(data []byte, expectedBizType string) error {
	var msg BrokerMessage

	if err := json.Unmarshal(data, &msg); err != nil {
		s.logger.Error("unmarshal broker message error",
			zap.Error(err),
			zap.String("data", string(data)),
		)
		return err
	}

	// 验证消息
	if err := msg.Validate(); err != nil {
		s.logger.Error("invalid broker message",
			zap.Error(err),
			zap.String("msg_id", msg.MsgID),
		)
		return err
	}

	// 验证 biz_type 是否匹配
	if msg.BizType != expectedBizType {
		s.logger.Warn("biz_type mismatch",
			zap.String("expected", expectedBizType),
			zap.String("actual", msg.BizType),
			zap.String("msg_id", msg.MsgID),
		)
		return fmt.Errorf("biz_type mismatch")
	}

	s.logger.Debug("processing broker message",
		zap.String("msg_id", msg.MsgID),
		zap.String("biz_type", msg.BizType),
		zap.String("recipient", msg.GetRecipientInfo()),
	)

	// 构建 websocket 消息
	wsMsg, err := NewWSMessage(MessageTypeMessage, &MessagePayload{
		BizType:  msg.BizType,
		Priority: msg.Priority,
		Data:     msg.Data,
	})
	if err != nil {
		s.logger.Error("create ws message error",
			zap.String("msg_id", msg.MsgID),
			zap.Error(err),
		)
		return err
	}

	wsMsg.MsgID = msg.MsgID
	wsData, err := json.Marshal(wsMsg)
	if err != nil {
		s.logger.Error("marshal ws message error",
			zap.String("msg_id", msg.MsgID),
			zap.Error(err),
		)
		return err
	}

	return s.routeMessage(&msg, wsData)
}

func (s *Server) routeMessage(msg *BrokerMessage, data []byte) error {
	// 单播消息
	if msg.IsUnicast() {
		return s.handleUnicastMessage(msg.ToUserID, data, msg.MsgID)
	}

	// 广播消息
	if msg.IsBroadcast() {
		return s.handleBroadcastMessage(msg, data)
	}

	return fmt.Errorf("invalid message routing")
}

// 处理单播消息
func (s *Server) handleUnicastMessage(toUserID string, wsData []byte, msgID string) error {
	err := s.manager.SendToUser(toUserID, wsData)

	if err != nil {
		if errors.Is(err, ErrUserOffline) {
			s.logger.Info("user offline, saving as offline message",
				zap.String("user_id", toUserID),
				zap.String("msg_id", msgID),
			)

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()

			if saveErr := s.msgService.SaveOfflineMessage(ctx, toUserID, msgID); saveErr != nil {
				s.logger.Error("save offline message error",
					zap.String("msg_id", msgID),
					zap.Error(saveErr),
				)
			}

			return nil
		}

		s.logger.Error("send to user failed",
			zap.String("user_id", toUserID),
			zap.String("msg_id", msgID),
			zap.Error(err),
		)
		return err
	}

	s.logger.Info("message delivered",
		zap.String("user_id", toUserID),
		zap.String("msg_id", msgID),
	)

	return nil
}

// 处理广播消息（使用 BrokerMessage）
func (s *Server) handleBroadcastMessage(msg *BrokerMessage, wsData []byte) error {
	var sentCount int

	switch msg.BroadcastType {
	case BroadcastTypeAll:
		s.manager.Broadcast(wsData, nil)
		sentCount = int(s.manager.GetStats()["total"])

	case BroadcastTypeDoctors:
		s.manager.BroadcastToDoctors(wsData)
		sentCount = int(s.manager.GetStats()["doctor"])

	case BroadcastTypeDepartment:
		sentCount = s.manager.BroadcastToDepartment(msg.DeptID, wsData)

	case BroadcastTypeUsers:
		sentCount = s.manager.BroadcastToUsers(msg.UserIDs, wsData)

	default:
		return fmt.Errorf("unknown broadcast type: %s", msg.BroadcastType)
	}

	s.logger.Info("broadcast message sent",
		zap.String("broadcast_type", msg.BroadcastType),
		zap.String("dept_id", msg.DeptID),
		zap.Int("user_count", len(msg.UserIDs)),
		zap.Int("sent_count", sentCount),
		zap.String("msg_id", msg.MsgID),
	)

	return nil
}

func (s *Server) HandleWebSocket(w http.ResponseWriter, r *http.Request) {
	token := r.URL.Query().Get("token")
	if token == "" {
		token = r.Header.Get("Authorization")
	}

	if token == "" {
		http.Error(w, "missing token", http.StatusUnauthorized)
		return
	}

	claims, err := s.jwtAuth.ValidateToken(token)
	if err != nil {
		http.Error(w, "invalid token", http.StatusUnauthorized)
		return
	}

	conn, err := upgrader.Upgrade(w, r, nil)
	if err != nil {
		s.logger.Error("upgrade error", zap.Error(err))
		return
	}

	wsConn := NewConnection(
		claims.UserID,
		claims.DeviceID,
		claims.Role,
		claims.DeptID,
		conn,
		s.sendChannelSize,
		s.maxMessageSize,
		s.pongTimeout,
		s.logger,
	)

	if err := s.manager.AddConnection(wsConn); err != nil {
		s.logger.Error("add connection error", zap.Error(err))

		errMsg, _ := NewWSMessage(MessageTypeError, &ErrorPayload{
			Code:    1002,
			Message: err.Error(),
		})
		data, _ := json.Marshal(errMsg)
		_ = conn.WriteMessage(websocket.TextMessage, data)
		_ = conn.Close()
		return
	}

	AuthSuccessMsg, _ := NewWSMessage(MessageTypeAuthSuccess, &AuthSuccessPayload{
		UserID:   claims.UserID,
		Role:     claims.Role,
		DeviceID: claims.DeviceID,
	})
	data, _ := json.Marshal(AuthSuccessMsg)
	wsConn.Send(data)

	s.logger.Info("websocket connected",
		zap.String("user_id", claims.UserID),
		zap.String("device_id", claims.DeviceID),
		zap.String("role", claims.Role),
	)

	// 启动读写协程
	go wsConn.WritePump(s.pingInterval)
	go wsConn.ReadPump(s.handler)

	// 连接断开后清理
	go func() {
		<-wsConn.ctx.Done()
		s.manager.RemoveConnection(wsConn.UserID, wsConn.DeviceID)
	}()
}

// GetStats 获取统计信息
func (s *Server) GetStats() map[string]int64 {
	return s.manager.GetStats()
}

// Shutdown 关闭服务器
func (s *Server) Shutdown() error {
	s.logger.Info("shutting down websocket server")

	s.cancel()

	time.Sleep(500 * time.Millisecond)

	// 关闭消息代理
	if err := s.broker.Close(); err != nil {
		s.logger.Error("close broker error", zap.Error(err))
	}

	// 关闭所有连接
	s.manager.Broadcast(nil, func(conn *Connection) bool {
		conn.Close()
		return false
	})

	return nil
}
