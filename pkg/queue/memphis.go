package queue

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/memphisdev/memphis.go"
	"github.com/suyog1pathak/transporter/model"
)

// MemphisQueue wraps Memphis client for event queuing
type MemphisQueue struct {
	conn     *memphis.Conn
	station  *memphis.Station
	producer *memphis.Producer
	consumer *memphis.Consumer
}

// Config holds Memphis configuration
type Config struct {
	Host       string
	Username   string
	Password   string
	StationName string
	AccountID  int
}

// NewMemphisQueue creates a new Memphis queue client
func NewMemphisQueue(config Config) (*MemphisQueue, error) {
	// Connect to Memphis
	conn, err := memphis.Connect(
		config.Host,
		config.Username,
		memphis.Password(config.Password),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Memphis: %w", err)
	}

	// Create or get station
	station, err := conn.CreateStation(config.StationName)
	if err != nil {
		return nil, fmt.Errorf("failed to create station: %w", err)
	}

	return &MemphisQueue{
		conn:    conn,
		station: station,
	}, nil
}

// Close closes the Memphis connection
func (mq *MemphisQueue) Close() error {
	if mq.producer != nil {
		mq.producer.Destroy()
	}
	if mq.consumer != nil {
		mq.consumer.Destroy()
	}
	if mq.conn != nil {
		mq.conn.Close()
	}
	return nil
}

// ProduceEvent publishes an event to the queue
func (mq *MemphisQueue) ProduceEvent(event *model.Event) error {
	// Create producer if not exists
	if mq.producer == nil {
		producer, err := mq.station.CreateProducer("transporter-cp")
		if err != nil {
			return fmt.Errorf("failed to create producer: %w", err)
		}
		mq.producer = producer
	}

	// Serialize event to JSON
	data, err := json.Marshal(event)
	if err != nil {
		return fmt.Errorf("failed to marshal event: %w", err)
	}

	// Produce message
	if err := mq.producer.Produce(
		data,
		memphis.MsgId(event.ID), // Use event ID for idempotency
	); err != nil {
		return fmt.Errorf("failed to produce event: %w", err)
	}

	return nil
}

// ConsumeEvents starts consuming events from the queue
func (mq *MemphisQueue) ConsumeEvents(consumerName string, handler func(*model.Event) error) error {
	// Create consumer
	consumer, err := mq.station.CreateConsumer(consumerName)
	if err != nil {
		return fmt.Errorf("failed to create consumer: %w", err)
	}

	mq.consumer = consumer

	// Start consuming
	consumer.Consume(func(msgs []*memphis.Msg, err error, ctx context.Context) {
		if err != nil {
			fmt.Printf("Error consuming messages: %v\n", err)
			return
		}

		for _, msg := range msgs {
			// Deserialize event
			var event model.Event
			if err := json.Unmarshal(msg.Data(), &event); err != nil {
				fmt.Printf("Failed to unmarshal event: %v\n", err)
				msg.Ack() // Ack anyway to avoid reprocessing bad message
				continue
			}

			// Call handler
			if err := handler(&event); err != nil {
				fmt.Printf("Error handling event %s: %v\n", event.ID, err)
				// Don't ack on handler error - message will be redelivered
				continue
			}

			// Ack message
			if err := msg.Ack(); err != nil {
				fmt.Printf("Failed to ack message: %v\n", err)
			}
		}
	})

	return nil
}

// GetQueueDepth returns the approximate number of messages in the queue
func (mq *MemphisQueue) GetQueueDepth() (int, error) {
	// Note: Memphis doesn't provide a direct API for queue depth
	// This is a placeholder - you may need to implement this differently
	// based on Memphis monitoring capabilities
	return 0, fmt.Errorf("not implemented")
}

// EventQueueMessage represents a message in the queue with metadata
type EventQueueMessage struct {
	Event       *model.Event
	EnqueuedAt  time.Time
	Attempts    int
	LastAttempt *time.Time
}

// ProduceEventBatch produces multiple events in a batch
func (mq *MemphisQueue) ProduceEventBatch(events []*model.Event) error {
	// Create producer if not exists
	if mq.producer == nil {
		producer, err := mq.station.CreateProducer("transporter-cp")
		if err != nil {
			return fmt.Errorf("failed to create producer: %w", err)
		}
		mq.producer = producer
	}

	// Produce each event
	for _, event := range events {
		if err := mq.ProduceEvent(event); err != nil {
			return fmt.Errorf("failed to produce event %s: %w", event.ID, err)
		}
	}

	return nil
}

// StopConsuming stops the consumer
func (mq *MemphisQueue) StopConsuming() error {
	if mq.consumer != nil {
		mq.consumer.StopConsume()
		mq.consumer.Destroy()
		mq.consumer = nil
	}
	return nil
}

// GetStationInfo returns information about the Memphis station
func (mq *MemphisQueue) GetStationInfo() (map[string]interface{}, error) {
	// This is a placeholder - implement based on Memphis API capabilities
	info := map[string]interface{}{
		"station_name": mq.station.Name,
		"status":       "active",
	}
	return info, nil
}
