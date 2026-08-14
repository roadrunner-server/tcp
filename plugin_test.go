package tcp

import (
	"bytes"
	"context"
	"log/slog"
	"net"
	"testing"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/v2/payload"
	"github.com/roadrunner-server/pool/v2/pool"
	staticPool "github.com/roadrunner-server/pool/v2/pool/static_pool"
	"github.com/roadrunner-server/pool/v2/worker"
	"github.com/stretchr/testify/require"
)

// stubLogger satisfies the Logger dependency of Init.
type stubLogger struct{}

func (stubLogger) NamedLogger(string) *slog.Logger { return slog.New(slog.DiscardHandler) }

// stubConfigurer hands Init a prepared config, or the failure of its choice.
type stubConfigurer struct {
	cfg       *Config
	has       bool
	unmarshal error
}

func (c *stubConfigurer) Has(string) bool { return c.has }

func (c *stubConfigurer) UnmarshalKey(_ string, out any) error {
	if c.unmarshal != nil {
		return c.unmarshal
	}

	dst, ok := out.(**Config)
	if !ok {
		return errors.Str("unexpected unmarshal target")
	}

	*dst = c.cfg

	return nil
}

// stubServer fails to allocate a pool, the only NewPool outcome a stub can produce:
// the concrete *static_pool.Pool has no exported constructor.
type stubServer struct {
	err error
}

func (s *stubServer) NewPool(context.Context, *pool.Config, map[string]string, *slog.Logger) (*staticPool.Pool, error) {
	return nil, s.err
}

// fakePool records the calls the plugin makes and answers with scripted results.
type fakePool struct {
	execCh    chan *staticPool.PExec
	execErr   error
	resetErr  error
	addErr    error
	removeErr error
	workers   []*worker.Process
	destroyed bool
}

func (p *fakePool) Workers() []*worker.Process { return p.workers }

func (p *fakePool) RemoveWorker(context.Context) error { return p.removeErr }

func (p *fakePool) AddWorker() error { return p.addErr }

func (p *fakePool) Exec(context.Context, *payload.Payload, chan struct{}) (chan *staticPool.PExec, error) {
	return p.execCh, p.execErr
}

func (p *fakePool) Reset(context.Context) error { return p.resetErr }

func (p *fakePool) Destroy(context.Context) { p.destroyed = true }

// validConfig is the smallest config InitDefault accepts.
func validConfig() *Config {
	return &Config{Servers: map[string]*Srv{"server1": {Addr: "127.0.0.1:0"}}}
}

func TestPluginInitDisabled(t *testing.T) {
	p := &Plugin{}

	err := p.Init(stubLogger{}, &stubConfigurer{has: false}, &stubServer{})
	require.True(t, errors.Is(errors.Disabled, err), "expected a disabled error, got %v", err)
}

func TestPluginInitUnmarshalError(t *testing.T) {
	p := &Plugin{}

	err := p.Init(stubLogger{}, &stubConfigurer{has: true, unmarshal: errors.Str("broken section")}, &stubServer{})
	require.ErrorContains(t, err, "tcp_plugin_init")
	require.ErrorContains(t, err, "broken section")
}

func TestPluginInitConfigError(t *testing.T) {
	p := &Plugin{}

	// the config error travels without the plugin op wrapped around it
	err := p.Init(stubLogger{}, &stubConfigurer{has: true, cfg: &Config{}}, &stubServer{})
	require.EqualError(t, err, "no servers registered")
}

func TestPluginInit(t *testing.T) {
	cfg := validConfig()
	cfg.ReadBufferSize = 2

	p := &Plugin{}
	require.NoError(t, p.Init(stubLogger{}, &stubConfigurer{has: true, cfg: cfg}, &stubServer{}))

	require.Equal(t, "tcp", p.Name())

	api, ok := p.RPC().(*rpc)
	require.True(t, ok)
	require.Same(t, p, api.p)

	// read_buf_size is only observable through the buffers the pool hands out
	buf, ok := p.readBufPool.Get().(*[]byte)
	require.True(t, ok)
	require.Len(t, *buf, 2*1024*1024)

	resBuf, ok := p.resBufPool.Get().(*bytes.Buffer)
	require.True(t, ok)
	require.NotNil(t, resBuf)
}

