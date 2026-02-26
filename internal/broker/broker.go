package broker

import "context"

// MessageBroker 消息代理接口
type MessageBroker interface {
	// Publish 发布消息
	Publish(ctx context.Context, topic string, message []byte) error

	// Subscribe 订阅消息
	Subscribe(ctx context.Context, topic string, handler MessageHandler) error

	// Close 关闭连接
	Close() error

	// GetStats 获取统计信息
	GetStats() *BrokerStats
}

// MessageHandler 消息处理函数
type MessageHandler func(message []byte) error

// Message 消息结构
type Message struct {
	Topic     string
	Key       string
	Value     []byte
	Timestamp int64
}
