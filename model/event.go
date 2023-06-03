package model

import (
	"time"

	"github.com/google/uuid"
)

type Event struct {
	AgentName string
	Name      string
	Metadata  map[string]string
	TimeStamp time.Time
	UUID      uuid.UUID
}
