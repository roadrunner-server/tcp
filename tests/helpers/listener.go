package helpers

import (
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// ListenerTimeout caps how long WaitListener waits for an address to accept.
	ListenerTimeout = time.Second * 15
	// ListenerTick is the interval between two connection attempts.
	ListenerTick = time.Millisecond * 20
)

// WaitListener waits until addr accepts a connection. Every server of one config
// binds in its own goroutine, so the readiness of the address Start probed says
// nothing about the others.
func WaitListener(t *testing.T, addr string) {
	t.Helper()

	require.Eventually(t, func() bool {
		var d net.Dialer

		conn, err := d.DialContext(t.Context(), "tcp", addr)
		if err != nil {
			return false
		}

		return conn.Close() == nil
	}, ListenerTimeout, ListenerTick, "listener %s did not start", addr)
}
