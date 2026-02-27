package broker

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type KafkaBroker struct {
	writer *kafka.Writer
	reader *kafka.Reader
	logger *zap.Logger

	brokers []string
	topic   string
	groupID string

	workerCount int
	msgChan     chan kafka.Message
	wg          sync.WaitGroup

	closeOnce sync.Once
	closed    atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc

	subscriptions   map[string]MessageHandler
	subscriptionsMu sync.RWMutex

	workerPoolStarted  atomic.Bool
	receiveLoopStarted atomic.Bool

	stats BrokerStats
}

func NewKafkaBroker(brokers []string, topic, groupID string, workerCount int, logger *zap.Logger) (*KafkaBroker, error) {
	if workerCount <= 0 {
		workerCount = 10
	}

	ctx, cancel := context.WithCancel(context.Background())

	broker := &KafkaBroker{
		brokers:       brokers,
		topic:         topic,
		groupID:       groupID,
		workerCount:   workerCount,
		msgChan:       make(chan kafka.Message, 1000),
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

func (k *KafkaBroker) Publish(ctx context.Context, topic string, message []byte) error {
	if k.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	if k.writer == nil {
		return fmt.Errorf("kafka writer is not initialized")
	}

	err := k.writer.WriteMessages(ctx, kafka.Message{
		Key:   []byte(topic),
		Value: message,
		Time:  time.Now(),
	})

	if err != nil {
		atomic.AddInt64(&k.stats.ErrorCount, 1)

		k.logger.Error("kafka publish failed",
			zap.String("topic", topic),
			zap.Error(err),
		)
		return fmt.Errorf("publish failed: %w", err)
	}

	atomic.AddInt64(&k.stats.PublishCount, 1)

	k.logger.Debug("message published",
		zap.String("topic", topic),
		zap.Int("size", len(message)),
	)

	return nil
}

func (k *KafkaBroker) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	if k.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	// 保存订阅信息
	k.subscriptionsMu.Lock()
	defer k.subscriptionsMu.Unlock()

	if _, exists := k.subscriptions[topic]; exists {
		k.logger.Warn("topic handler already registered",
			zap.String("topic", topic),
		)
		k.subscriptions[topic] = handler
		return nil
	}

	k.subscriptions[topic] = handler

	k.logger.Info("kafka topic handler registered",
		zap.String("topic", topic),
		zap.Int("total_handlers", len(k.subscriptions)),
	)

	// 第一次订阅：启动 worker 池和接收循环
	if len(k.subscriptions) == 1 {
		k.startWorkerPool()
		k.startReceiveLoop()
	}

	return nil
}

func (k *KafkaBroker) Close() error {
	var closeErr error

	k.closeOnce.Do(func() {
		if k.closed.Swap(true) {
			return
		}

		k.logger.Info("closing kafka broker")

		k.cancel()

		done := make(chan struct{})
		go func() {
			k.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			k.logger.Info("all kafka workers stopped")
		case <-time.After(30 * time.Second):
			k.logger.Warn("kafka force closing: timeout")
		}

		k.closeConnections()

		k.logger.Info("kafka broker closed",
			zap.Int64("processed", k.stats.ProcessedCount),
			zap.Int64("errors", k.stats.ErrorCount),
			zap.Int64("published", k.stats.PublishCount),
		)
	})
	return closeErr
}

func (k *KafkaBroker) GetStats() *BrokerStats {
	return &BrokerStats{
		ActiveWorkers:  atomic.LoadInt32(&k.stats.ActiveWorkers),
		ProcessedCount: atomic.LoadInt64(&k.stats.ProcessedCount),
		ErrorCount:     atomic.LoadInt64(&k.stats.ErrorCount),
		PublishCount:   atomic.LoadInt64(&k.stats.PublishCount),
	}
}

func (k *KafkaBroker) HealthCheck() error {
	if k.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	if k.reader == nil {
		return fmt.Errorf("reader is nil")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	conn, err := kafka.DialLeader(ctx, "tcp", k.brokers[0], k.topic, 0)
	if err != nil {
		k.closeConnections()
		return fmt.Errorf("kafka connection failed: %w", err)
	}
	_ = conn.Close()

	return err
}

// 建立连接
func (k *KafkaBroker) connect() error {
	k.logger.Info("connecting to kafka", zap.Strings("brokers", k.brokers))

	// 创建 writer
	k.writer = &kafka.Writer{
		Addr:         kafka.TCP(k.brokers...),
		Topic:        k.topic,
		Balancer:     &kafka.LeastBytes{},
		RequiredAcks: kafka.RequireOne,
		Compression:  kafka.Snappy,
		BatchSize:    100,
		BatchTimeout: 10 * time.Millisecond,
		MaxAttempts:  3,
		Async:        false,
	}

	// 创建 reader
	k.reader = kafka.NewReader(kafka.ReaderConfig{
		Brokers:        k.brokers,
		Topic:          k.topic,
		GroupID:        k.groupID,
		MinBytes:       10e3, // 10KB
		MaxBytes:       10e6, // 10MB
		CommitInterval: time.Second,
		StartOffset:    kafka.LastOffset,
		// 设置超时
		ReadBackoffMin: 100 * time.Millisecond,
		ReadBackoffMax: 1 * time.Second,
	})

	// 测试连接
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	// 尝试获取 topic metadata
	conn, err := kafka.DialLeader(ctx, "tcp", k.brokers[0], k.topic, 0)
	if err != nil {
		k.closeConnections()
		return fmt.Errorf("kafka connection failed: %w", err)
	}
	_ = conn.Close()

	k.logger.Info("connected to kafka")
	return nil
}

// 关闭连接
func (k *KafkaBroker) closeConnections() {
	if k.writer != nil {
		_ = k.writer.Close()
		k.writer = nil
	}
	if k.reader != nil {
		_ = k.reader.Close()
		k.reader = nil
	}
}

// 监控连接
func (k *KafkaBroker) monitorConnection() {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-k.ctx.Done():
			return
		case <-ticker.C:
			if k.closed.Load() {
				return
			}

			// 检查连接健康
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)

			// 尝试获取 stats（测试连接）
			conn, err := kafka.DialLeader(ctx, "tcp", k.brokers[0], k.topic, 0)
			cancel()

			if err != nil {
				k.logger.Error("kafka connection lost", zap.Error(err))
			}
			_ = conn.Close()
		}
	}
}

