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

func GenerateRandomEvent(agentName string) ([]byte, error) {

	eventName, err := generateName()
	if err != nil {
		return nil, err
	}

	// Create a simple namespace manifest as payload
	namespace := "test-" + eventName
	manifest := `apiVersion: v1
kind: Namespace
metadata:
  name: ` + namespace + `
  labels:
    created-by: transporter-random-event`

	event := model.Event{
		ID:          uuid.New().String(),
		Type:        model.EventTypeK8sResource,
		TargetAgent: agentName,
		Payload: model.EventPayload{
			Manifests: []string{manifest},
		},
		CreatedAt: time.Now(),
		CreatedBy: "random-event-generator",
		TTL:       24 * time.Hour,
		Priority:  0,
		Labels: map[string]string{
			"managed_by": "transporter",
			"event_name": eventName,
		},
	}

	jsonEvent, err := json.Marshal(event)
	if err != nil {
		return nil, err
	}

	return jsonEvent, err

}
