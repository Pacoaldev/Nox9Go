package telegram

import (
	"errors"
	"fmt"
	"net"
	"syscall"
	"testing"
)

func TestIsTransientTelegramPollError_getUpdatesWSA(t *testing.T) {
	under := &net.OpError{Op: "read", Err: syscall.ECONNABORTED}
	wrapped := fmt.Errorf("error get updates, %w", under)
	if !isTransientTelegramPollError(wrapped) {
		t.Fatalf("WSAECONNABORTED during getUpdates should be transient")
	}
}

func TestIsTransientTelegramPollError_tokenRejectedNotTransient(t *testing.T) {
	err := errors.New("telegram: token rejected: 401 Unauthorized")
	if isTransientTelegramPollError(err) {
		t.Fatal("auth errors must surface to the operator")
	}
}

func TestIsTransientTelegramPollError_wsarecvString(t *testing.T) {
	err := errors.New(`error get updates, read tcp 192.0.2.1:12345->149.154.167.220:443: wsarecv: An established connection was aborted by the software in your host machine.`)
	if !isTransientTelegramPollError(err) {
		t.Fatal("wsarecv abort strings should be transient")
	}
}