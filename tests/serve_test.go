package tests

import (
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestTCPMultipleServers(t *testing.T) {
	helpers.Start(t, "configs/.rr-tcp-init.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7777"))

	servers := []struct {
		name    string
		addr    string
		payload string
	}{
		{name: "server1", addr: "127.0.0.1:7777", payload: "wuzaaaa\r\n"},
		{name: "server2", addr: "127.0.0.1:8889", payload: "helooooo\r\n"},
		{name: "server3", addr: "127.0.0.1:8810", payload: "HEEEEEEEEEEEEEYYYYYYYYYYYYY\r\n"},
	}

	// every server binds in its own goroutine, the probe only proves the first one
	for _, srv := range servers {
		helpers.WaitListener(t, srv.addr)
	}

	for _, srv := range servers {
		t.Run(srv.name, func(t *testing.T) {
			conn := helpers.Dial(t, srv.addr)

			connected := helpers.ReadResponse(t, conn)
			require.Equal(t, "CONNECTED", connected.Event)
			require.Equal(t, srv.name, connected.Server)
			require.Equal(t, conn.LocalAddr().String(), connected.RemoteAddr)
			require.NotEmpty(t, connected.UUID)

			// the handler forwards the read bytes verbatim, delimiter included
			data := helpers.WriteRead(t, conn, srv.payload)
			require.Equal(t, "DATA", data.Event)
			require.Equal(t, srv.payload, data.Body)
			require.Equal(t, connected.UUID, data.UUID)
			require.Equal(t, srv.name, data.Server)
		})
	}
}

func TestTCPConnectedEvent(t *testing.T) {
	helpers.Start(t, "configs/.rr-tcp-empty.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7779"))

	conn := helpers.Dial(t, "127.0.0.1:7779")

	// the worker is invoked on connect, before the client has sent anything
	connected := helpers.ReadResponse(t, conn)
	require.Equal(t, "CONNECTED", connected.Event)
	require.Equal(t, "tcp_access_point_1", connected.Server)
	require.Equal(t, conn.LocalAddr().String(), connected.RemoteAddr)
	require.NotEmpty(t, connected.UUID)
	require.Empty(t, connected.Body)
}
