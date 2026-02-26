package broker

import (
	"context"
	"fmt"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type RedisBroker struct {
	client *redis.Client
	pubsub *redis.PubSub
	logger *zap.Logger
}

func NewRedisBroker(addr, password string, db int, logger *zap.Logger) (*RedisBroker, error) {
	client := redis.NewClient(&redis.Options{
		Addr:     addr,
		Password: password,
		DB:       db,
	})

	// 测试连接
	ctx := context.Background()
	if err := client.Ping(ctx).Err(); err != nil {
		return nil, fmt.Errorf("redis connection failed: %w", err)
	}

	return &RedisBroker{
		client: client,
		logger: logger,
	}, nil
}

func (r *RedisBroker) Publish(ctx context.Context, topic string, message []byte) error {
	return r.client.Publish(ctx, topic, message).Err()
}

func (r *RedisBroker) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	r.logger.Info("subscribed to redis topic", zap.String("topic", topic))

	r.pubsub = r.client.Subscribe(ctx, topic)

	// 等待订阅确认
	_, err := r.pubsub.Receive(ctx)
	if err != nil {
		return err
	}

	// 启动消息接收协程
	go func() {
		ch := r.pubsub.Channel()
		for {
			select {
			case <-ctx.Done():
				r.logger.Info("subscription cancelled", zap.String("topic", topic))
				return
			case msg := <-ch:
				if err := handler([]byte(msg.Payload)); err != nil {
					r.logger.Error("handle message error",
						zap.String("topic", topic),
						zap.Error(err),
					)
				}
			}
		}
	}()

	return nil
}

func (r *RedisBroker) Close() error {
	if r.pubsub != nil {
		r.pubsub.Close()
	}

	return r.client.Close()
}
