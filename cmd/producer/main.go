package main

import (
	"math/rand"
	"time"

	"github.com/suyog1pathak/transporter/pkg/producer"

	"github.com/gookit/slog"
	"github.com/suyog1pathak/transporter/pkg/randomEvent"
)

func GetMeRandomValue(data []string) string {
	rand.Seed(time.Now().UnixNano())
	randomIndex := rand.Intn(len(data))
	randomValue := data[randomIndex]
	return randomValue

}

func main() {
	AGENT := []string{"agent-1", "agent-2", "agent-3", "agent-4"}
	//slog.SetFormatter(slog.NewJSONFormatter())
	slog.Info("Generating random events")

	for {
		eventByte, err := randomEvent.GenerateRandomEvent()
		if err != nil {
			slog.Panic(err)
		}
		agentName := GetMeRandomValue(AGENT)
		err = producer.PublishEvent(eventByte, agentName)
		if err != nil {
			slog.Panic(err)
		}
		time.Sleep(3 * time.Second)
	}

}
