//go:build linux

package main

import (
	"encoding/json"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestServerStartupOrdering(t *testing.T) {
	// Exercise the real runServe adapter, real filesystem/security checks, native
	// restore, PID locking, listener binding and readiness descriptor failure.
	dir := t.TempDir()
	options := serveOptions{listen: "127.0.0.1:0", userfile: filepath.Join(dir, "users"), pidfile: filepath.Join(dir, "server.pid"), dumpdir: filepath.Join(dir, "dumps"), logdir: filepath.Join(dir, "logs")}
	t.Setenv("STUNTGRES_READY_FD", "")
	if err := os.Unsetenv("STUNTGRES_READY_FD"); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(options.userfile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	assertNoStartedOrStopped := func() {
		t.Helper()
		data, err := os.ReadFile(filepath.Join(options.logdir, "server.log"))
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), "server started") || strings.Contains(string(data), "server stopped") {
			t.Fatal(string(data))
		}
	}
	t.Run("log opening precedes account load", func(t *testing.T) {
		broken := options
		broken.userfile = filepath.Join(dir, "missing-users")
		broken.logdir = options.userfile
		if err := runServe(broken); err == nil || !strings.Contains(err.Error(), broken.logdir) || strings.Contains(err.Error(), "missing-users") {
			t.Fatal(err)
		}
	})
	owner, err := acquirePID(options.pidfile)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = owner.Close() })
	t.Run("accounts precede PID", func(t *testing.T) {
		broken := options
		broken.userfile = filepath.Join(dir, "missing-users")
		if err := runServe(broken); err == nil || !strings.Contains(err.Error(), "missing-users") || strings.Contains(err.Error(), "already held") {
			t.Fatal(err)
		}
		assertNoStartedOrStopped()
	})
	t.Run("restore precedes PID", func(t *testing.T) {
		if err := os.MkdirAll(options.dumpdir, 0700); err != nil {
			t.Fatal(err)
		}
		path := filepath.Join(options.dumpdir, "app--20260101000000.sql")
		if err := os.WriteFile(path, []byte("not a native dump"), 0600); err != nil {
			t.Fatal(err)
		}
		broken := options
		broken.restore = true
		if err := runServe(broken); err == nil || !strings.Contains(err.Error(), "restore") || strings.Contains(err.Error(), "already held") {
			t.Fatal(err)
		}
		assertNoStartedOrStopped()
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	})
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	t.Run("PID precedes listen", func(t *testing.T) {
		broken := options
		broken.listen = listener.Addr().String()
		if err := runServe(broken); err == nil || !strings.Contains(err.Error(), "already held") {
			t.Fatal(err)
		}
		assertNoStartedOrStopped()
	})
	if err := owner.Close(); err != nil {
		t.Fatal(err)
	}
	t.Run("listen failure releases PID without finalization", func(t *testing.T) {
		broken := options
		broken.listen = listener.Addr().String()
		if err := runServe(broken); err == nil || !strings.Contains(err.Error(), "address already in use") {
			t.Fatal(err)
		}
		if _, err := os.Stat(options.pidfile); !os.IsNotExist(err) {
			t.Fatal("failed listener retained PID", err)
		}
		assertNoStartedOrStopped()
	})
	t.Run("started record precedes readiness and abort releases ownership", func(t *testing.T) {
		t.Setenv("STUNTGRES_READY_FD", "99")
		if err := runServe(options); err == nil {
			t.Fatal("invalid readiness descriptor accepted")
		}
		if _, err := os.Stat(options.pidfile); !os.IsNotExist(err) {
			t.Fatal("readiness failure retained PID", err)
		}
		data, err := os.ReadFile(filepath.Join(options.logdir, "server.log"))
		if err != nil {
			t.Fatal(err)
		}
		address := ""
		for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
			var record struct {
				Message string `json:"message"`
				Address string `json:"address"`
			}
			if err := json.Unmarshal([]byte(line), &record); err != nil {
				t.Fatal(err)
			}
			if record.Message == "server started" {
				address = record.Address
			}
			if record.Message == "server stopped" {
				t.Fatal("readiness failure used graceful finalization")
			}
		}
		if address == "" {
			t.Fatal("readiness attempted before started record")
		}
		if conn, err := net.Dial("tcp", address); err == nil {
			conn.Close()
			t.Fatal("readiness failure retained listener")
		}
		entries, err := os.ReadDir(options.dumpdir)
		if err != nil || len(entries) != 0 {
			t.Fatal("readiness failure published final dump", entries, err)
		}
		recovered, err := acquirePID(options.pidfile)
		if err != nil {
			t.Fatal(err)
		}
		if err := recovered.Close(); err != nil {
			t.Fatal(err)
		}
	})
}
