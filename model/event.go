package model

import (
	"time"

	"github.com/google/uuid"
)

type Event struct {
	Name      string
	Metadata  map[string]string
	TimeStamp time.Time
	UUID      uuid.UUID
}
