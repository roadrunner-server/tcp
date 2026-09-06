package tcp

import (
	"fmt"
	"math"
	"reflect"
	"strconv"

	"github.com/roadrunner-server/errors"
	"github.com/roadrunner-server/pool/v2/pool"
	"github.com/roadrunner-server/tcplisten"
)

type Srv struct {
	Addr       string                       `mapstructure:"addr"`
	UnixSocket *tcplisten.UnixSocketOptions `mapstructure:"unix_socket"`
	Delimiter  string                       `mapstructure:"delimiter"`
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

		if err := v.UnixSocket.Validate(v.Addr); err != nil {
			return fmt.Errorf("tcp.servers.%s.unix_socket: %w", k, err)
		}

		// already written
		if len(v.delimBytes) > 0 {
			continue
		}

		v.delimBytes = []byte(v.Delimiter)
	}

	if c.Pool == nil {
		c.Pool = &pool.Config{}
	}
	c.Pool.InitDefaults()

	if c.ReadBufferSize == 0 {
		// 1mb by default
		c.ReadBufferSize = 1
	}

	// convert to megabytes
	c.ReadBufferSize *= 1024 * 1024

	return nil
}

// Weak decoding into *int can convert booleans and fractions to IDs.
func validateUnixSocketIDs(cfg Configurer, key string) error {
	var raw map[string]any
	if err := cfg.UnmarshalKey(key, &raw); err != nil {
		return fmt.Errorf("%s: %w", key, err)
	}

	const maxID = 4294967295
	for _, field := range []string{"uid", "gid"} {
		if raw[field] == nil {
			continue
		}
		value := reflect.ValueOf(raw[field])
		valid := false
		switch value.Kind() { //nolint:exhaustive // Other kinds are not valid IDs.
		case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
			id := value.Int()
			valid = id >= 0 && id < maxID
		case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Uintptr:
			valid = value.Uint() < maxID
		case reflect.String:
			id, err := strconv.ParseInt(value.String(), 0, strconv.IntSize)
			valid = err == nil && id >= 0 && id < maxID
		case reflect.Float32, reflect.Float64:
			id := value.Float()
			valid = id >= 0 && id < maxID && id == math.Trunc(id)
		}
		if !valid {
			return fmt.Errorf("%s.%s: must be an integer from 0 through 4294967294", key, field)
		}
	}
	return nil
}
