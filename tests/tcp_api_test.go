package tcp

import (
	"bytes"
	"context"
	"crypto/tls"
	"io"
	"log/slog"
	"net"
	"net/http"
	"sync"
	"testing"
	"time"

	"connectrpc.com/connect"
	tcpV1 "github.com/roadrunner-server/api-go/v6/tcp/v1"
	"github.com/roadrunner-server/api-go/v6/tcp/v1/tcpV1connect"
	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/logger/v6"
	rpcPlugin "github.com/roadrunner-server/rpc/v6"
	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
	"golang.org/x/net/http2"
	"google.golang.org/grpc"
	"google.golang.org/grpc/credentials/insecure"
	"google.golang.org/protobuf/encoding/protojson"
)

const tcpAPIAddr = "127.0.0.1:6001"

// startTCPAPIContainer brings up rpc + server + tcp + logger on tcpAPIAddr.
// Close on a non-existent UUID is a no-op on the plugin side, so the wire
// surface alone is enough to validate each protocol.
func startTCPAPIContainer(t *testing.T) func() {
	t.Helper()

	cont := endure.New(slog.LevelError)
	cfg := &config.Plugin{
		Version: "2024.2.0",
		Path:    "configs/.rr-tcp-api.yaml",
	}

	require.NoError(t, cont.RegisterAll(
		cfg,
		&logger.Plugin{},
		&server.Plugin{},
		&rpcPlugin.Plugin{},
		&tcp.Plugin{},
	))
	require.NoError(t, cont.Init())

	ch, err := cont.Serve()
	require.NoError(t, err)

	wg := &sync.WaitGroup{}
	stop := make(chan struct{})
	wg.Go(func() {
		select {
		case e := <-ch:
			t.Errorf("container reported error: %v", e.Error)
		case <-stop:
		}
	})

	time.Sleep(500 * time.Millisecond)

	return func() {
		close(stop)
		require.NoError(t, cont.Stop())
		wg.Wait()
	}
}

func TestTCPConnectAPI(t *testing.T) {
	stop := startTCPAPIContainer(t)
	defer stop()

	httpc := &http.Client{
		Timeout: 30 * time.Second,
		Transport: &http2.Transport{
			AllowHTTP: true,
			DialTLSContext: func(ctx context.Context, network, addr string, _ *tls.Config) (net.Conn, error) {
				return (&net.Dialer{Timeout: 30 * time.Second}).DialContext(ctx, network, addr)
			},
		},
	}
	client := tcpV1connect.NewTCPServiceClient(httpc, "http://"+tcpAPIAddr)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	resp, err := client.Close(ctx, connect.NewRequest(&tcpV1.CloseRequest{Uuid: "unknown-uuid"}))
	require.NoError(t, err)
	require.True(t, resp.Msg.GetOk())
}

// TestTCPHTTPApi exercises tcp.Close through plain HTTP/1.1 with a protojson
// body — the shape any non-Connect HTTP client uses against this handler.
func TestTCPHTTPApi(t *testing.T) {
	stop := startTCPAPIContainer(t)
	defer stop()

	httpc := &http.Client{Timeout: 30 * time.Second}
	ctx := t.Context()

	body, err := protojson.Marshal(&tcpV1.CloseRequest{Uuid: "unknown-uuid"})
	require.NoError(t, err)

	req, err := http.NewRequestWithContext(ctx, http.MethodPost,
		"http://"+tcpAPIAddr+"/tcp.v1.TCPService/Close", bytes.NewReader(body))
	require.NoError(t, err)
	req.Header.Set("Content-Type", "application/json")

	resp, err := httpc.Do(req)
	require.NoError(t, err)
	defer func() { _ = resp.Body.Close() }()

	respBody, err := io.ReadAll(resp.Body)
	require.NoError(t, err)
	require.Equalf(t, http.StatusOK, resp.StatusCode, "body=%s", respBody)

	var out tcpV1.Response
	require.NoError(t, protojson.Unmarshal(respBody, &out))
	require.True(t, out.GetOk())
}

// TestTCPGRPCApi exercises tcp.Close through a regular gRPC client. The same
// Connect handler serves gRPC framing off the same port — used by PHP's gRPC
// extension.
func TestTCPGRPCApi(t *testing.T) {
	stop := startTCPAPIContainer(t)
	defer stop()

	conn, err := grpc.NewClient(tcpAPIAddr, grpc.WithTransportCredentials(insecure.NewCredentials()))
	require.NoError(t, err)
	defer func() { _ = conn.Close() }()

	client := tcpV1.NewTCPServiceClient(conn)
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
	defer cancel()

	resp, err := client.Close(ctx, &tcpV1.CloseRequest{Uuid: "unknown-uuid"})
	require.NoError(t, err)
	require.True(t, resp.GetOk())
}
