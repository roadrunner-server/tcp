package handler

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/roadrunner-server/pool/v2/payload"
	"github.com/stretchr/testify/require"
)

const (
	// defaultReadBufSize matches the plugin default of one megabyte.
	defaultReadBufSize = 1024 * 1024
	// ioTimeout caps a single read or write on the client side of the rig.
	ioTimeout = time.Second * 10
	// stopTimeout caps how long a test waits for the handler goroutine to return.
	stopTimeout = time.Second * 10
	// pollTick is the interval between two checks of the recorded calls.
	pollTick = time.Millisecond * 5
	// idleTimeout is the window requireNothingWritten gives the handler to write.
	idleTimeout = time.Millisecond * 250
	// clientBufSize holds the largest body the tests write back to the client.
	clientBufSize = 1024
)

// recordedCall is one payload the handler pushed to the worker pool.
type recordedCall struct {
	info ServerInfo
	body []byte
}

// replyFunc answers a single exec call; seq is the zero-based call index.
type replyFunc func(call recordedCall, seq int) (*payload.Payload, error)

// fakeWorker stands in for the plugin Exec function: it records what the handler
// sends and answers with whatever the test scripted.
type fakeWorker struct {
	mu    sync.Mutex
	calls []recordedCall
	reply replyFunc
}

func (w *fakeWorker) exec(pld *payload.Payload) (*payload.Payload, error) {
	// the handler recycles the payload as soon as the call returns, so copy first
	call := recordedCall{body: bytes.Clone(pld.Body)}
	if err := json.Unmarshal(pld.Context, &call.info); err != nil {
		return nil, err
	}

	w.mu.Lock()
	seq := len(w.calls)
	w.calls = append(w.calls, call)
	w.mu.Unlock()

	return w.reply(call, seq)
}

func (w *fakeWorker) snapshot() []recordedCall {
	w.mu.Lock()
	defer w.mu.Unlock()

	return append([]recordedCall(nil), w.calls...)
}

// respondWith builds the payload a worker would answer an event with.
func respondWith(ctx []byte, body string) (*payload.Payload, error) {
	return &payload.Payload{Context: ctx, Body: []byte(body)}, nil
}

// keepReading answers every event with CONTINUE, the reply that keeps the read
// loop running without writing anything back to the client.
func keepReading(recordedCall, int) (*payload.Payload, error) {
	return respondWith(CONTINUE, "")
}

// logSink captures the records the handler writes.
type logSink struct {
	mu      sync.Mutex
	entries []string
}

func (s *logSink) Enabled(context.Context, slog.Level) bool { return true }

func (s *logSink) Handle(_ context.Context, r slog.Record) error {
	s.mu.Lock()
	s.entries = append(s.entries, r.Message)
	s.mu.Unlock()

	return nil
}

func (s *logSink) WithAttrs([]slog.Attr) slog.Handler { return s }

func (s *logSink) WithGroup(string) slog.Handler { return s }

func (s *logSink) messages() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	return append([]string(nil), s.entries...)
}

// bufferPools mirrors the four sync.Pools the plugin hands to every handler.
type bufferPools struct {
	payloads *sync.Pool
	servInfo *sync.Pool
	readBuf  *sync.Pool
	resBuf   *sync.Pool
}

func newPools(readBufSize int) bufferPools {
	return bufferPools{
		payloads: &sync.Pool{New: func() any { return new(payload.Payload) }},
		servInfo: &sync.Pool{New: func() any { return new(ServerInfo) }},
		readBuf: &sync.Pool{New: func() any {
			buf := make([]byte, readBufSize)
			return &buf
		}},
		resBuf: &sync.Pool{New: func() any {
			buf := new(bytes.Buffer)
			buf.Grow(readBufSize)
			return buf
		}},
	}
}

// readErrConn reports a read failure that is not io.EOF.
type readErrConn struct {
	net.Conn
	err error
}

func (c readErrConn) Read([]byte) (int, error) { return 0, c.err }

// writeErrConn fails every write, standing in for a client that went away while
// the worker was preparing its answer.
type writeErrConn struct {
	net.Conn
	err error
}

func (c writeErrConn) Write([]byte) (int, error) { return 0, c.err }

// rigConfig describes the handler a test wants.
type rigConfig struct {
	// delim is the packet delimiter; defaults to CRLF.
	delim []byte
	// serverName is the name reported in every event context.
	serverName string
	// readBufSize is the size of the buffer the handler reads into.
	readBufSize int
	// wrap replaces the accepted connection the handler talks over.
	wrap func(net.Conn) net.Conn
}

// rig is a handler running against one end of a loopback connection.
type rig struct {
	client net.Conn
	worker *fakeWorker
	conns  *sync.Map
	logs   *logSink
	done   chan struct{}
}

