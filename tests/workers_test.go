package tests

import (
	"net/rpc"
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/informer/v6"
	"github.com/roadrunner-server/resetter/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestTCPInformerReset(t *testing.T) {
	helpers.Start(t, "configs/.rr-tcp-workers.yaml", []any{
		&rpcPlugin.Plugin{},
		&server.Plugin{},
		&tcp.Plugin{},
		&informer.Plugin{},
		&resetter.Plugin{},
	}, helpers.WithTCPProbe("127.0.0.1:7789"))

	client := helpers.RPC(t, "127.0.0.1:6362")

	initial := requireWorkers(t, client, 2)
	for _, pid := range initial {
		require.NotZero(t, pid)
	}

	addWorker(t, client)
	requireWorkers(t, client, 3)

	removeWorker(t, client)
	beforeReset := requireWorkers(t, client, 2)

	var services []string
	require.NoError(t, client.Call("resetter.List", nil, &services))
	require.Contains(t, services, "tcp")

	var reset bool
	require.NoError(t, client.Call("resetter.Reset", "tcp", &reset))
	require.True(t, reset)

	// the reset replaces every worker of the pool
	afterReset := requireWorkers(t, client, 2)
	for _, pid := range afterReset {
		require.NotContains(t, beforeReset, pid)
	}

	// the fresh pool still answers
	conn := helpers.Dial(t, "127.0.0.1:7789")
	connected := helpers.ReadResponse(t, conn)
	require.Equal(t, "CONNECTED", connected.Event)
	require.Equal(t, "server1", connected.Server)
}

// requireWorkers requires the tcp pool to hold exactly n workers and returns their pids.
func requireWorkers(t *testing.T, client *rpc.Client, n int) []int64 {
	t.Helper()

	var list helpers.WorkersList
	require.NoError(t, client.Call("informer.Workers", "tcp", &list))
	require.Len(t, list.Workers, n)

	pids := make([]int64, 0, len(list.Workers))
	for _, w := range list.Workers {
		pids = append(pids, w.Pid)
	}

	return pids
}

func addWorker(t *testing.T, client *rpc.Client) {
	t.Helper()

	var ok bool
	require.NoError(t, client.Call("informer.AddWorker", "tcp", &ok))
}

func removeWorker(t *testing.T, client *rpc.Client) {
	t.Helper()

	var ok bool
	require.NoError(t, client.Call("informer.RemoveWorker", "tcp", &ok))
}
