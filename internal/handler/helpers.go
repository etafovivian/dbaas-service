package handler

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"math/big"
)

// osReply is the standard natsutil response envelope from openstack-adapter.
type osReply struct {
	OK    bool            `json:"ok"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// mustMarshal marshals v to JSON. It panics on error (programmer bug).
func mustMarshal(v any) []byte {
	data, err := json.Marshal(v)
	if err != nil {
		panic(fmt.Sprintf("handler: marshal: %v", err))
	}
	return data
}

// parseOSReply unmarshals an openstack-adapter response and decodes the data field.
func parseOSReply(raw []byte, dest any) error {
	var r osReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("unmarshal openstack reply: %w", err)
	}
	if !r.OK {
		return fmt.Errorf("openstack-adapter: %s", r.Error)
	}
	if dest != nil {
		if err := json.Unmarshal(r.Data, dest); err != nil {
			return fmt.Errorf("unmarshal openstack data: %w", err)
		}
	}
	return nil
}

// checkOSReply validates that the openstack-adapter returned ok: true.
func checkOSReply(raw []byte) error {
	var r osReply
	if err := json.Unmarshal(raw, &r); err != nil {
		return fmt.Errorf("unmarshal openstack reply: %w", err)
	}
	if !r.OK {
		return fmt.Errorf("openstack-adapter: %s", r.Error)
	}
	return nil
}

// generatePassword creates a cryptographically random alphanumeric password.
func generatePassword(length int) (string, error) {
	const chars = "abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"
	b := make([]byte, length)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(chars))))
		if err != nil {
			return "", fmt.Errorf("generate password: %w", err)
		}
		b[i] = chars[n.Int64()]
	}
	return string(b), nil
}