func TestPluginServePoolError(t *testing.T) {
	cfg := validConfig()
	require.NoError(t, cfg.InitDefault())

	p := &Plugin{cfg: cfg, log: slog.New(slog.DiscardHandler), server: &stubServer{err: errors.Str("no workers")}}

	errCh := p.Serve()
	require.ErrorContains(t, <-errCh, "no workers")

	// a failed allocation leaves the interface field nil, so Stop has nothing to destroy
	require.Nil(t, p.wPool)
	require.NoError(t, p.Stop(t.Context()))
}

func TestPluginStopCanceledContext(t *testing.T) {
	p := &Plugin{}

	// hold the lock the shutdown goroutine needs so the canceled context wins
	p.mu.Lock()

	ctx, cancel := context.WithCancel(t.Context())
	cancel()

	require.ErrorIs(t, p.Stop(ctx), context.Canceled)

	p.mu.Unlock()
}

func TestPluginStopDestroysPool(t *testing.T) {
	fp := &fakePool{}
	p := &Plugin{wPool: fp}

	client, srv := net.Pipe()
	t.Cleanup(func() { _ = srv.Close() })
	p.connections.Store("conn", client)

	var lc net.ListenConfig
	listener, err := lc.Listen(t.Context(), "tcp", "127.0.0.1:0")
	require.NoError(t, err)
	p.listeners.Store("listener", listener)

	require.NoError(t, p.Stop(t.Context()))
	require.True(t, fp.destroyed)

	_, err = client.Write([]byte("x"))
	require.Error(t, err)

	var d net.Dialer
	_, err = d.DialContext(t.Context(), "tcp", listener.Addr().String())
	require.Error(t, err)
}

func TestPluginClose(t *testing.T) {
	p := &Plugin{}

	client, srv := net.Pipe()
	t.Cleanup(func() { _ = srv.Close() })
	p.connections.Store("known", client)

	require.NoError(t, p.Close("known"))

	_, err := client.Write([]byte("x"))
	require.Error(t, err)

	_, stored := p.connections.Load("known")
	require.False(t, stored)

	// the entry is gone, so a repeat call and an unknown uuid are both no-ops
	require.NoError(t, p.Close("known"))
	require.NoError(t, p.Close("unknown"))
}

func TestRPCClose(t *testing.T) {
	p := &Plugin{}

	client, srv := net.Pipe()
	t.Cleanup(func() { _ = srv.Close() })
	p.connections.Store("known", client)

	api, ok := p.RPC().(*rpc)
	require.True(t, ok)

	var closed bool
	require.NoError(t, api.Close("known", &closed))
	require.True(t, closed)

	// the caller is told the connection is gone even when the uuid was never stored
	closed = false
	require.NoError(t, api.Close("unknown", &closed))
	require.True(t, closed)
}

func TestPluginExecErrors(t *testing.T) {
	t.Run("pool failure", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{execErr: errors.Str("pool is down")}}

		_, err := p.Exec(&payload.Payload{})
		require.ErrorContains(t, err, "pool is down")
	})

	t.Run("no response on the channel", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{execCh: make(chan *staticPool.PExec, 1)}}

		_, err := p.Exec(&payload.Payload{})
		require.ErrorContains(t, err, "empty response")
	})
}

func TestPluginReset(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{}, log: slog.New(slog.DiscardHandler)}
		require.NoError(t, p.Reset())
	})

	t.Run("pool failure", func(t *testing.T) {
		p := &Plugin{wPool: &fakePool{resetErr: errors.Str("reset refused")}, log: slog.New(slog.DiscardHandler)}

		err := p.Reset()
		require.ErrorContains(t, err, "tcp_reset")
		require.ErrorContains(t, err, "reset refused")
	})
}

func TestPluginWorkersEmptyPool(t *testing.T) {
	p := &Plugin{wPool: &fakePool{}, log: slog.New(slog.DiscardHandler)}

	states := p.Workers()
	require.NotNil(t, states)
	require.Empty(t, states)
}
