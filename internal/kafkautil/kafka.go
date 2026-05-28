// Package kafkautil provides Kafka producer and consumer utilities
// for streaming energy meter readings through Kafka topics.
package kafkautil

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/segmentio/kafka-go"
	"github.com/yliana-efimova/energy-collector/internal/source"
)

// ProducerConfig holds the Kafka producer configuration.
type ProducerConfig struct {
	Brokers []string
	Topic   string
}

// Producer writes energy readings to a Kafka topic.
type Producer struct {
	cfg    ProducerConfig
	writer *kafka.Writer
}

// NewProducer creates a new Kafka producer.
func NewProducer(cfg ProducerConfig) *Producer {
	w := &kafka.Writer{
		Addr:         kafka.TCP(cfg.Brokers...),
		Topic:        cfg.Topic,
		Balancer:     &kafka.Hash{}, // same meter_id -> same partition
		BatchTimeout: 100 * time.Millisecond,
		BatchSize:    100,
		Async:        false,
	}
	return &Producer{cfg: cfg, writer: w}
}

// SendReading sends a single reading to Kafka.
func (p *Producer) SendReading(ctx context.Context, reading source.Reading) error {
	data, err := json.Marshal(reading)
	if err != nil {
		return fmt.Errorf("failed to marshal reading: %w", err)
	}

	msg := kafka.Message{
		Key:   []byte(reading.MeterID),
		Value: data,
	}

	return p.writer.WriteMessages(ctx, msg)
}

// SendReadings sends multiple readings to Kafka in a batch.
func (p *Producer) SendReadings(ctx context.Context, readings []source.Reading) error {
	if len(readings) == 0 {
		return nil
	}

	messages := make([]kafka.Message, len(readings))
	for i, r := range readings {
		data, err := json.Marshal(r)
		if err != nil {
			return fmt.Errorf("failed to marshal reading %s: %w", r.MeterID, err)
		}
		messages[i] = kafka.Message{
			Key:   []byte(r.MeterID),
			Value: data,
		}
	}

	return p.writer.WriteMessages(ctx, messages...)
}

// Close closes the Kafka producer.
func (p *Producer) Close() error {
	if p.writer != nil {
		return p.writer.Close()
	}
	return nil
}

// ConsumerConfig holds the Kafka consumer configuration.
type ConsumerConfig struct {
	Brokers []string
	Topic   string
	GroupID string
}

// Consumer reads energy readings from a Kafka topic.
type Consumer struct {
	cfg    ConsumerConfig
	reader *kafka.Reader
}

// NewConsumer creates a new Kafka consumer.
func NewConsumer(cfg ConsumerConfig) *Consumer {
	r := kafka.NewReader(kafka.ReaderConfig{
		Brokers:        cfg.Brokers,
		Topic:          cfg.Topic,
		GroupID:        cfg.GroupID,
		MinBytes:       10,
		MaxBytes:       10e6, // 10MB
		MaxWait:        1 * time.Second,
		CommitInterval: 1 * time.Second,
		StartOffset:    kafka.LastOffset,
	})
	return &Consumer{cfg: cfg, reader: r}
}

// ReadReading reads a single reading from Kafka. Blocks until a message is available.
func (c *Consumer) ReadReading(ctx context.Context) (*source.Reading, error) {
	msg, err := c.reader.ReadMessage(ctx)
	if err != nil {
		return nil, err
	}

	var reading source.Reading
	if err := json.Unmarshal(msg.Value, &reading); err != nil {
		log.Printf("[kafka-consumer] failed to unmarshal message: %v", err)
		return nil, err
	}

	return &reading, nil
}

// Close closes the Kafka consumer.
func (c *Consumer) Close() error {
	if c.reader != nil {
		return c.reader.Close()
	}
	return nil
}