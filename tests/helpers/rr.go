package helpers

import (
	"context"
	"log/slog"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/logger/v6"
	"github.com/stretchr/testify/require"
)

const (
	// configVersion is the config schema version used by the test configs.
	configVersion = "2023.3.0"
	// probeTimeout caps how long Start waits for a server to accept connections.
	probeTimeout = time.Second * 15
	probeTick    = time.Millisecond * 20
	probeDial    = time.Second
	// runtimeErrTimeout caps how long StartExpectRuntimeError waits for the
	// container to report an error.
	runtimeErrTimeout = time.Second * 30
)

// bootCfg holds the options applied to a container before it is started.
type bootCfg struct {
	probe func(ctx context.Context) bool
}

// Option customizes the container built by Start.
type Option func(*bootCfg)

// WithTCPProbe makes Start return only once addr accepts a connection. The
// listener binds inside Serve after the worker pool has been allocated, so an
// accepted connection proves the pool is up.
//
// The probe connection itself produces a CONNECTED event and one worker exec, so
// a test counting execs has to skip the probe and use WaitListener instead.
func WithTCPProbe(addr string) Option {
	return func(b *bootCfg) {
		b.probe = func(ctx context.Context) bool {
			d := net.Dialer{Timeout: probeDial}

			conn, err := d.DialContext(ctx, "tcp", addr)
			if err != nil {
				return false
			}

			return conn.Close() == nil
		}
	}
}

// Start registers the plugins, boots the container and waits for the probe, if
// any, to answer. Errors arriving on the container channel are reported through
// t.Errorf and stop the container, but they do not abort the test. The container
// is stopped by t.Cleanup.
func Start(t *testing.T, cfgPath string, plugins []any, opts ...Option) {
	t.Helper()

	bc := &bootCfg{}
	for _, o := range opts {
		o(bc)
	}

	cont := newContainer(t, cfgPath, plugins)
	require.NoError(t, cont.Init())

	ch, err := cont.Serve()
	require.NoError(t, err)

	stopCont := sync.OnceValue(cont.Stop)
	done := make(chan struct{})
	wg := &sync.WaitGroup{}

	wg.Go(func() {
		for {
			select {
			case res := <-ch:
				if res == nil {
					return
				}
				t.Errorf("plugin %s reported an error: %v", res.VertexID, res.Error)
				if errS := stopCont(); errS != nil {
					t.Errorf("container stop: %v", errS)
				}
			case <-done:
				if errS := stopCont(); errS != nil {
					t.Errorf("container stop: %v", errS)
				}
				return
			}
		}
	})

	// The drain goroutine calls t.Errorf, so it has to be joined while the test
	// is still running.
	t.Cleanup(func() {
		close(done)
		wg.Wait()
	})

	if bc.probe != nil {
		require.Eventually(t, func() bool { return bc.probe(t.Context()) }, probeTimeout, probeTick, "server did not become ready")
	}
}

// StartExpectInitError registers the plugins and requires Init to fail, returning its error.
func StartExpectInitError(t *testing.T, cfgPath string, plugins []any) error {
	t.Helper()

	err := newContainer(t, cfgPath, plugins).Init()
	require.Error(t, err)

	return err
}

// StartExpectRuntimeError boots the container and returns the first error it
// reports. The tcp listeners are created in per-server goroutines after Serve has
// returned, so a listen failure never surfaces as a Serve error.
func StartExpectRuntimeError(t *testing.T, cfgPath string, plugins []any) error {
	t.Helper()

	cont := newContainer(t, cfgPath, plugins)
	require.NoError(t, cont.Init())

	ch, err := cont.Serve()
	require.NoError(t, err)
	t.Cleanup(func() { _ = cont.Stop() })

	select {
	case res := <-ch:
		require.NotNil(t, res)
		return res.Error
	case <-time.After(runtimeErrTimeout):
		require.FailNow(t, "the container reported no error")
		return nil
	}
}

// newContainer builds the container and registers the config, the logger and the
// caller's plugins. The container is not initialized yet.
func newContainer(t *testing.T, cfgPath string, plugins []any) *endure.Endure {
	t.Helper()

	all := make([]any, 0, 2+len(plugins))
	all = append(all, &config.Plugin{Version: configVersion, Path: cfgPath}, &logger.Plugin{})
	all = append(all, plugins...)

	cont := endure.New(slog.LevelDebug)
	require.NoError(t, cont.RegisterAll(all...))

	return cont
}
