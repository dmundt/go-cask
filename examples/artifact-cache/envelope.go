package main

import (
	"encoding/base64"
	"encoding/json"
	"fmt"

	"github.com/dmundt/go-cask/cas"
)

// envelope is the self-describing storage form (cas-core §8 decision 1):
// {"type": "<type>@<major>", "data": "<base64 payload>"}. The example
// implements it itself — the core keeps the format contract, apps copy the
// pattern (gitlike does the same).
type envelope struct {
	Type string `json:"type"`
	Data string `json:"data"`
}

func marshalEnvelope(typeName string, payload []byte) ([]byte, error) {
	return json.Marshal(envelope{Type: typeName, Data: base64.StdEncoding.EncodeToString(payload)})
}

// envelopePayload parses the envelope and returns the base64-decoded
// payload (the gzip-compressed codec output).
func envelopePayload(data []byte) ([]byte, error) {
	var env envelope
	if err := json.Unmarshal(data, &env); err != nil {
		return nil, fmt.Errorf("%w: not a valid object envelope", cas.ErrUnknownType)
	}
	if env.Type == "" {
		return nil, fmt.Errorf("%w: envelope missing type", cas.ErrUnknownType)
	}
	payload, err := base64.StdEncoding.DecodeString(env.Data)
	if err != nil {
		return nil, fmt.Errorf("%w: envelope data is not base64", cas.ErrUnknownType)
	}
	return payload, nil
}
