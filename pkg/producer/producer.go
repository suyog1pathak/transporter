package producer

import (
	"encoding/json"
	"os"

	"github.com/gookit/slog"

	"github.com/memphisdev/memphis.go"
)

var conn *memphis.Conn
var p *memphis.Producer
var err error
var producerName string

func init() {
	slog.Info("Creating connection with memphis")
	conn, err = memphis.Connect(os.Getenv("MEMPHIS_HOST"), os.Getenv("MEMPHIS_USER"), memphis.Password(os.Getenv("MEMPHIS_PASS")))
	if err != nil {
		slog.Panic(err)
		return
	}
	producerName, _ = os.Hostname()
	p, err = conn.CreateProducer(os.Getenv("MEMPHIS_STATION"), producerName)
	if err != nil {
		slog.Panic(err)
		return
	}
}

func PublishEvent(event []byte, agentName string) error {
	headers := memphis.Headers{}
	headers.New()
	err = headers.Add("agent", agentName)
	if err != nil {
		return err
	}

	jheader, _ := json.Marshal(headers.MsgHeaders)

	slog.Info("publishing Event --->", string(event), jheader)
	err = p.Produce(event, memphis.MsgHeaders(headers))
	if err != nil {
		return err
	}
	return err
}

//app f4c11pb31
