package telegram

import (
	"crypto/rand"
	"encoding/hex"
)

// newTaskCorrelationID is the short handle operators and the feed share.
func newTaskCorrelationID() string {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return hex.EncodeToString([]byte("fallback"))[:8]
	}
	return hex.EncodeToString(b[:])
}