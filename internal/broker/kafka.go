package broker

import (
	"context"
	"errors"

	"github.com/segmentio/kafka-go"
	"go.uber.org/zap"
)

type KafkaBroker struct {
	writer *kafka.Writer
	reader *kafka.Reader
	logger *zap.Logger
}

func NewKafkaBroker(brokers []string, topic, groupID string, logger *zap.Logger) (*KafkaBroker, error) {
	writer := &kafka.Writer{
		Addr:     kafka.TCP(brokers...),
		Topic:    topic,
		Balancer: &kafka.LeastBytes{},
	}

	reader := kafka.NewReader(kafka.ReaderConfig{
		Brokers:  brokers,
		Topic:    topic,
		GroupID:  groupID,
		MinBytes: 10e3, // 10KB
		MaxBytes: 10e6, // 10MB
	})

	return &KafkaBroker{
		writer: writer,
		reader: reader,
		logger: logger,
	}, nil
}

func (k *KafkaBroker) Publish(ctx context.Context, topic string, message []byte) error {
	return k.writer.WriteMessages(ctx, kafka.Message{
		Value: message,
	})
}

func (k *KafkaBroker) Subscribe(ctx context.Context, topic string, handler MessageHandler) error {
	k.logger.Info("subscribed to kafka topic", zap.String("topic", topic))

	go func() {
		for {
			select {
			case <-ctx.Done():
				k.logger.Info("kafka subscription cancelled", zap.String("topic", topic))
				return
			default:
				msg, err := k.reader.ReadMessage(ctx)
				if err != nil {
					if !errors.Is(err, context.Canceled) {
						k.logger.Error("read kafka message error", zap.Error(err))
					}
					continue
				}

				if err := handler(msg.Value); err != nil {
					k.logger.Error("handle kafka message error",
						zap.String("topic", topic),
						zap.Error(err),
					)
				}
			}
		}
	}()

	return nil
}

func (k *KafkaBroker) Close() error {
	if err := k.reader.Close(); err != nil {
		return err
	}

	return k.reader.Close()
}
