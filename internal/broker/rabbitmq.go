package broker

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
	"time"

	amqp "github.com/rabbitmq/amqp091-go"
	"go.uber.org/zap"
)

type RabbitMQBroker struct {
	conn     *amqp.Connection
	channels []*amqp.Channel
	exchange string
	queue    string
	logger   *zap.Logger
	// 并发控制
	workerCount int
	wg          sync.WaitGroup
	// 专门用于发布的 channel（与消费分离）
	publishChannel *amqp.Channel
	// 发布 channel 的锁
	publishMu sync.Mutex
	// 关闭控制
	closeOnce sync.Once
	closed    atomic.Bool
	ctx       context.Context
	cancel    context.CancelFunc

	subscriptionsMu  sync.Mutex
	subscriptions    map[string]MessageHandler
	subscribedTopics []string

	// 统计信息
	stats BrokerStats
}

func NewRabbitMQBroker(url, exchange, queue string, workerCount int, logger *zap.Logger) (*RabbitMQBroker, error) {
	if workerCount <= 0 {
		// 设置默认 10 个并发 worker
		workerCount = 10
	}

	conn, err := amqp.Dial(url)
	if err != nil {
		return nil, fmt.Errorf("rabbitmq connection failed: %w", err)
	}

	// 创建发布专用 channel
	publishChannel, err := conn.Channel()
	if err != nil {
		_ = conn.Close()
		return nil, fmt.Errorf("create publish channel failed: %w", err)
	}

	// 设置发布确认模式
	err = publishChannel.Confirm(false)
	if err != nil {
		_ = publishChannel.Close()
		_ = conn.Close()
		return nil, fmt.Errorf("set publish confirm failed: %w", err)
	}

	// 创建多个 channel（每个 worker 一个）
	channels := make([]*amqp.Channel, workerCount)
	for i := 0; i < workerCount; i++ {
		ch, err := conn.Channel()
		if err != nil {
			for j := 0; j < i; j++ {
				_ = channels[j].Close()
			}
			_ = conn.Close()
			return nil, fmt.Errorf("create channel %d failed: %w", i, err)
		}

		// 每个 channel 设置 QoS prefetch = 1: 每个 worker 一次处理 1 条
		err = ch.Qos(1, 0, false)
		if err != nil {
			for j := 0; j < i; j++ {
				_ = channels[j].Close()
			}
			_ = conn.Close()
			return nil, fmt.Errorf("set qos for channel %d failed: %w", i, err)
		}

		channels[i] = ch
	}

	// 声明交换机（Exchange和 Queue 是全局资源，只需在发布 channel 上操作 declare 即可）
	err = publishChannel.ExchangeDeclare(
		exchange,
		"topic",
		true,
		false,
		false,
		false,
		nil,
	)
	if err != nil {
		for _, ch := range channels {
			_ = ch.Close()
		}
		_ = conn.Close()
		return nil, fmt.Errorf("declare exchange failed: %w", err)
	}

	// 声明队列
	_, err = publishChannel.QueueDeclare(
		queue,
		true,
		false,
		false,
		false,
		amqp.Table{
			"x-dead-letter-exchange": "dlx.exchange",
			"x-message-ttl":          300000, // 5分钟
			"x-max-length":           100000, // 队列最大长度
		},
	)
	if err != nil {
		for _, ch := range channels {
			_ = ch.Close()
		}
		_ = conn.Close()
		return nil, fmt.Errorf("declare queue failed: %w", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	broker := &RabbitMQBroker{
		conn:           conn,
		channels:       channels,
		exchange:       exchange,
		queue:          queue,
		logger:         logger,
		publishChannel: publishChannel,
		ctx:            ctx,
		cancel:         cancel,
	}

	// 监听连接断开
	go broker.handleConnectionErrors()

	return broker, nil
}

func (r *RabbitMQBroker) Publish(ctx context.Context, topic string, message []byte) error {
	if r.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	r.publishMu.Lock()
	defer r.publishMu.Unlock()

	// 发布消息
	err := r.publishChannel.PublishWithContext(
		ctx,
		r.exchange,
		topic,
		false,
		false,
		amqp.Publishing{
			ContentType:  "application/json",
			Body:         message,
			DeliveryMode: amqp.Persistent,
			Timestamp:    time.Now(),
			MessageId:    generateMessageID(),
		},
	)

	if err != nil {
		atomic.AddInt64(&r.stats.ErrorCount, 1)

		r.logger.Error("publish message failed",
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

func (r *RabbitMQBroker) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	if r.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	r.subscriptionsMu.Lock()
	defer r.subscriptionsMu.Unlock()

	if _, exists := r.subscriptions[topic]; exists {
		r.logger.Warn("topic already subscribed",
			zap.String("topic", topic),
		)
		r.subscriptions[topic] = handler
		return nil
	}

	r.subscriptions[topic] = handler
	r.subscribedTopics = append(r.subscribedTopics, topic)

	r.logger.Info("subscribing to rabbitmq topic",
		zap.String("topic", topic),
		zap.Int("total_topics", len(r.subscribedTopics)),
	)

	// 绑定队列
	if r.channels[0] == nil {
		return fmt.Errorf("channel is nil")
	}

	err := r.channels[0].QueueBind(
		r.queue,
		topic,
		r.exchange,
		false,
		nil,
	)
	if err != nil {
		// 回滚
		delete(r.subscriptions, topic)
		r.subscribedTopics = r.subscribedTopics[:len(r.subscribedTopics)-1]
		return fmt.Errorf("queue bind failed: %w", err)
	}

	// 首次订阅,启动 worker
	if len(r.subscriptions) == 1 {
		r.startWorkerPool()
	}

	return nil
}

func (r *RabbitMQBroker) Close() error {
	var closeErr error

	r.closeOnce.Do(func() {
		if r.closed.Swap(true) {
			// 已经关闭过了
			return
		}

		r.logger.Info("closing rabbitmq broker")

		// 1. 取消 context（停止所有 worker）
		r.cancel()

		// 2. 等待所有 worker 完成（最多等待 30 秒）
		done := make(chan struct{})
		go func() {
			r.wg.Wait()
			close(done)
		}()

		select {
		case <-done:
			r.logger.Info("all workers stopped gracefully")
		case <-time.After(30 * time.Second):
			r.logger.Warn("force closing: workers timeout")
		}

		// 3. 关闭发布 channel
		if r.publishChannel != nil {
			if err := r.publishChannel.Close(); err != nil {
				r.logger.Error("close publish channel failed", zap.Error(err))
				closeErr = err
			}
		}

		// 4. 关闭所有消费 channel
		for i, ch := range r.channels {
			if ch != nil {
				if err := ch.Close(); err != nil {
					r.logger.Error("close channel failed",
						zap.Int("channel_id", i),
						zap.Error(err),
					)
					if closeErr == nil {
						closeErr = err
					}
				}
			}
		}

		// 5. 关闭连接
		if r.conn != nil {
			if err := r.conn.Close(); err != nil {
				r.logger.Error("close connection failed", zap.Error(err))
				if closeErr == nil {
					closeErr = err
				}
			}
		}

		r.logger.Info("rabbitmq broker closed",
			zap.Int64("processed", r.stats.ProcessedCount),
			zap.Int64("errors", r.stats.ErrorCount),
			zap.Int64("published", r.stats.PublishCount),
		)
	})

	return closeErr
}

// ACK逻辑的消息处理
func (r *RabbitMQBroker) handleMessageWithAck(workerID int, msg amqp.Delivery) {
	start := time.Now()

	routingKey := msg.RoutingKey

	r.subscriptionsMu.Lock()
	handler, exists := r.subscriptions[routingKey]
	r.subscriptionsMu.Unlock()

	if !exists {
		r.logger.Warn("no handler for routing key",
			zap.Int("worker_id", workerID),
			zap.String("routing_key", routingKey),
		)
		// ACK避免消息堆积
		_ = msg.Ack(false)
		return
	}

	// 记录消息接收
	r.logger.Debug("worker processing message",
		zap.Int("worker_id", workerID),
		zap.Uint64("delivery_tag", msg.DeliveryTag),
		zap.String("routing_key", msg.RoutingKey),
	)

	// 处理消息
	err := handler(msg.Body)

	duration := time.Since(start)

	if err != nil {
		atomic.AddInt64(&r.stats.ErrorCount, 1)

		// 处理失败
		r.logger.Error("worker handler failed",
			zap.Int("worker_id", workerID),
			zap.Uint64("delivery_tag", msg.DeliveryTag),
			zap.Duration("duration", duration),
			zap.Error(err),
		)

		// 获取重试次数
		retryCount := getRetryCount(msg.Headers)
		const maxRetries = 3

		if retryCount < maxRetries {
			// 超过最大重试次数，拒绝消息（进入死信队列）
			r.logger.Warn("max retries exceeded, rejecting message",
				zap.Int("worker_id", workerID),
				zap.Uint64("delivery_tag", msg.DeliveryTag),
				zap.Int("retry_count", retryCount),
			)

			_ = msg.Reject(false)
			return
		}

		// Nack 并重新入队
		r.logger.Info("requeuing message",
			zap.Int("worker_id", workerID),
			zap.Uint64("delivery_tag", msg.DeliveryTag),
			zap.Int("retry_count", retryCount),
		)

		_ = msg.Nack(false, true)
		return
	}

	// 处理成功，发送 ACK
	if err := msg.Ack(false); err != nil {
		r.logger.Error("ack failed",
			zap.Int("worker_id", workerID),
			zap.Uint64("delivery_tag", msg.DeliveryTag),
			zap.Error(err),
		)
		return
	}

	atomic.AddInt64(&r.stats.ProcessedCount, 1)

	r.logger.Debug("worker message processed",
		zap.Int("worker_id", workerID),
		zap.Uint64("delivery_tag", msg.DeliveryTag),
		zap.Duration("duration", duration),
	)
}

// GetStats 获取统计信息
func (r *RabbitMQBroker) GetStats() *BrokerStats {
	return &BrokerStats{
		ActiveWorkers:  atomic.LoadInt32(&r.stats.ActiveWorkers),
		ProcessedCount: atomic.LoadInt64(&r.stats.ProcessedCount),
		ErrorCount:     atomic.LoadInt64(&r.stats.ErrorCount),
		PublishCount:   atomic.LoadInt64(&r.stats.PublishCount),
	}
}

func (r *RabbitMQBroker) HealthCheck() error {
	if r.closed.Load() {
		return fmt.Errorf("broker is closed")
	}

	if r.conn == nil || r.conn.IsClosed() {
		return fmt.Errorf("connection is closed")
	}

	if r.publishChannel == nil {
		return fmt.Errorf("publish channel is nil")
	}

	// 尝试声明一个临时队列（测试连接）
	_, err := r.publishChannel.QueueDeclare(
		"",    // 随机名称
		false, // durable
		true,  // delete when unused
		true,  // exclusive
		false, // no-wait
		nil,
	)

	return err
}

// 监听连接错误（自动重连，可选）
func (r *RabbitMQBroker) handleConnectionErrors() {
	// 监听连接关闭事件
	errChan := make(chan *amqp.Error)
	r.conn.NotifyClose(errChan)

	for {
		select {
		case <-r.ctx.Done():
			return

		case err, ok := <-errChan:
			if !ok {
				return
			}

			r.logger.Error("rabbitmq connection error",
				zap.Error(err),
				zap.Int("code", err.Code),
				zap.String("reason", err.Reason),
			)
		}
	}
}

// 启动 worker 池
func (r *RabbitMQBroker) startWorkerPool() {
	r.logger.Info("starting rabbitmq workers",
		zap.Int("worker_count", r.workerCount),
	)

	for i := 0; i < r.workerCount; i++ {
		if r.channels[i] == nil {
			r.logger.Error("channel is nil", zap.Int("worker_id", i))
			continue
		}

		r.wg.Add(1)

		go func(workerID int, ch *amqp.Channel) {
			defer r.wg.Done()

			atomic.AddInt32(&r.stats.ActiveWorkers, 1)
			defer atomic.AddInt32(&r.stats.ActiveWorkers, -1)

			r.logger.Info("rabbitmq worker started", zap.Int("worker_id", workerID))

			msgs, err := ch.Consume(
				r.queue, // queue
				fmt.Sprintf("worker-%d-%d", time.Now().Unix(), workerID),
				false,
				false,
				false,
				false,
				nil,
			)
			if err != nil {
				r.logger.Error("worker consume failed",
					zap.Int("worker_id", workerID),
					zap.Error(err),
				)
				return
			}

			for {
				select {
				case <-r.ctx.Done():
					r.logger.Info("worker stopping", zap.Int("worker_id", workerID))
					return
				case msg, ok := <-msgs:
					if !ok {
						r.logger.Warn("worker channel closed", zap.Int("worker_id", workerID))
						return
					}
					r.handleMessageWithAck(workerID, msg)
				}
			}
		}(i, r.channels[i])
	}
}

// 生成消息 ID
func generateMessageID() string {
	return fmt.Sprintf("msg-%d-%d", time.Now().UnixNano(), randomInt())
}

// 获取重试次数
func getRetryCount(headers amqp.Table) int {
	if headers == nil {
		return 0
	}

	if count, ok := headers["x-retry-count"].(int32); ok {
		return int(count)
	}

	return 0
}

// 生成随机数
func randomInt() int64 {
	return time.Now().UnixNano() % 100000
}
