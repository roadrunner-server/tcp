package handler

import (
	"errors"
	"net"
	"strings"
	"testing"

	"github.com/roadrunner-server/pool/v2/payload"
	"github.com/stretchr/testify/require"
)

func TestHandlerConnectedEvent(t *testing.T) {
	r := newRig(t, rigConfig{serverName: "tcp_access_point_1"}, keepReading)

	// the worker is called on connect, before the client has sent anything
	calls := r.requireCalls(t, 1)
	connected := calls[0].info

	require.Equal(t, EventConnected, connected.Event)
	require.Equal(t, "tcp_access_point_1", connected.Server)
	require.Equal(t, r.client.LocalAddr().String(), connected.RemoteAddr)
	require.NotEmpty(t, connected.UUID)
	require.Empty(t, calls[0].body)

	// the uuid the worker sees is the key tcp.Close looks the connection up by
	stored, ok := r.conns.Load(connected.UUID)
	require.True(t, ok)
	require.NotNil(t, stored)
}

func TestHandlerResponseContexts(t *testing.T) {
	cases := []struct {
		name        string
		context     []byte
		body        string
		wantWritten string
		staysOpen   bool
	}{
		{name: "continue", context: CONTINUE, body: "not written", staysOpen: true},
		{name: "write", context: WRITE, body: "pong\r\n", wantWritten: "pong\r\n", staysOpen: true},
		{name: "write and close", context: WRITECLOSE, body: "bye\r\n", wantWritten: "bye\r\n"},
		{name: "close", context: CLOSE, body: "not written"},
		{name: "unknown context", context: []byte("NONSENSE"), body: "not written"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newRig(t, rigConfig{}, func(call recordedCall, _ int) (*payload.Payload, error) {
				if call.info.Event == EventIncomingData {
					return respondWith(tc.context, tc.body)
				}

				return respondWith(CONTINUE, "")
			})

			r.requireCalls(t, 1)
			writeClient(t, r.client, "ping\r\n")

			// the handler forwards the read bytes verbatim, delimiter included
			calls := r.requireCalls(t, 2)
			require.Equal(t, EventIncomingData, calls[1].info.Event)
			require.Equal(t, "ping\r\n", string(calls[1].body))
			require.Equal(t, calls[0].info.UUID, calls[1].info.UUID)

			if tc.wantWritten != "" {
				require.Equal(t, tc.wantWritten, readClient(t, r.client))
			}

			if tc.staysOpen {
				if tc.wantWritten == "" {
					requireNothingWritten(t, r.client)
				}

				// the loop is still running, so a second packet reaches the worker
				writeClient(t, r.client, "ping\r\n")
				r.requireCalls(t, 3)

				return
			}

			requireClientClosed(t, r.client)
			r.waitStopped(t)
			require.Equal(t, []string{EventConnected, EventIncomingData, EventClose}, r.events())
		})
	}
}

func TestHandlerPayloadLargerThanBuffer(t *testing.T) {
	// the reads that do not end with the delimiter are accumulated instead of dispatched
	r := newRig(t, rigConfig{readBufSize: 8}, keepReading)
	r.requireCalls(t, 1)

	body := strings.Repeat("z", 30) + "\r\n"
	writeClient(t, r.client, body)

	calls := r.requireCalls(t, 2)
	require.Equal(t, EventIncomingData, calls[1].info.Event)
	require.Equal(t, body, string(calls[1].body))
}

func TestHandlerMultiByteDelimiter(t *testing.T) {
	r := newRig(t, rigConfig{delim: []byte("--end--")}, keepReading)
	r.requireCalls(t, 1)

	writeClient(t, r.client, "payload--end--")

	calls := r.requireCalls(t, 2)
	require.Equal(t, "payload--end--", string(calls[1].body))
}

func TestHandlerTooSmallPayload(t *testing.T) {
	r := newRig(t, rigConfig{}, keepReading)
	r.requireCalls(t, 1)

	// a read shorter than the two byte delimiter cannot be matched against it
	writeClient(t, r.client, "x")

	requireClientClosed(t, r.client)
	r.waitStopped(t)

	require.Equal(t, []string{EventConnected, EventClose}, r.events())
	requireLogged(t, r.logs, "too small payload")
}

func TestHandlerReadError(t *testing.T) {
	r := newRig(t, rigConfig{
		wrap: func(c net.Conn) net.Conn { return readErrConn{Conn: c, err: errors.New("connection reset")} },
	}, keepReading)

	requireClientClosed(t, r.client)
	r.waitStopped(t)

	require.Equal(t, []string{EventConnected, EventClose}, r.events())
	requireLogged(t, r.logs, "read error, connection closed")
}

func TestHandlerClientDisconnects(t *testing.T) {
	r := newRig(t, rigConfig{}, keepReading)
	r.requireCalls(t, 1)

	require.NoError(t, r.client.Close())
	r.waitStopped(t)

	// the EOF path reports the close event and leaves the loop
	require.Equal(t, []string{EventConnected, EventClose}, r.events())
}

func TestHandlerConnectedEventFails(t *testing.T) {
	r := newRig(t, rigConfig{}, func(recordedCall, int) (*payload.Payload, error) {
		return nil, errors.New("pool is down")
	})

	requireClientClosed(t, r.client)
	r.waitStopped(t)

	// the read loop is never entered, so no close event follows
	require.Equal(t, []string{EventConnected}, r.events())
	requireLogged(t, r.logs, "execute error")

	uuid := r.worker.snapshot()[0].info.UUID
	_, stored := r.conns.Load(uuid)
	require.False(t, stored)
}

func TestHandlerDataEventFails(t *testing.T) {
	r := newRig(t, rigConfig{}, func(call recordedCall, _ int) (*payload.Payload, error) {
		if call.info.Event == EventIncomingData {
			return nil, errors.New("pool is down")
		}

		return respondWith(CONTINUE, "")
	})

	r.requireCalls(t, 1)
	writeClient(t, r.client, "ping\r\n")

	requireClientClosed(t, r.client)
	r.waitStopped(t)

	require.Equal(t, []string{EventConnected, EventIncomingData}, r.events())
	requireLogged(t, r.logs, "execute error")
}

func TestHandlerWriteFails(t *testing.T) {
	r := newRig(t, rigConfig{
		wrap: func(c net.Conn) net.Conn { return writeErrConn{Conn: c, err: errors.New("broken pipe")} },
	}, func(call recordedCall, _ int) (*payload.Payload, error) {
		if call.info.Event == EventIncomingData {
			return respondWith(WRITE, "pong\r\n")
		}

		return respondWith(CONTINUE, "")
	})

	r.requireCalls(t, 1)
	writeClient(t, r.client, "ping\r\n")

	requireClientClosed(t, r.client)
	r.waitStopped(t)

	require.Equal(t, []string{EventConnected, EventIncomingData, EventClose}, r.events())
	requireLogged(t, r.logs, "write response error")
}

func TestHandlerCloseEventFails(t *testing.T) {
	r := newRig(t, rigConfig{}, func(call recordedCall, _ int) (*payload.Payload, error) {
		switch call.info.Event {
		case EventIncomingData:
			return respondWith(CLOSE, "")
		case EventClose:
			return nil, errors.New("pool is down")
		default:
			return respondWith(CONTINUE, "")
		}
	})

	r.requireCalls(t, 1)
	writeClient(t, r.client, "ping\r\n")

	requireClientClosed(t, r.client)
	r.waitStopped(t)

	require.Equal(t, []string{EventConnected, EventIncomingData, EventClose}, r.events())
	requireLogged(t, r.logs, "failed to send close event to worker")
}