// newRig starts a handler on the server side of a loopback pair and returns the
// client side together with everything the handler writes to.
func newRig(t *testing.T, cfg rigConfig, reply replyFunc) *rig {
	t.Helper()

	if cfg.delim == nil {
		cfg.delim = []byte("\r\n")
	}
	if cfg.serverName == "" {
		cfg.serverName = "server1"
	}
	if cfg.readBufSize == 0 {
		cfg.readBufSize = defaultReadBufSize
	}

	var lc net.ListenConfig
	ln, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	t.Cleanup(func() { _ = ln.Close() })

	type accepted struct {
		conn net.Conn
		err  error
	}

	acceptCh := make(chan accepted, 1)
	go func() {
		conn, errA := ln.Accept()
		acceptCh <- accepted{conn: conn, err: errA}
	}()

	var d net.Dialer
	client, err := d.DialContext(t.Context(), "tcp", ln.Addr().String())
	require.NoError(t, err)
	t.Cleanup(func() { _ = client.Close() })

	a := <-acceptCh
	require.NoError(t, a.err)
	t.Cleanup(func() { _ = a.conn.Close() })

	srvConn := a.conn
	if cfg.wrap != nil {
		srvConn = cfg.wrap(srvConn)
	}

	r := &rig{
		client: client,
		worker: &fakeWorker{reply: reply},
		conns:  &sync.Map{},
		logs:   &logSink{},
		done:   make(chan struct{}),
	}

	pools := newPools(cfg.readBufSize)
	h := NewHandler(srvConn, cfg.delim, cfg.serverName, r.worker.exec,
		pools.payloads, pools.servInfo, pools.readBuf, pools.resBuf, r.conns, slog.New(r.logs))

	go func() {
		h.Start()
		h.Release()
		close(r.done)
	}()

	// closing the client releases a handler still parked in its read loop
	t.Cleanup(func() {
		_ = client.Close()
		_ = a.conn.Close()

		select {
		case <-r.done:
		case <-time.After(stopTimeout):
			t.Error("the handler did not return after the connection was closed")
		}
	})

	return r
}

// requireCalls waits until the worker has seen n calls and returns them.
func (r *rig) requireCalls(t *testing.T, n int) []recordedCall {
	t.Helper()

	require.Eventually(t, func() bool {
		return len(r.worker.snapshot()) >= n
	}, ioTimeout, pollTick, "the worker received fewer than %d calls", n)

	return r.worker.snapshot()
}

// events returns the event names the worker was called with, in order.
func (r *rig) events() []string {
	calls := r.worker.snapshot()

	events := make([]string, 0, len(calls))
	for _, c := range calls {
		events = append(events, c.info.Event)
	}

	return events
}

// waitStopped waits for the handler goroutine to return.
func (r *rig) waitStopped(t *testing.T) {
	t.Helper()

	select {
	case <-r.done:
	case <-time.After(stopTimeout):
		require.FailNow(t, "the handler is still running")
	}
}

// requireLogged requires snippet to appear in one of the captured log messages.
func requireLogged(t *testing.T, logs *logSink, snippet string) {
	t.Helper()

	require.Eventually(t, func() bool {
		for _, m := range logs.messages() {
			if bytes.Contains([]byte(m), []byte(snippet)) {
				return true
			}
		}

		return false
	}, ioTimeout, pollTick, "no log record contains %q, got %v", snippet, logs.messages())
}

func writeClient(t *testing.T, conn net.Conn, payload string) {
	t.Helper()

	require.NoError(t, conn.SetWriteDeadline(time.Now().Add(ioTimeout)))

	_, err := conn.Write([]byte(payload))
	require.NoError(t, err)
}

func readClient(t *testing.T, conn net.Conn) string {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(ioTimeout)))

	buf := make([]byte, clientBufSize)
	n, err := conn.Read(buf)
	require.NoError(t, err)

	return string(buf[:n])
}

// requireClientClosed requires the client side to reach EOF without receiving
// anything the test has not already read.
func requireClientClosed(t *testing.T, conn net.Conn) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(ioTimeout)))

	buf := make([]byte, clientBufSize)
	for {
		n, err := conn.Read(buf)
		if err != nil {
			require.True(t, errors.Is(err, io.EOF), "the handler kept the connection open: %v", err)
			return
		}

		require.Failf(t, "unexpected data", "the handler wrote %q before closing", buf[:n])
	}
}

// requireNothingWritten requires the handler to send nothing within a short
// window, leaving the connection open.
func requireNothingWritten(t *testing.T, conn net.Conn) {
	t.Helper()

	require.NoError(t, conn.SetReadDeadline(time.Now().Add(idleTimeout)))

	buf := make([]byte, clientBufSize)
	n, err := conn.Read(buf)
	require.Error(t, err, "the handler wrote %q", buf[:n])

	var netErr net.Error
	require.ErrorAs(t, err, &netErr)
	require.True(t, netErr.Timeout(), "the connection is not readable anymore: %v", err)

	// drop the expired deadline so the connection stays usable
	require.NoError(t, conn.SetReadDeadline(time.Time{}))
}