// 启动 worker 池
func (k *KafkaBroker) startWorkerPool() {
	// 防止重复启动
	if !k.workerPoolStarted.CompareAndSwap(false, true) {
		k.logger.Warn("worker pool already started")
		return
	}

	k.logger.Info("starting kafka worker pool",
		zap.Int("worker_count", k.workerCount),
	)

	for i := 0; i < k.workerCount; i++ {
		k.wg.Add(1)

		go func(workerID int) {
			defer k.wg.Done()

			atomic.AddInt32(&k.stats.ActiveWorkers, 1)
			defer atomic.AddInt32(&k.stats.ActiveWorkers, -1)

			k.logger.Info("kafka worker started", zap.Int("worker_id", workerID))

			for {
				select {
				case <-k.ctx.Done():
					k.logger.Info("kafka worker stopping", zap.Int("worker_id", workerID))
					return
				case msg, ok := <-k.msgChan:
					if !ok {
						k.logger.Info("kafka message channel closed", zap.Int("worker_id", workerID))
						return
					}

					k.handleMessage(workerID, msg)
				}
			}
		}(i)
	}
}

// 启动接收循环
func (k *KafkaBroker) startReceiveLoop() {
	// 防止重复启动
	if !k.receiveLoopStarted.CompareAndSwap(false, true) {
		k.logger.Warn("receive loop already started")
		return
	}

	k.logger.Info("starting kafka receive loop")

	go func() {
		defer k.receiveLoopStarted.Store(false)

		for {
			select {
			case <-k.ctx.Done():
				k.logger.Info("kafka receive loop stopping")
				close(k.msgChan)
				return

			default:
				if k.reader == nil {
					k.logger.Warn("kafka reader is nil, waiting...")
					time.Sleep(1 * time.Second)
					continue
				}

				msg, err := k.reader.ReadMessage(k.ctx)
				if err != nil {
					if errors.Is(err, context.Canceled) {
						k.logger.Error("read kafka message error", zap.Error(err))
						time.Sleep(1 * time.Second)
						continue
					}
				}

				// 分发到 worker 池
				select {
				case k.msgChan <- msg:
				case <-time.After(100 * time.Millisecond):
					k.logger.Warn("kafka message channel full, dropping message",
						zap.Int("partition", msg.Partition),
						zap.Int64("offset", msg.Offset),
					)
				}
			}
		}
	}()
}

// 处理消息
func (k *KafkaBroker) handleMessage(workerID int, msg kafka.Message) {
	start := time.Now()

	routingKey := string(msg.Key)

	k.subscriptionsMu.Lock()
	handler, exists := k.subscriptions[routingKey]
	k.subscriptionsMu.Unlock()

	if !exists {
		k.subscriptionsMu.RLock()
		// 如果没有找到特定的 handler，使用通配符 handler
		handler, exists = k.subscriptions["*"]
		k.subscriptionsMu.RUnlock()

		if !exists {
			k.logger.Debug("no handler for routing key",
				zap.Int("worker_id", workerID),
				zap.String("routing_key", routingKey),
			)
			return
		}
	}

	k.logger.Debug("kafka worker processing message",
		zap.Int("worker_id", workerID),
		zap.String("routing_key", routingKey),
		zap.Int("partition", msg.Partition),
		zap.Int64("offset", msg.Offset),
	)

	err := handler(msg.Value)

	duration := time.Since(start)

	if err != nil {
		atomic.AddInt64(&k.stats.ErrorCount, 1)
		k.logger.Error("kafka worker handler failed",
			zap.Int("worker_id", workerID),
			zap.String("routing_key", routingKey),
			zap.Error(err),
		)
		return
	}

	atomic.AddInt64(&k.stats.ProcessedCount, 1)

	k.logger.Debug("kafka worker message processed",
		zap.Int("worker_id", workerID),
		zap.String("routing_key", routingKey),
		zap.Duration("duration", duration),
	)
}
