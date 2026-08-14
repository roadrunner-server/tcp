package tcp

import (
	"testing"
	"time"

	"github.com/roadrunner-server/pool/v2/pool"
	"github.com/stretchr/testify/require"
)

func TestConfigInitDefaultErrors(t *testing.T) {
	cases := []struct {
		name    string
		cfg     *Config
		message string
	}{
		{
			name:    "nil servers map",
			cfg:     &Config{},
			message: "no servers registered",
		},
		{
			name:    "empty servers map",
			cfg:     &Config{Servers: map[string]*Srv{}},
			message: "no servers registered",
		},
		{
			name:    "server without address",
			cfg:     &Config{Servers: map[string]*Srv{"tcp_access_point_1": {Delimiter: "\n"}}},
			message: "empty address for the server: tcp_access_point_1",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			require.ErrorContains(t, tc.cfg.InitDefault(), tc.message)
		})
	}
}

func TestConfigDelimiter(t *testing.T) {
	cases := []struct {
		name       string
		delimiter  string
		wantDelim  string
		wantConfig string
	}{
		{
			name:       "defaults to CRLF",
			delimiter:  "",
			wantDelim:  "\r\n",
			wantConfig: "\r\n",
		},
		{
			name:       "single byte",
			delimiter:  "\n",
			wantDelim:  "\n",
			wantConfig: "\n",
		},
		{
			name:       "multi byte",
			delimiter:  "--end--",
			wantDelim:  "--end--",
			wantConfig: "--end--",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			srv := &Srv{Addr: "127.0.0.1:0", Delimiter: tc.delimiter}
			cfg := &Config{Servers: map[string]*Srv{"server1": srv}}

			require.NoError(t, cfg.InitDefault())
			require.Equal(t, tc.wantDelim, string(srv.delimBytes))
			require.Equal(t, tc.wantConfig, srv.Delimiter)
		})
	}
}

func TestConfigReadBufferSize(t *testing.T) {
	cases := []struct {
		name  string
		given int
		want  int
	}{
		{name: "defaults to one megabyte", given: 0, want: 1024 * 1024},
		{name: "converted from megabytes", given: 10, want: 10 * 1024 * 1024},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			cfg := &Config{
				Servers:        map[string]*Srv{"server1": {Addr: "127.0.0.1:0"}},
				ReadBufferSize: tc.given,
			}

			require.NoError(t, cfg.InitDefault())
			require.Equal(t, tc.want, cfg.ReadBufferSize)
		})
	}
}

func TestConfigPoolDefaults(t *testing.T) {
	t.Run("allocated when absent", func(t *testing.T) {
		cfg := &Config{Servers: map[string]*Srv{"server1": {Addr: "127.0.0.1:0"}}}

		require.NoError(t, cfg.InitDefault())
		require.NotNil(t, cfg.Pool)
		require.NotZero(t, cfg.Pool.NumWorkers)
		require.Equal(t, time.Minute, cfg.Pool.AllocateTimeout)
	})

	t.Run("provided values kept", func(t *testing.T) {
		cfg := &Config{
			Servers: map[string]*Srv{"server1": {Addr: "127.0.0.1:0"}},
			Pool:    &pool.Config{NumWorkers: 7, AllocateTimeout: time.Second * 3},
		}

		require.NoError(t, cfg.InitDefault())
		require.Equal(t, uint64(7), cfg.Pool.NumWorkers)
		require.Equal(t, time.Second*3, cfg.Pool.AllocateTimeout)
		// the untouched timeouts still get their defaults
		require.Equal(t, time.Minute, cfg.Pool.DestroyTimeout)
	})
}
