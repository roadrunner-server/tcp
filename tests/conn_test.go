package tests

import (
	"testing"

	"tests/helpers"

	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestTCPCloseViaRPC(t *testing.T) {
	helpers.Start(t, "configs/.rr-tcp-close.yaml", []any{
		&rpcPlugin.Plugin{},
		&server.Plugin{},
		&tcp.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7788"))

	client := helpers.RPC(t, "127.0.0.1:6361")
	conn := helpers.Dial(t, "127.0.0.1:7788")

	connected := helpers.ReadResponse(t, conn)
	require.NotEmpty(t, connected.UUID)

	t.Run("known connection", func(t *testing.T) {
		var closed bool
		require.NoError(t, client.Call("tcp.Close", connected.UUID, &closed))
		require.True(t, closed)

		helpers.RequireClosed(t, conn)
	})

	t.Run("unknown connection", func(t *testing.T) {
		// the rpc contract reports success for a uuid the plugin does not know
		var closed bool
		require.NoError(t, client.Call("tcp.Close", "6b1f5b1e-0000-0000-0000-000000000000", &closed))
		require.True(t, closed)
	})
}

func TestTCPWorkerCloseResponse(t *testing.T) {
	helpers.Start(t, "configs/.rr-tcp-close-response.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7790"))

	conn := helpers.Dial(t, "127.0.0.1:7790")

	// the worker answers the data packet and asks for the connection to be closed
	helpers.Write(t, conn, "bye\r\n")
	require.Equal(t, "goodbye\r\n", string(helpers.ReadRaw(t, conn)))

	helpers.RequireClosed(t, conn)
}

func TestTCPWorkerError(t *testing.T) {
	helpers.Start(t, "configs/.rr-tcp-worker-error.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7791"))

	conn := helpers.Dial(t, "127.0.0.1:7791")

	// a regular packet is answered, so the connection is known to work
	helpers.Write(t, conn, "ping\r\n")
	require.Equal(t, "pong\r\n", string(helpers.ReadRaw(t, conn)))

	// the marker makes the worker report an error; the pool returns it to the
	// plugin and the handler drops the connection without writing anything
	helpers.Write(t, conn, "fail\r\n")
	helpers.RequireClosed(t, conn)
}
