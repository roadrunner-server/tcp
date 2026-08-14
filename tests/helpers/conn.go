package helpers

import (
	"encoding/json"
	"errors"
	"io"
	"net"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

const (
	// ioTimeout caps a single read or write on a test connection.
	ioTimeout = time.Second * 15
	// readBufSize holds the largest response the test workers produce.
	readBufSize = 1024
)

// WorkerResponse is the JSON document the tcp test workers echo back.
type WorkerResponse struct {
	RemoteAddr string `json:"remote_addr"`
	Server     string `json:"server"`
	UUID       string `json:"uuid"`
	Body       string `json:"body"`
	Event      string `json:"event"`
}

// Dial connects to a tcp server of the running container. The connection is
// closed by t.Cleanup.
func Dial(t *testing.T, addr string) net.Conn {
	t.Helper()

	var d net.Dialer
	conn, err := d.DialContext(t.Context(), "tcp", addr)
	require.NoError(t, err)
	t.Cleanup(func() { _ = conn.Close() })

	return conn
}

// Write sends payload over conn.
func Write(t *testing.T, conn net.Conn, payload string) {
	t.Helper()

	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(ioTimeout)))

	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
}

// ReadRaw returns the bytes of a single read from conn.
func ReadRaw(t *testing.T, conn net.Conn) []byte {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(ioTimeout)))

	buf := make([]byte, readBufSize)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	return buf[:n]
}

// ReadResponse decodes a single worker response from conn.
func ReadResponse(t *testing.T, conn net.Conn) WorkerResponse {
	t.Helper()

	var resp WorkerResponse
	require.NoError(t, json.Unmarshal(ReadRaw(t, conn), &resp))

	return resp
}

// WriteRead sends payload and decodes the answer the worker writes back.
func WriteRead(t *testing.T, conn net.Conn, payload string) WorkerResponse {
	t.Helper()

	Write(t, conn, payload)

	return ReadResponse(t, conn)
}

// RequireClosed requires conn to reach EOF, that is, the server closed its side,
// without sending anything the caller has not read yet.
func RequireClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(ioTimeout)))

	buf := make([]byte, readBufSize)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			require.True(t, errors.Is(err, io.EOF), "the server kept the connection open: %v", err)
			return
		}

		require.Failf(t, "unexpected data", "the server wrote %q before closing", buf[:n])
	}
}
