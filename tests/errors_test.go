package tests

import (
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestTCPInitErrors(t *testing.T) {
	cases := []struct {
		name    string
		cfgPath string
		message string
	}{
		{
			name:    "no servers",
			cfgPath: "configs/.rr-tcp-no-servers.yaml",
			message: "no servers registered",
		},
		{
			name:    "server without address",
			cfgPath: "configs/.rr-tcp-no-addr.yaml",
			message: "empty address for the server: server_without_addr",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := helpers.StartExpectInitError(t, tc.cfgPath, []any{
				&server.Plugin{},
				&tcp.Plugin{},
			})

			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestTCPBadListenAddress(t *testing.T) {
	// the listener is created in a per-server goroutine, so the failure reaches the
	// container after Serve has already returned
	err := helpers.StartExpectRuntimeError(t, "configs/.rr-tcp-bad-addr.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	})

	require.ErrorContains(t, err, "invalid Protocol")
}
