package broker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
)

type RedisBroker struct {
	addr     string
	password string
	db       int

	client *redis.Client
	pubsub *redis.PubSub
	logger *zap.Logger

	// 消息分发通道
	workerCount int
	msgChan     chan *redis.Message
	wg          sync.WaitGroup

	// 关闭控制
	closeOnce sync.Once
	closed    atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc

	subscriptions      map[string]MessageHandler
	subscriptionsMu    sync.RWMutex
	subscribedTopics   []string
	receiveLoopStarted atomic.Bool

	stats BrokerStats
}

func NewRedisBroker(addr, password string, db int, workerCount int, logger *zap.Logger) (*RedisBroker, error) {
	if workerCount <= 0 {
		workerCount = 10
	}

	ctx, cancel := context.WithCancel(context.Background())

	broker := &RedisBroker{
		addr:          addr,
		password:      password,
		db:            db,
		logger:        logger,
		workerCount:   workerCount,
		msgChan:       make(chan *redis.Message, 1000),
		ctx:           ctx,
		cancel:        cancel,
		subscriptions: make(map[string]MessageHandler),
	}

	// 初始化连接
	if err := broker.connect(); err != nil {
		cancel()
		return nil, err
	}

	// 启动连接监控
	go broker.monitorConnection()

	return broker, nil
}

func (r *RedisBroker) Publish(ctx context.Context, topic string, message []byte) error {
	if r.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	if r.client == nil {
		return fmt.Errorf("redis client is nil")
	}

	err := r.client.Publish(ctx, topic, message).Err()
	if err != nil {
		atomic.AddInt64(&r.stats.ErrorCount, 1)
		r.logger.Error("redis publish message failed",
			zap.String("topic", topic),
			zap.Error(err),
		)

		return fmt.Errorf("publish failed: %w", err)
	}

	atomic.AddInt64(&r.stats.PublishCount, 1)

	r.logger.Debug("message published",
		zap.String("topic", topic),
		zap.Int("size", len(message)),
	)

	return nil
}

func (r *RedisBroker) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	if r.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	r.subscriptionsMu.Lock()
	defer r.subscriptionsMu.Unlock()

	// 检查是否订阅过该 topic
	if _, exists := r.subscriptions[topic]; exists {
		r.logger.Warn("topic already subscribed",
			zap.String("topic", topic),
		)
		// 更新 handler
		r.subscriptions[topic] = handler
		return nil
	}

	// 保存订阅信息
	r.subscriptions[topic] = handler
	r.subscribedTopics = append(r.subscribedTopics, topic)

	r.logger.Info("subscribing to redis topic",
		zap.String("topic", topic),
		zap.Int("total_topics", len(r.subscribedTopics)),
	)

	// 第一次订阅
	if r.pubsub == nil {
		r.pubsub = r.client.Subscribe(r.ctx, topic)

		_, err := r.pubsub.Receive(r.ctx)
		if err != nil {
			// 订阅失败回滚
			delete(r.subscriptions, topic)
			r.subscribedTopics = r.subscribedTopics[:len(r.subscribedTopics)-1]
			return fmt.Errorf("subscribe failed: %w", err)
		}

		// 启动 worker 池
		r.startWorkerPool()
		// 启动接收循环
		r.startReceiveLoop()
	} else {
		err := r.pubsub.Subscribe(r.ctx, topic)
		if err != nil {
			// 订阅失败回滚
			delete(r.subscriptions, topic)
			r.subscribedTopics = r.subscribedTopics[:len(r.subscribedTopics)-1]
			return fmt.Errorf("subscribe additional topic failed: %w", err)
		}
	}

	r.logger.Info("subscribed successfully",
		zap.String("topic", topic),
	)
	return nil
}

func (r *RedisBroker) Close() error {
	var closeErr error

	r.closeOnce.Do(func() {
		if r.closed.Load() {
			return
		}

		r.logger.Info("closing redis broker")

		r.cancel()

		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			r.logger.Info("all redis workers stopped")
		case <-time.After(30 * time.Second):
			r.logger.Warn("redis force closing: timeout")
		}

		if r.pubsub != nil {
			closeErr = r.pubsub.Close()
		}

		if r.client != nil {
			closeErr = r.client.Close()
		}

		r.logger.Info("redis broker closed",
			zap.Int64("processed", r.stats.ProcessedCount),
			zap.Int64("errors", r.stats.ErrorCount),
			zap.Int64("published", r.stats.PublishCount),
		)
	})

	return closeErr
}

