//go:build linux || darwin || freebsd

package tests

import (
	"fmt"
	"log/slog"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"syscall"
	"testing"
	"time"

	"tests/helpers"

	"github.com/roadrunner-server/config/v6"
	"github.com/roadrunner-server/endure/v2"
	"github.com/roadrunner-server/logger/v6"
	"github.com/roadrunner-server/server/v6"
	"github.com/roadrunner-server/tcp/v6"
	"github.com/stretchr/testify/require"
)

func TestUnixSocketConfig(t *testing.T) {
	for _, tc := range []struct {
		name    string
		addr    string
		options string
		mode    string
		zeroIDs bool
		invalid bool
	}{
		{name: "TCP defaults", addr: "127.0.0.1:0"},
		{name: "UNIX defaults", addr: "unix://test.sock"},
		{name: "empty options", addr: "unix://test.sock", options: "{}"},
		{name: "mode only", addr: "unix://test.sock", options: `{mode: "0600"}`, mode: "0600"},
		{name: "explicit zero", addr: "unix://test.sock", options: `{mode: "0000", uid: 0, gid: 0}`, mode: "0000", zeroIDs: true},
		{name: "unset mode", addr: "unix://test.sock", options: "{uid: 0, gid: 0}", zeroIDs: true},
		{name: "TCP options", addr: "127.0.0.1:0", options: "{}", invalid: true},
		{name: "unquoted mode", addr: "unix://test.sock", options: "{mode: 0660}", invalid: true},
		{name: "scalar options", addr: "unix://test.sock", options: "false", invalid: true},
		{name: "unsigned UID out of range", addr: "unix://test.sock", options: "{uid: 18446744073709551615}", invalid: true},
		{name: "unsigned GID out of range", addr: "unix://test.sock", options: "{gid: 18446744073709551615}", invalid: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), ".rr.yaml")
			data := fmt.Sprintf("version: '3'\ntcp:\n  servers:\n    local:\n      addr: %q\n", tc.addr)
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
			if tc.invalid {
				require.ErrorContains(t, err, "tcp.servers.local.unix_socket")
				return
			}
			require.NoError(t, err)
			var decoded tcp.Config
			require.NoError(t, cfg.UnmarshalKey("tcp", &decoded))
			if tc.options == "{}" {
				require.True(t, cfg.Has("tcp.servers.local.unix_socket"))
				require.Nil(t, decoded.Servers["local"].UnixSocket)
				return
			}
			require.NoError(t, decoded.InitDefault())
			options := decoded.Servers["local"].UnixSocket
			if tc.options == "" {
				require.Nil(t, options)
				return
			}
			require.NotNil(t, options)
			require.Equal(t, tc.mode, options.Mode)
			if tc.zeroIDs {
				require.NotNil(t, options.UID)
				require.NotNil(t, options.GID)
				require.Zero(t, *options.UID)
				require.Zero(t, *options.GID)
			} else {
				require.Nil(t, options.UID)
				require.Nil(t, options.GID)
			}
		})
	}
}

