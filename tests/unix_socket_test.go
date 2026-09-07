//go:build linux || darwin || freebsd

package tests

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"tests/helpers"

	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestTCPUnixSocketInitErrors(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		options string
		message string
	}{
		{name: "TCP options", addr: "127.0.0.1:0", options: `{mode: "0600"}`, message: "filesystem unix:// address"},
		{name: "invalid mode", addr: "unix://test.sock", options: `{mode: "600"}`, message: "invalid unix socket mode"},
		{name: "unquoted mode", addr: "unix://test.sock", options: "{mode: 0660}", message: "invalid unix socket mode"},
		{name: "scalar options", addr: "unix://test.sock", options: "false", message: "unix_socket"},
		{name: "negative UID", addr: "unix://test.sock", options: "{uid: -1}", message: "invalid unix socket uid"},
		{name: "negative GID", addr: "unix://test.sock", options: "{gid: -1}", message: "invalid unix socket gid"},
		{name: "reserved UID", addr: "unix://test.sock", options: "{uid: 4294967295}", message: "invalid unix socket uid"},
		{name: "reserved GID", addr: "unix://test.sock", options: "{gid: 4294967295}", message: "invalid unix socket gid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".rr.yaml")
			data := fmt.Sprintf(`version: "3"
server:
  command: "php php_test_files/psr-worker-tcp.php"
tcp:
  servers:
    local:
      addr: %q
      unix_socket: %s
`, tc.addr, tc.options)
			require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
			err := helpers.StartExpectInitError(t, path, []any{
				&server.Plugin{},
				&tcp.Plugin{},
			})

			require.ErrorContains(t, err, tc.message)
		})
	}
}

func TestTCPUnixSocketServers(t *testing.T) {
	helpers.Start(t, unixSocketConfig(t), []any{
		&server.Plugin{},
		&tcp.Plugin{},
	})

	servers := []struct {
		name    string
		payload string
	}{
		{name: "first", payload: "first\r\n"},
		{name: "second", payload: "second\n"},
	}

	for _, srv := range servers {
		t.Run(srv.name, func(t *testing.T) {
			conn := helpers.DialUnix(t, srv.name+".sock")

			connected := helpers.ReadResponse(t, conn)
			require.Equal(t, "CONNECTED", connected.Event)
			require.Equal(t, srv.name, connected.Server)

			data := helpers.WriteRead(t, conn, srv.payload)
			require.Equal(t, "DATA", data.Event)
			require.Equal(t, srv.name, data.Server)
			require.Equal(t, srv.payload, data.Body)
		})
	}
}

func TestTCPUnixSocketPermissions(t *testing.T) {
	helpers.Start(t, unixSocketConfig(t), []any{
		&server.Plugin{},
		&tcp.Plugin{},
	})

	servers := []struct {
		name string
		mode os.FileMode
	}{
		{name: "first", mode: 0o600},
		{name: "second", mode: 0o640},
	}

	for _, srv := range servers {
		t.Run(srv.name, func(t *testing.T) {
			path := srv.name + ".sock"
			conn := helpers.DialUnix(t, path)
			// The worker response confirms that listener setup is complete.
			helpers.ReadResponse(t, conn)

			info, err := os.Stat(path)
			require.NoError(t, err)
			require.Equal(t, srv.mode, info.Mode().Perm())
		})
	}
}

func TestTCPUnixSocketCleanup(t *testing.T) {
	cfgPath := unixSocketConfig(t)
	paths := []string{"first.sock", "second.sock"}
	// Check after container shutdown and before temporary directory removal.
	t.Cleanup(func() {
		for _, path := range paths {
			_, err := os.Stat(path)
			require.ErrorIs(t, err, os.ErrNotExist, path)
		}
	})
	helpers.Start(t, cfgPath, []any{
		&server.Plugin{},
		&tcp.Plugin{},
	})

	for _, path := range paths {
		conn := helpers.DialUnix(t, path)
		helpers.ReadResponse(t, conn)
	}
}

func TestTCPUnixSocketOwnershipError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Requires an unprivileged process.")
	}

	worker, err := filepath.Abs("php_test_files/psr-worker-tcp.php")
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	data := fmt.Sprintf(`version: "3"
server:
  command: [php, %q]
tcp:
  servers:
    local:
      addr: unix://ownership.sock
      unix_socket: {uid: 0}
  pool:
    num_workers: 1
    destroy_timeout: 5s
`, worker)
	require.NoError(t, os.WriteFile(".rr.yaml", []byte(data), 0o600))
	err = helpers.StartExpectRuntimeError(t, ".rr.yaml", []any{
		&server.Plugin{},
		&tcp.Plugin{},
	})

	require.ErrorContains(t, err, "chown unix socket")
	_, err = os.Stat("ownership.sock")
	require.ErrorIs(t, err, os.ErrNotExist)
}

func unixSocketConfig(t *testing.T) string {
	t.Helper()

	worker, err := filepath.Abs("php_test_files/psr-worker-tcp.php")
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	data := fmt.Sprintf(`version: "3"
server:
  command: [php, %q]
tcp:
  servers:
    first:
      addr: unix://first.sock
      unix_socket: {mode: "0600"}
      delimiter: "\r\n"
    second:
      addr: unix://second.sock
      unix_socket: {mode: "0640"}
      delimiter: "\n"
  pool:
    num_workers: 1
    destroy_timeout: 5s
`, worker)
	require.NoError(t, os.WriteFile(".rr.yaml", []byte(data), 0o600))

	return ".rr.yaml"
}