func (r *RedisBroker) GetStats() *BrokerStats {
	return &BrokerStats{
		ActiveWorkers:  atomic.LoadInt32(&r.stats.ActiveWorkers),
		ProcessedCount: atomic.LoadInt64(&r.stats.ProcessedCount),
		ErrorCount:     atomic.LoadInt64(&r.stats.ErrorCount),
		PublishCount:   atomic.LoadInt64(&r.stats.PublishCount),
	}
}

// HealthCheck 健康检查
func (r *RedisBroker) HealthCheck() error {
	if r.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	if r.client == nil {
		return fmt.Errorf("client is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()

	return r.client.Ping(ctx).Err()
}

// 建立连接
func (r *RedisBroker) connect() error {
	r.logger.Info("connecting to redis", zap.String("addr", r.addr))

	r.client = redis.NewClient(&redis.Options{
		Addr:         r.addr,
		Password:     r.password,
		DB:           r.db,
		PoolSize:     10,
		MinIdleConns: 5,
		DialTimeout:  5 * time.Second,
		ReadTimeout:  3 * time.Second,
		WriteTimeout: 3 * time.Second,

		MaxRetries:      3,
		MinRetryBackoff: 100 * time.Millisecond,
		MaxRetryBackoff: 3 * time.Second,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := r.client.Ping(ctx).Err(); err != nil {
		r.logger.Error("failed to connect to redis", zap.Error(err))
		_ = r.client.Close()
		return fmt.Errorf("redis ping failed: %w", err)
	}

	r.logger.Info("connected to redis")
	return nil
}

func (r *RedisBroker) monitorConnection() {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.ctx.Done():
			return
		case <-ticker.C:
			if r.closed.Load() {
				return
			}

			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			err := r.client.Ping(ctx).Err()
			cancel()

			if err != nil {
				r.logger.Error("redis connection lost", zap.Error(err))
			}
		}

	}
}

// 启动 worker 池
func (r *RedisBroker) startWorkerPool() {
	for i := 0; i < r.workerCount; i++ {
		r.wg.Add(1)

		go func(workerID int) {
			defer r.wg.Done()

			atomic.AddInt32(&r.stats.ActiveWorkers, 1)
			defer atomic.AddInt32(&r.stats.ActiveWorkers, -1)

			r.logger.Info("redis worker started",
				zap.Int("worker_id", workerID),
			)

			for {
				select {
				case <-r.ctx.Done():
					r.logger.Info("redis worker stopping", zap.Int("worker_id", workerID))
					return
				case msg, ok := <-r.msgChan:
					if !ok {
						r.logger.Info("redis message channel closed", zap.Int("worker_id", workerID))
						return
					}

					r.handleMessage(workerID, msg)
				}
			}
		}(i)
	}
}

// 启动接收循环
func (r *RedisBroker) startReceiveLoop() {
	// 防止重复启动
	if !r.receiveLoopStarted.CompareAndSwap(false, true) {
		return
	}

	go func() {
		defer r.receiveLoopStarted.Store(false)

		r.logger.Info("starting redis receive loop")

		ch := r.pubsub.Channel()
		for {
			select {
			case <-r.ctx.Done():
				r.logger.Info("redis receive loop stopping")
				return
			case msg, ok := <-ch:
				if !ok {
					r.logger.Warn("redis pubsub channel closed")
					return
				}

				// 分发到 worker 池
				select {
				case r.msgChan <- msg:
				// 成功分发
				case <-time.After(100 * time.Millisecond):
					r.logger.Warn("redis message channel full, dropping message",
						zap.String("channel", msg.Channel),
					)
				}
			}
		}
	}()
}

// 处理消息（根据 topic 路由到对应 handler）
func (r *RedisBroker) handleMessage(workerID int, msg *redis.Message) {
	start := time.Now()

	// 根据 topic 获取 handler
	r.subscriptionsMu.Lock()
	handler, exists := r.subscriptions[msg.Channel]
	r.subscriptionsMu.Unlock()

	if !exists {
		r.logger.Warn("no handler found for topic",
			zap.Int("worker_id", workerID),
			zap.String("topic", msg.Channel),
		)
		return
	}

	r.logger.Debug("redis worker processing message",
		zap.Int("worker_id", workerID),
		zap.String("channel", msg.Channel),
	)

	err := handler([]byte(msg.Payload))

	duration := time.Since(start)

	if err != nil {
		atomic.AddInt64(&r.stats.ErrorCount, 1)
		r.logger.Error("redis worker handler failed",
			zap.Int("worker_id", workerID),
			zap.String("channel", msg.Channel),
			zap.Error(err),
		)
		return
	}

	atomic.AddInt64(&r.stats.ProcessedCount, 1)

	r.logger.Debug("redis worker message processed",
		zap.Int("worker_id", workerID),
		zap.String("channel", msg.Channel),
		zap.Duration("duration", duration),
	)
}
