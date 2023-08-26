package tcp

import (
	"unsafe"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/sdk/v4/pool"
)

type Srv struct {
	Addr       string `mapstructure:"addr"`
	Delimiter  string `mapstructure:"delimiter"`
	delimBytes []byte
}

type Config struct {
	Servers        map[string]*Srv `mapstructure:"servers"`
	ReadBufferSize int             `mapstructure:"read_buf_size"`
	Pool           *pool.Config    `mapstructure:"pool"`
}

func (c *Config) InitDefault() error {
	if len(c.Servers) == 0 {
		return errors.Str("no servers registered")
	}

	for k, v := range c.Servers {
		if v.Delimiter == "" {
			v.Delimiter = "\r\n"
			v.delimBytes = []byte{'\r', '\n'}
		}

		if v.Addr == "" {
			return errors.Errorf("empty address for the server: %s", k)
		}

		// already written
		if len(v.delimBytes) > 0 {
			continue
		}

		v.delimBytes = strToBytes(v.Delimiter)
	}

	if c.Pool == nil {
		c.Pool = &pool.Config{}
	}

	if c.ReadBufferSize == 0 {
		c.ReadBufferSize = 1024 * 1024 * 1
	}

	c.Pool.InitDefaults()

	return nil
}

func strToBytes(data string) []byte {
	if data == "" {
		return nil
	}

	return unsafe.Slice(unsafe.StringData(data), len(data))
}
