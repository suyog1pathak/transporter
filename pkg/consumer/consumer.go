package consumer

import (
	"context"
	"encoding/json"
	"os"
	"sync"

	"github.com/gookit/slog"

	"github.com/memphisdev/memphis.go"
	"github.com/suyog1pathak/transporter/model"
)

var wg sync.WaitGroup

func CreateConsumer(agentName string) {
	conn, err := memphis.Connect(os.Getenv("MEMPHIS_HOST"), os.Getenv("MEMPHIS_USER"), memphis.Password(os.Getenv("MEMPHIS_PASS")))
	if err != nil {
		slog.Panic(err)
	}
	defer conn.Close()
	consumerName, _ := os.Hostname()
	consumer, err := conn.CreateConsumer(os.Getenv("MEMPHIS_STATION"), consumerName, memphis.BatchSize(5))
	if err != nil {
		slog.Panic(err)
	}

	handler := func(msgs []*memphis.Msg, err error, ctx context.Context) {
		for _, msg := range msgs {
			//fmt.Println(string(msg.Data()))
			//msg.Ack()
			headers := msg.GetHeaders()
			if headers["agent"] == agentName {
				event := msg.Data()
				data := model.Event{}
				err = json.Unmarshal(event, &data)
				if err != nil {
					slog.Error("unable to unmarshal event to struct")
				}
				jevent, err := json.Marshal(&data)
				if err != nil {
					slog.Error("unable to convert event struct to json")
				}
				msg.Ack()
				slog.Info("Event consumed", jevent)
			}
		}
	}
	wg.Add(1)
	ctx := context.Background()
	consumer.SetContext(ctx)
	consumer.Consume(handler)
	wg.Wait()

}
