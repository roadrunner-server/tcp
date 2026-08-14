package tests

import (
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

// roundTrips is the number of request/response pairs each connection exchanges.
const roundTrips = 100

func TestTCPContinueLoop(t *testing.T) {
	t.Parallel()

	helpers.Start(t, "configs/.rr-tcp-full.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7778"))

	servers := []struct {
		name       string
		addr       string
		payload    string
		remoteAddr string
	}{
		{name: "server1", addr: "127.0.0.1:7778", payload: "foo \r\n", remoteAddr: "foo1"},
		{name: "server2", addr: "127.0.0.1:8811", payload: "bar \r\n", remoteAddr: "foo2"},
		{name: "server3", addr: "127.0.0.1:8812", payload: "baz \r\n", remoteAddr: "foo3"},
	}

	for _, srv := range servers {
		helpers.WaitListener(t, srv.addr)
	}

	// parallel subtests keep every assertion on the goroutine that owns its
	// testing.T, and the container outlives them all
	for _, srv := range servers {
		t.Run(srv.name, func(t *testing.T) {
			t.Parallel()

			conn := helpers.Dial(t, srv.addr)

			// the worker greets the connection instead of echoing the context
			require.Equal(t, "hello \r\n", string(helpers.ReadRaw(t, conn)))

			for range roundTrips {
				resp := helpers.WriteRead(t, conn, srv.payload)
				require.Equal(t, srv.remoteAddr, resp.RemoteAddr)
				require.Equal(t, srv.payload, resp.Body)
			}
		})
	}
}
