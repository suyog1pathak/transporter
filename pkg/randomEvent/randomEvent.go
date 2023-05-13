package randomEvent

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"
	"github.com/lucasepe/codename"
	"github.com/suyog1pathak/transporter/model"
)

func generateName() (string, error) {
	rng, err := codename.DefaultRNG()
	if err != nil {
		return "", err
	}
	name := codename.Generate(rng, 0)
	return name, nil
}

func GenerateRandomEvent() ([]byte, error) {

	eventName, err := generateName()
	if err != nil {
		return nil, err
	}

	event := model.Event{
		Name: eventName,
		Metadata: map[string]string{
			"managed_by": "transporter",
		},
		TimeStamp: time.Now(),
		UUID:      uuid.New(),
	}

	jsonEvent, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}

	return jsonEvent, err

}