func TestUnixSocketRawIDs(t *testing.T) {
	for _, field := range []string{"uid", "gid"} {
		for _, tc := range []struct {
			value   string
			env     string
			want    int
			invalid bool
		}{
			{value: "null"},
			{value: "0"},
			{value: "33.0", want: 33},
			{value: `"0x21"`, want: 33},
			{value: `"${RR_TEST_SOCKET_ID}"`, env: "0"},
			{value: `"${RR_TEST_SOCKET_ID}"`, env: "33", want: 33},
			{value: `"${RR_TEST_SOCKET_ID}"`, invalid: true},
			{value: `""`, invalid: true},
			{value: "1.9", invalid: true},
			{value: "-0.5", invalid: true},
			{value: "true", invalid: true},
			{value: "false", invalid: true},
			{value: "-1", invalid: true},
			{value: "4294967295", invalid: true},
			{value: `"4294967295"`, invalid: true},
			{value: "[]", invalid: true},
			{value: "{}", invalid: true},
		} {
			t.Run(field+"/"+tc.value+"/"+tc.env, func(t *testing.T) {
				t.Setenv("RR_TEST_SOCKET_ID", tc.env)
				if tc.env == "" {
					require.NoError(t, os.Unsetenv("RR_TEST_SOCKET_ID"))
				}
				dir := t.TempDir()
				socket := filepath.Join(dir, "local.sock")
				path := filepath.Join(dir, ".rr.json")
				data := fmt.Sprintf(`{"version":"3","tcp":{"servers":{
					"other":{"addr":"unix://other.sock","unix_socket":{"uid":7,"gid":8}},
					"local":{"addr":%q,"unix_socket":{%q:%s}},
					"tcp":{"addr":"127.0.0.1:0"}}}}`, "unix://"+socket, field, tc.value)
				require.NoError(t, os.WriteFile(path, []byte(data), 0o600))
				cfg := &config.Plugin{Path: path}
				require.NoError(t, cfg.Init())
				log := &logger.Plugin{}
				require.NoError(t, log.Init(cfg))
				p := &tcp.Plugin{}
				err := p.Init(log.ServiceLogger(), cfg, nil)
				_, statErr := os.Stat(socket)
				require.ErrorIs(t, statErr, os.ErrNotExist)
				if tc.invalid {
					require.ErrorContains(t, err, "tcp.servers.local.unix_socket."+field)
					return
				}
				require.NoError(t, err)
				var decoded tcp.Config
				require.NoError(t, cfg.UnmarshalKey("tcp", &decoded))
				require.Nil(t, decoded.Servers["tcp"].UnixSocket)
				require.Equal(t, 7, *decoded.Servers["other"].UnixSocket.UID)
				require.Equal(t, 8, *decoded.Servers["other"].UnixSocket.GID)
				options := decoded.Servers["local"].UnixSocket
				if tc.value == "null" {
					if options != nil {
						require.Nil(t, options.UID)
						require.Nil(t, options.GID)
					}
					return
				}
				require.NotNil(t, options)
				id, unset := options.UID, options.GID
				if field == "gid" {
					id, unset = options.GID, options.UID
				}
				require.NotNil(t, id)
				require.Equal(t, tc.want, *id)
				require.Nil(t, unset)
			})
		}
	}
}

func TestUnixSocketServers(t *testing.T) {
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
    tcp:
      addr: 127.0.0.1:0
  pool:
    num_workers: 1
    destroy_timeout: 5s
`, worker, uid, gid)
	require.NoError(t, os.WriteFile(".rr.yaml", []byte(data), 0o600))
	cfg := &config.Plugin{Path: ".rr.yaml"}
	cont := endure.New(slog.LevelError)
	require.NoError(t, cont.RegisterAll(cfg, &logger.Plugin{}, &server.Plugin{}, &tcp.Plugin{}))
	require.NoError(t, cont.Init())
	errCh, err := cont.Serve()
	require.NoError(t, err)
	stop := sync.OnceValue(cont.Stop)
	t.Cleanup(func() { require.NoError(t, stop()) })

	for _, srv := range []struct {
		name    string
		mode    os.FileMode
		message string
	}{
		{name: "first", mode: 0o600, message: "first\r\n"},
		{name: "second", mode: 0o640, message: "second\n"},
	} {
		path := srv.name + ".sock"
		require.Eventually(t, func() bool {
			info, errS := os.Stat(path)
			return errS == nil && info.Mode().Perm() == srv.mode
		}, 5*time.Second, 10*time.Millisecond, "listener %s", srv.name)
		info, errS := os.Stat(path)
		require.NoError(t, errS)
		require.NotZero(t, info.Mode()&os.ModeSocket)
		stat := info.Sys().(*syscall.Stat_t)
		require.EqualValues(t, uid, stat.Uid)
		require.EqualValues(t, gid, stat.Gid)

		var d net.Dialer
		conn, errD := d.DialContext(t.Context(), "unix", path)
		require.NoError(t, errD)
		t.Cleanup(func() { _ = conn.Close() })
		connected := helpers.ReadResponse(t, conn)
		require.Equal(t, "CONNECTED", connected.Event)
		require.Equal(t, srv.name, connected.Server)
		data := helpers.WriteRead(t, conn, srv.message)
		require.Equal(t, "DATA", data.Event)
		require.Equal(t, srv.name, data.Server)
		require.Equal(t, srv.message, data.Body)
		require.NoError(t, conn.Close())
	}
	select {
	case result := <-errCh:
		t.Fatalf("serve error: %v", result)
	default:
	}
	require.NoError(t, stop())
	for _, path := range []string{"first.sock", "second.sock"} {
		_, err = os.Stat(path)
		require.ErrorIs(t, err, os.ErrNotExist)
	}
}
