package serverruntime

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
)

const runtimeInstanceIDSize = 18

func newRuntimeInstanceID() (string, error) {
	var raw [runtimeInstanceIDSize]byte
	if _, err := rand.Read(raw[:]); err != nil {
		return "", fmt.Errorf("create GOX server instance identity: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(raw[:]), nil
}
