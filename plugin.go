package tcp

import (
	"bytes"
	"context"
	"net"
	"sync"

	"github.com/google/uuid"
	rrErrors "github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/sdk/v3/payload"
	"github.com/roadrunner-server/sdk/v3/pool"
	staticPool "github.com/roadrunner-server/sdk/v3/pool/static_pool"
	"github.com/roadrunner-server/sdk/v3/state/process"
	"github.com/roadrunner-server/sdk/v3/utils"
	"github.com/roadrunner-server/sdk/v3/worker"
	"github.com/roadrunner-server/tcp/v3/handler"
	"go.uber.org/zap"
)

const (
	pluginName string = "tcp"
	RrMode     string = "RR_MODE"
)

type Pool interface {
	// Workers returns worker list associated with the pool.
	Workers() (workers []*worker.Process)

	// Exec payload
	Exec(ctx context.Context, p *payload.Payload) (*payload.Payload, error)

	// Reset kill all workers inside the watcher and replaces with new
	Reset(ctx context.Context) error

	// Destroy all underlying stack (but let them complete the task).
	Destroy(ctx context.Context)
}

// Server creates workers for the application.
type Server interface {
	NewPool(ctx context.Context, cfg *pool.Config, env map[string]string, _ *zap.Logger) (*staticPool.Pool, error)
}

type Configurer interface {
	// UnmarshalKey takes a single key and unmarshal it into a Struct.
	UnmarshalKey(name string, out any) error
	// Has checks if config section exists.
	Has(name string) bool
}

type Plugin struct {
	sync.RWMutex
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

func (p *Plugin) Init(log *zap.Logger, cfg Configurer, server Server) error {
	const op = rrErrors.Op("tcp_plugin_init")

	if !cfg.Has(pluginName) {
		return rrErrors.E(op, rrErrors.Disabled)
	}

	err := cfg.UnmarshalKey(pluginName, &p.cfg)
	if err != nil {
		return rrErrors.E(op, err)
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

	p.log = new(zap.Logger)
	*p.log = *log
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
			l, err := utils.CreateListener(addr)
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

func (p *Plugin) Stop() error {
	// close all connections
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

	return nil
}

func (p *Plugin) Reset() error {
	p.Lock()
	defer p.Unlock()
	const op = rrErrors.Op("tcp_reset")
	p.log.Info("reset signal was received")
	err := p.wPool.Reset(context.Background())
	if err != nil {
		return rrErrors.E(op, err)
	}

	p.log.Info("plugin was successfully reset")

	return nil
}

func (p *Plugin) Workers() []*process.State {
	p.RLock()
	wrk := p.wPool.Workers()
	p.RUnlock()

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

func (p *Plugin) RPC() interface{} {
	return &rpc{
		p: p,
	}
}

func (p *Plugin) Exec(pld *payload.Payload) (*payload.Payload, error) {
	p.RLock()
	rsp, err := p.wPool.Exec(context.Background(), pld)
	if err != nil {
		p.RUnlock()
		return nil, err
	}

	p.RUnlock()
	return rsp, nil
}
