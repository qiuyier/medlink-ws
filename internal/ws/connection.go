package ws

import (
	"context"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

type Connection struct {
	// 基本信息
	UserID   string
	DeviceID string
	Role     string
	DeptID   string

	// WebSocket 连接
	conn *websocket.Conn

	// 通道
	sendChan  chan []byte
	closeChan chan struct{}

	// 状态
	lastActive atomic.Int64
	closed     atomic.Bool

	// 订阅的主题
	topics   map[string]bool
	topicsMu sync.RWMutex

	// 日志
	logger *zap.Logger

	// 配置
	maxMessageSize int64
	pongTimeout    time.Duration

	// 上下文
	ctx    context.Context
	cancel context.CancelFunc
}

func NewConnection(
	userID, deviceID, role, deptID string,
	conn *websocket.Conn,
	sendChanSize int,
	maxMessageSize int64,
	pongTimeout time.Duration,
	logger *zap.Logger,
) *Connection {
	ctx, cancel := context.WithCancel(context.Background())

	c := &Connection{
		UserID:         userID,
		DeviceID:       deviceID,
		Role:           role,
		DeptID:         deptID,
		conn:           conn,
		sendChan:       make(chan []byte, sendChanSize),
		closeChan:      make(chan struct{}),
		topics:         make(map[string]bool),
		maxMessageSize: maxMessageSize,
		pongTimeout:    pongTimeout,
		logger:         logger.With(zap.String("user_id", userID), zap.String("device_id", deviceID)),
		ctx:            ctx,
		cancel:         cancel,
	}

	c.UpdateLastActive()

	return c
}

// UpdateLastActive 更新最后活跃时间
func (c *Connection) UpdateLastActive() {
	c.lastActive.Store(time.Now().Unix())
}

// GetLastActive 获取最后活跃时间
func (c *Connection) GetLastActive() time.Time {
	return time.Unix(c.lastActive.Load(), 0)
}

// Subscribe 订阅主题
func (c *Connection) Subscribe(topics []string) {
	c.topicsMu.Lock()
	defer c.topicsMu.Unlock()

	for _, topic := range topics {
		c.topics[topic] = true
	}

	c.logger.Info("subscribed topics", zap.Strings("topics", topics))
}

// Unsubscribe 取消订阅
func (c *Connection) Unsubscribe(topics []string) {
	c.topicsMu.Lock()
	defer c.topicsMu.Unlock()

	for _, topic := range topics {
		delete(c.topics, topic)
	}

	c.logger.Info("unsubscribed topics", zap.Strings("topics", topics))
}

// IsSubscribed 检查是否订阅了主题
func (c *Connection) IsSubscribed(topic string) bool {
	c.topicsMu.RLock()
	defer c.topicsMu.RUnlock()

	return c.topics[topic]
}

// Send 发送消息（异步）
func (c *Connection) Send(data []byte) bool {
	if c.closed.Load() {
		return false
	}

	select {
	case c.sendChan <- data:
		return true
	case <-time.After(100 * time.Millisecond):
		c.logger.Warn("send channel full, message dropped")
		return false
	}
}

// Close 关闭连接
func (c *Connection) Close() {
	if c.closed.CompareAndSwap(false, true) {
		c.cancel()
		close(c.closeChan)

		// 等待 sendChan 清空（最多等待 1 秒）
		timeout := time.After(1 * time.Second)
		for {
			select {
			case <-timeout:
				goto cleanup
			default:
				if len(c.sendChan) == 0 {
					goto cleanup
				}
				time.Sleep(10 * time.Millisecond)
			}
		}

	cleanup:
		close(c.sendChan)
		_ = c.conn.Close()
		c.logger.Info("connection closed")
	}
}

// ReadPump 读取消息循环
func (c *Connection) ReadPump(handler MessageHandler) {
	defer c.Close()

	c.conn.SetReadLimit(c.maxMessageSize)
	_ = c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout))

	c.conn.SetPongHandler(func(string) error {
		c.UpdateLastActive()
		_ = c.conn.SetReadDeadline(time.Now().Add(c.pongTimeout))
		return nil
	})

	for {
		select {
		case <-c.ctx.Done():
			return
		default:
			_, message, err := c.conn.ReadMessage()
			if err != nil {
				if websocket.IsUnexpectedCloseError(err, websocket.CloseGoingAway, websocket.CloseAbnormalClosure) {
					c.logger.Error("read error", zap.Error(err))
				}
				return
			}

			c.UpdateLastActive()

			// 处理信息
			if err := handler.HandleMessage(c, message); err != nil {
				c.logger.Error("handle message error", zap.Error(err))
			}
		}
	}
}

// WritePump 写入消息循环
func (c *Connection) WritePump(pingInterval time.Duration) {
	ticker := time.NewTicker(pingInterval)
	defer func() {
		ticker.Stop()
		c.Close()
	}()

	for {
		select {
		case <-c.ctx.Done():
			return
		case message, ok := <-c.sendChan:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if !ok {
				_ = c.conn.WriteMessage(websocket.CloseMessage, []byte{})
				return
			}

			if err := c.conn.WriteMessage(websocket.TextMessage, message); err != nil {
				c.logger.Error("write error", zap.Error(err))
				return
			}

		case <-ticker.C:
			_ = c.conn.SetWriteDeadline(time.Now().Add(10 * time.Second))
			if err := c.conn.WriteMessage(websocket.PingMessage, nil); err != nil {
				c.logger.Error("ping error", zap.Error(err))
				return
			}
		}
	}
}
