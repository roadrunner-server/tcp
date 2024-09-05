package tcp

import (
	"bytes"
	"context"
	"net"
	"sync"

	"github.com/google/uuid"
	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/goridge/v3/pkg/frame"
	"github.com/roadrunner-server/pool/payload"
	"github.com/roadrunner-server/pool/pool"
	staticPool "github.com/roadrunner-server/pool/pool/static_pool"
	"github.com/roadrunner-server/pool/state/process"
	"github.com/roadrunner-server/pool/worker"
	"github.com/roadrunner-server/tcp/v5/handler"
	"github.com/roadrunner-server/tcplisten"
	"go.uber.org/zap"
)

const (
	pluginName string = "tcp"
	RrMode     string = "RR_MODE"
)

type Pool interface {
	// Workers return a worker list associated with the pool.
	Workers() (workers []*worker.Process)
	// RemoveWorker removes worker from the pool.
	RemoveWorker(ctx context.Context) error
	// AddWorker adds worker to the pool.
	AddWorker() error
	// Exec payload
	Exec(ctx context.Context, p *payload.Payload, stopCh chan struct{}) (chan *staticPool.PExec, error)
	// Reset kills all workers inside the watcher and replaces with new
	Reset(ctx context.Context) error
	// Destroy all underlying stacks (but let them complete the task).
	Destroy(ctx context.Context)
}

type Logger interface {
	NamedLogger(name string) *zap.Logger
}

// Server creates workers for the application.
type Server interface {
	NewPool(ctx context.Context, cfg *pool.Config, env map[string]string, _ *zap.Logger) (*staticPool.Pool, error)
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if a config section exists.
	Has(name string) bool
}

type Plugin struct {
	mu          sync.RWMutex
	cfg         *Config
	log         *zap.Logger
	server      Server
	connections sync.Map // uuid -> conn

	wPool     Pool
	listeners sync.Map // server -> listener

	resBufPool   sync.Pool
	readBufPool  sync.Pool
	servInfoPool sync.Pool
	pldPool      sync.Pool
}

func (p *Plugin) Init(log Logger, cfg Configurer, server Server) error {
	const op = errors.Op("tcp_plugin_init")

	if !cfg.Has(pluginName) {
		return errors.E(op, errors.Disabled)
	}

	err := cfg.UnmarshalKey(pluginName, &p.cfg)
	if err != nil {
		return errors.E(op, err)
	}

	err = p.cfg.InitDefault()
	if err != nil {
		return err
	}

	// buffer sent to the user
	p.resBufPool = sync.Pool{
		New: func() interface{} {
			buf := new(bytes.Buffer)
			buf.Grow(p.cfg.ReadBufferSize)
			return buf
		},
	}

	// cyclic buffer to read the data from the connection
	p.readBufPool = sync.Pool{
		New: func() interface{} {
			buf := make([]byte, p.cfg.ReadBufferSize)
			return &buf
		},
	}

	p.servInfoPool = sync.Pool{
		New: func() interface{} {
			return new(handler.ServerInfo)
		},
	}

	p.pldPool = sync.Pool{
		New: func() interface{} {
			return new(payload.Payload)
		},
	}

	p.log = log.NamedLogger(pluginName)
	p.server = server
	return nil
}

func (p *Plugin) Serve() chan error {
	errCh := make(chan error, 1)

	var err error
	p.wPool, err = p.server.NewPool(context.Background(), p.cfg.Pool, map[string]string{RrMode: pluginName}, nil)
	if err != nil {
		errCh <- err
		return errCh
	}

	for k := range p.cfg.Servers {
		go func(addr string, delim []byte, name string) {
			// create a TCP listener
			l, err := tcplisten.CreateListener(addr)
			if err != nil {
				errCh <- err
				return
			}

			p.listeners.Store(uuid.NewString(), l)

			for {
				conn, errA := l.Accept()
				if errA != nil {
					p.log.Warn("failed to accept the connection", zap.Error(errA))
					// just stop
					return
				}

				go func() {
					h := handler.NewHandler(conn, delim, name, p.Exec, &p.pldPool, &p.servInfoPool, &p.readBufPool, &p.resBufPool, &p.connections, p.log)
					h.Start()
					// release resources
					h.Release()
				}()
			}
		}(p.cfg.Servers[k].Addr, p.cfg.Servers[k].delimBytes, k)
	}

	return errCh
}

func (p *Plugin) Stop(ctx context.Context) error {
	doneCh := make(chan struct{}, 1)

	go func() {
		// close all connections
		p.mu.Lock()
		defer p.mu.Unlock()

		p.connections.Range(func(_, value interface{}) bool {
			conn := value.(net.Conn)
			if conn != nil {
				_ = conn.Close()
			}
			return true
		})

		// then close all listeners
		p.listeners.Range(func(_, value interface{}) bool {
			_ = value.(net.Listener).Close()
			return true
		})
		if p.wPool != nil {
			switch pp := p.wPool.(type) {
			case *staticPool.Pool:
				if pp != nil {
					pp.Destroy(ctx)
				}
			default:
				// pool is nil, nothing to do
			}
		}

		doneCh <- struct{}{}
	}()

	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-doneCh:
		return nil
	}
}

func (p *Plugin) Reset() error {
	p.mu.Lock()
	defer p.mu.Unlock()
	const op = errors.Op("tcp_reset")
	p.log.Info("reset signal was received")
	err := p.wPool.Reset(context.Background())
	if err != nil {
		return errors.E(op, err)
	}

	p.log.Info("plugin was successfully reset")

	return nil
}

func (p *Plugin) Workers() []*process.State {
	p.mu.RLock()
	wrk := p.wPool.Workers()
	p.mu.RUnlock()

	ps := make([]*process.State, len(wrk))

	for i := 0; i < len(wrk); i++ {
		st, err := process.WorkerProcessState(wrk[i])
		if err != nil {
			p.log.Error("jobs workers state", zap.Error(err))
			return nil
		}

		ps[i] = st
	}

	return ps
}

func (p *Plugin) Name() string {
	return pluginName
}

func (p *Plugin) Close(uuid string) error {
	if c, ok := p.connections.LoadAndDelete(uuid); ok {
		conn := c.(net.Conn)
		if conn != nil {
			return conn.Close()
		}
	}

	return nil
}

func (p *Plugin) RPC() any {
	return &rpc{
		p: p,
	}
}

func (p *Plugin) Exec(epld *payload.Payload) (*payload.Payload, error) {
	p.mu.RLock()

	result, err := p.wPool.Exec(context.Background(), epld, nil)
	if err != nil {
		p.mu.RUnlock()
		return nil, err
	}

	var r *payload.Payload

	select {
	case pld := <-result:
		if pld.Error() != nil {
			p.mu.RUnlock()
			return nil, pld.Error()
		}
		// streaming is not supported
		if pld.Payload().Flags&frame.STREAM != 0 {
			p.mu.RUnlock()
			return nil, errors.Str("streaming is not supported")
		}

		// assign the payload
		r = pld.Payload()
	default:
		p.mu.RUnlock()
		return nil, errors.Str("activity worker empty response")
	}

	p.mu.RUnlock()
	return r, nil
}
