package telegram

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"strings"
	"syscall"
	"time"
)

// telegramPollTimeout is the getUpdates long-poll window. Shorter than the
// library default (~59s) so idle TCP sessions are recycled before local
// firewalls or AV often abort them (WSAECONNABORTED ~50s on Windows).
const telegramPollTimeout = 45 * time.Second

func telegramPollHTTPClient(pollTimeout time.Duration) *http.Client {
	return &http.Client{
		// Slightly longer than the Telegram timeout so a healthy long-poll can
		// finish without the client aborting first.
		Timeout: pollTimeout + 15*time.Second,
		Transport: &http.Transport{
			Proxy: http.ProxyFromEnvironment,
			DialContext: (&net.Dialer{
				Timeout:   30 * time.Second,
				KeepAlive: 30 * time.Second,
			}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          4,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		},
	}
}

// isTransientTelegramPollError reports errors the go-telegram/bot library already
// retries on the next getUpdates. They must not paint LastError on the dashboard.
func isTransientTelegramPollError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) {
		return true
	}

	msg := strings.ToLower(err.Error())
	if strings.Contains(msg, "get updates") && isTransientNetErr(err) {
		return true
	}

	for _, frag := range []string{
		"wsarecv",
		"wsaconnaborted",
		"connection aborted",
		"connection reset",
		"broken pipe",
		"forcibly closed",
	} {
		if strings.Contains(msg, frag) {
			return true
		}
	}
	return isTransientNetErr(err)
}

func isTransientNetErr(err error) bool {
	var netErr net.Error
	if errors.As(err, &netErr) && netErr.Timeout() {
		return true
	}

	var op *net.OpError
	if !errors.As(err, &op) {
		return false
	}
	if errors.Is(op.Err, syscall.ECONNRESET) ||
		errors.Is(op.Err, syscall.ECONNABORTED) ||
		errors.Is(op.Err, io.EOF) {
		return true
	}
	errno, ok := op.Err.(syscall.Errno)
	if !ok {
		return false
	}
	switch errno {
	case syscall.ECONNRESET, syscall.ECONNABORTED:
		return true
	default:
		return false
	}
}