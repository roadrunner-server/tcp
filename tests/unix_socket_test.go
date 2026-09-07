//go:build linux || darwin || freebsd

package tests

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"syscall"
	"testing"
	"time"

	"tests/helpers"

	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/logger/v6"
	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestTCPUnixSocketConfig(t *testing.T) {
	cases := []struct {
		name    string
		addr    string
		options string
		wantErr string
	}{
		{name: "TCP defaults", addr: "127.0.0.1:0"},
		{name: "UNIX defaults", addr: "unix://test.sock"},
		{name: "empty options", addr: "unix://test.sock", options: "{}"},
		{name: "mode only", addr: "unix://test.sock", options: `{mode: "0600"}`},
		{name: "explicit zero", addr: "unix://test.sock", options: `{mode: "0000", uid: 0, gid: 0}`},
		{name: "unset mode", addr: "unix://test.sock", options: "{uid: 0, gid: 0}"},
		{name: "TCP empty options", addr: "127.0.0.1:0", options: "{}"},
		{name: "TCP options", addr: "127.0.0.1:0", options: `{mode: "0600"}`, wantErr: "filesystem unix:// address"},
		{name: "unquoted mode", addr: "unix://test.sock", options: "{mode: 0660}", wantErr: "invalid unix socket mode"},
		{name: "scalar options", addr: "unix://test.sock", options: "false", wantErr: "unix_socket"},
		{name: "negative UID", addr: "unix://test.sock", options: "{uid: -1}", wantErr: "invalid unix socket uid"},
		{name: "negative GID", addr: "unix://test.sock", options: "{gid: -1}", wantErr: "invalid unix socket gid"},
		{name: "reserved UID", addr: "unix://test.sock", options: "{uid: 4294967295}", wantErr: "invalid unix socket uid"},
		{name: "reserved GID", addr: "unix://test.sock", options: "{gid: 4294967295}", wantErr: "invalid unix socket gid"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".rr.yaml")
			data := fmt.Sprintf(`version: "3"
tcp:
  servers:
    local:
      addr: %q
`, tc.addr)
			if tc.options != "" {
				data += "      unix_socket: " + tc.options + "\n"
			}
			require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
			cfg := &config.Plugin{Path: path}
			require.NoError(t, cfg.Init())
			log := &logger.Plugin{}
			require.NoError(t, log.Init(cfg))
			p := &tcp.Plugin{}
			err := p.Init(log.ServiceLogger(), cfg, nil)
			if tc.wantErr != "" {
				require.ErrorContains(t, err, tc.wantErr)
				require.ErrorContains(t, err, "local")
				return
			}
			require.NoError(t, err)
		})
	}
}

func TestTCPUnixSocketServers(t *testing.T) {
	worker, err := filepath.Abs("php_test_files/psr-worker-tcp.php")
	require.NoError(t, err)
	t.Chdir(t.TempDir())
	uid, gid := os.Getuid(), os.Getgid()
	t.Setenv("RR_TEST_SOCKET_UID", strconv.Itoa(uid))
	t.Setenv("RR_TEST_SOCKET_GID", strconv.Itoa(gid))
	data := fmt.Sprintf(`
version: "3"
server:
  command: [php, %q]
tcp:
  servers:
    first:
      addr: unix://first.sock
      unix_socket: {mode: "0600", uid: "${RR_TEST_SOCKET_UID}", gid: "${RR_TEST_SOCKET_GID}"}
    second:
      addr: unix://second.sock
      unix_socket: {mode: "0640", uid: %d, gid: %d}
      delimiter: "\n"
  pool:
    num_workers: 1
    destroy_timeout: 5s
`, worker, uid, gid)
	require.NoError(t, os.WriteFile(".rr.yaml", []byte(data), 0o600))
	t.Run("serve", func(t *testing.T) {
		helpers.Start(t, ".rr.yaml", []any{
			&server.Plugin{},
			&tcp.Plugin{},
		})

		servers := []struct {
			name    string
			mode    os.FileMode
			message string
		}{
			{name: "first", mode: 0o600, message: "first\r\n"},
			{name: "second", mode: 0o640, message: "second\n"},
		}

		for _, srv := range servers {
			t.Run(srv.name, func(t *testing.T) {
				path := srv.name + ".sock"
				d := net.Dialer{Timeout: time.Second}
				var conn net.Conn
				require.Eventually(t, func() bool {
					var errD error
					conn, errD = d.DialContext(t.Context(), "unix", path)
					return errD == nil
				}, 5*time.Second, 10*time.Millisecond, "listener %s", srv.name)
				t.Cleanup(func() { _ = conn.Close() })

				connected := helpers.ReadResponse(t, conn)
				require.Equal(t, "CONNECTED", connected.Event)
				require.Equal(t, srv.name, connected.Server)

				info, errS := os.Stat(path)
				require.NoError(t, errS)
				require.NotZero(t, info.Mode()&os.ModeSocket)
				require.Equal(t, srv.mode, info.Mode().Perm())
				stat := info.Sys().(*syscall.Stat_t)
				require.EqualValues(t, uid, stat.Uid)
				require.EqualValues(t, gid, stat.Gid)

				data := helpers.WriteRead(t, conn, srv.message)
				require.Equal(t, "DATA", data.Event)
				require.Equal(t, srv.name, data.Server)
				require.Equal(t, srv.message, data.Body)
			})
		}
	})

	for _, path := range []string{"first.sock", "second.sock"} {
		_, err = os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}

func TestTCPUnixSocketOwnershipError(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("Requires an unprivileged process.")
	}

	worker, err := filepath.Abs("php_test_files/psr-worker-tcp.php")
	require.NoError(t, err)
	groups, err := os.Getgroups()
	require.NoError(t, err)
	otherGID := 0
	for otherGID == os.Getegid() || slices.Contains(groups, otherGID) {
		otherGID++
	}

	cases := []struct {
		field string
		id    int
	}{
		{field: "uid", id: 0},
		{field: "gid", id: otherGID},
	}

	for _, tc := range cases {
		t.Run(tc.field, func(t *testing.T) {
			t.Chdir(t.TempDir())
			t.Setenv("RR_TEST_SOCKET_ID", strconv.Itoa(tc.id))
			data := fmt.Sprintf(`version: "3"
server:
  command: [php, %q]
tcp:
  servers:
    local:
      addr: unix://ownership.sock
      unix_socket: {%s: "${RR_TEST_SOCKET_ID}"}
  pool:
    num_workers: 1
    destroy_timeout: 5s
`, worker, tc.field)
			require.NoError(t, os.WriteFile(".rr.yaml", []byte(data), 0o600))
			err := helpers.StartExpectRuntimeError(t, ".rr.yaml", []any{&server.Plugin{}, &tcp.Plugin{}})
			require.ErrorContains(t, err, "chown unix socket")
			_, err = os.Stat("ownership.sock")
			require.ErrorIs(t, err, os.ErrNotExist)
		})
	}
}
