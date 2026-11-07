package pgserver

import (
	"bytes"
	"context"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/serverauth"
	"stuntgres/internal/serverref"
)

func embeddedOpen(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := Open(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func embeddedStart(t *testing.T, cfg Config) *Server {
	t.Helper()
	s, err := Start(context.Background(), cfg)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}
func embeddedWait(t *testing.T, done <-chan struct{}) {
	t.Helper()
	timer := time.NewTimer(testLiveness)
	defer timer.Stop()
	select {
	case <-done:
	case <-timer.C:
		t.Fatal("owned work did not finish")
	}
}

func TestEmbeddedServerConfig(t *testing.T) {
	t.Run("quiet empty sources", func(t *testing.T) {
		for _, accounts := range [][]Account{nil, {}} {
			s := embeddedStart(t, Config{Accounts: accounts})
			if s.Addr() != nil || len(s.users) != 0 || s.core.listener != nil {
				t.Fatal("empty source opened TCP or accounts")
			}
			if _, err := s.DSN("u", "p", "db"); err == nil {
				t.Fatal("pipe-only DSN accepted")
			}
			if s.core.referenceTimeout != serverref.DefaultTimeout {
				t.Fatal(s.core.referenceTimeout)
			}
		}
	})
	t.Run("snapshot and hash", func(t *testing.T) {
		input := []Account{{Username: "alice", Password: "original", Databases: []string{"app"}}}
		s := embeddedOpen(t, Config{Accounts: input})
		input[0].Username = "changed"
		input[0].Password = "changed"
		input[0].Databases[0] = "changed"
		if err := s.Prepare(); err != nil {
			t.Fatal(err)
		}
		a := s.users["alice"]
		if a.Username != "alice" || len(a.Databases) != 1 || a.Databases[0] != "app" || !serverauth.VerifyPassword(a, "original") || s.cfg.Accounts != nil {
			t.Fatal("caller configuration retained or changed")
		}
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("validation failures close ownership", func(t *testing.T) {
		for _, cfg := range []Config{
			{UserFile: "missing", Accounts: []Account{}},
			{UserFile: filepath.Join(t.TempDir(), "missing")},
			{Accounts: []Account{{Username: "", Password: "p", Databases: []string{"app"}}}},
			{Accounts: []Account{{Username: "u", Password: "", Databases: []string{"app"}}}},
			{Accounts: []Account{{Username: "u", Password: "p", Databases: []string{"../app"}}}},
			{Accounts: []Account{{Username: "u", Password: "p", Databases: []string{"app", "app"}}}},
			{Accounts: []Account{{Username: "u", Password: "p", Databases: []string{"app"}}, {Username: "u", Password: "p", Databases: []string{"app"}}}},
			{Restore: true}, {ReferenceTimeout: -time.Second},
			{References: []string{"bad"}}, {References: []string{"app=postgres://u@h"}},
			{References: []string{"absent=postgres://u@h"}, QueryLog: true, QueryLogWriter: io.Discard},
		} {
			s := embeddedOpen(t, cfg)
			if err := s.Prepare(); err == nil {
				t.Fatalf("accepted invalid configuration: %+v", cfg)
			}
			embeddedWait(t, s.Done())
			if s.Err() == nil || !errors.Is(s.Log(context.Background(), slog.LevelInfo, "closed"), os.ErrClosed) {
				t.Fatal("failure did not close logs and retain error")
			}
		}
	})
	t.Run("references copied", func(t *testing.T) {
		specs := []string{"absent=postgres://u@h"}
		s := embeddedOpen(t, Config{References: specs, QueryLog: true, QueryLogWriter: io.Discard})
		specs[0] = "bad"
		if err := s.Prepare(); err == nil || !strings.Contains(err.Error(), "absent") {
			t.Fatalf("reference slice not copied: %v", err)
		}
	})
	t.Run("query destinations", func(t *testing.T) {
		for _, cfg := range []Config{{QueryLog: true}, {QueryLogWriter: io.Discard}, {LogDir: t.TempDir(), QueryLog: true, QueryLogWriter: io.Discard}} {
			if s, err := Open(context.Background(), cfg); err == nil {
				_ = s.Close()
				t.Fatal("invalid query destination accepted")
			}
		}
		for _, cfg := range []Config{{QueryLog: true, QueryLogWriter: io.Discard}, {QueryLog: true, LogDir: t.TempDir()}} {
			s := embeddedStart(t, cfg)
			if s.logs.queries == nil {
				t.Fatal("missing selected query destination")
			}
		}
	})
	t.Run("TCP URL escaping", func(t *testing.T) {
		for _, listen := range []string{"127.0.0.1:0", "[::1]:0"} {
			s := embeddedStart(t, Config{Listen: listen})
			for _, identity := range [][3]string{{"a:@ /?", "p:@/?#", "db/name ?#"}, {"", "", ""}} {
				dsn, err := s.DSN(identity[0], identity[1], identity[2])
				if err != nil {
					t.Fatal(err)
				}
				u, err := url.Parse(dsn)
				if err != nil {
					t.Fatal(err)
				}
				password, _ := u.User.Password()
				if u.Scheme != "postgres" || u.Host != s.Addr().String() || u.User.Username() != identity[0] || password != identity[1] || u.Path != "/"+identity[2] || u.Query().Get("sslmode") != "disable" {
					t.Fatalf("wrong escaped DSN: %q", dsn)
				}
			}
			addr := s.Addr().String()
			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if s.Addr().String() != addr {
				t.Fatal("address changed after shutdown")
			}
			if conn, err := net.Dial("tcp", addr); err == nil {
				conn.Close()
				t.Fatal("listener survived shutdown")
			}
		}
	})
}

func TestEmbeddedServerStartup(t *testing.T) {
	t.Run("stages and external boundary", func(t *testing.T) {
		var output bytes.Buffer
		dir := t.TempDir()
		s := embeddedOpen(t, Config{Listen: "127.0.0.1:0", DumpDir: dir, LogWriter: &output})
		if s.core != nil || len(s.databases) != 0 || output.Len() != 0 {
			t.Fatal("Open prepared resources")
		}
		if err := s.Start(); err == nil {
			t.Fatal("Start before Prepare accepted")
		}
		if err := s.Dump(context.Background()); err == nil {
			t.Fatal("Dump before Start accepted")
		}
		if err := s.Prepare(); err != nil {
			t.Fatal(err)
		}
		if s.core != nil || s.Addr() != nil || output.Len() != 0 {
			t.Fatal("Prepare admitted or logged startup")
		}
		if err := s.Prepare(); err == nil {
			t.Fatal("repeated Prepare accepted")
		}
		if err := s.Log(context.Background(), slog.LevelWarn, "external PID boundary"); err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); err == nil {
			t.Fatal("repeated Start accepted")
		}
		if !strings.Contains(output.String(), "external PID boundary") || strings.Index(output.String(), "external PID boundary") > strings.Index(output.String(), "server started") {
			t.Fatal(output.String())
		}
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
	})
	t.Run("close before Start has no final effects", func(t *testing.T) {
		for _, prepare := range []bool{false, true} {
			var output bytes.Buffer
			dir := t.TempDir()
			s := embeddedOpen(t, Config{DumpDir: dir, LogWriter: &output})
			if prepare {
				if err := s.Prepare(); err != nil {
					t.Fatal(err)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			if output.Len() != 0 {
				t.Fatal(output.String())
			}
			entries, err := os.ReadDir(dir)
			if err != nil || len(entries) != 0 {
				t.Fatal(entries, err)
			}
			embeddedWait(t, s.Done())
		}
	})
	t.Run("restore precedes listen", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		dir := t.TempDir()
		if err := os.WriteFile(filepath.Join(dir, "app--20260101000000.sql"), []byte("not a native dump"), 0600); err != nil {
			t.Fatal(err)
		}
		var output bytes.Buffer
		s := embeddedOpen(t, Config{Listen: listener.Addr().String(), Restore: true, DumpDir: dir, LogWriter: &output})
		if err := s.Prepare(); err == nil || !strings.Contains(err.Error(), "restore") {
			t.Fatal(err)
		}
		if s.core != nil || strings.Contains(output.String(), "server started") || strings.Contains(output.String(), "server stopped") {
			t.Fatal(output.String())
		}
	})
	t.Run("log open failure wins", func(t *testing.T) {
		path := filepath.Join(t.TempDir(), "file")
		if err := os.WriteFile(path, nil, 0600); err != nil {
			t.Fatal(err)
		}
		if _, err := Open(context.Background(), Config{LogDir: path, UserFile: "absent"}); err == nil || !strings.Contains(err.Error(), path) {
			t.Fatal(err)
		}
	})
	t.Run("started log failure releases transport", func(t *testing.T) {
		failure := errors.New("startup writer failed")
		s := embeddedOpen(t, Config{Listen: "127.0.0.1:0", LogWriter: loggingFailedWriter{failure}})
		if err := s.Prepare(); err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		embeddedWait(t, s.Done())
		if !errors.Is(s.Err(), failure) {
			t.Fatal(s.Err())
		}
		if conn, err := net.Dial("tcp", s.Addr().String()); err == nil {
			conn.Close()
			t.Fatal("failed startup listener survived")
		}
	})
	t.Run("required runtime log failure finalizes", func(t *testing.T) {
		writer := &embeddedSwitchWriter{}
		s := embeddedStart(t, Config{LogWriter: writer})
		failure := errors.New("runtime writer failed")
		writer.set(failure)
		if err := s.Log(context.Background(), slog.LevelInfo, "required"); !errors.Is(err, failure) {
			t.Fatal(err)
		}
		embeddedWait(t, s.Done())
		if !errors.Is(s.Err(), failure) {
			t.Fatal(s.Err())
		}
	})
	t.Run("unexpected closed accept is retained", func(t *testing.T) {
		s := embeddedStart(t, Config{Listen: "127.0.0.1:0"})
		if err := s.core.listener.Close(); err != nil {
			t.Fatal(err)
		}
		embeddedWait(t, s.Done())
		if !errors.Is(s.Err(), net.ErrClosed) {
			t.Fatal("unexpected accept failure was suppressed", s.Err())
		}
	})
}

type embeddedSwitchWriter struct {
	mu     sync.Mutex
	err    error
	output bytes.Buffer
	closes int
}

func (w *embeddedSwitchWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.err != nil {
		return 0, w.err
	}
	return w.output.Write(p)
}
func (w *embeddedSwitchWriter) set(err error) { w.mu.Lock(); w.err = err; w.mu.Unlock() }
func (w *embeddedSwitchWriter) Close() error  { w.mu.Lock(); w.closes++; w.mu.Unlock(); return nil }
func (w *embeddedSwitchWriter) text() string {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.String()
}

func TestEmbeddedServerLogs(t *testing.T) {
	t.Run("quiet and independent destinations", func(t *testing.T) {
		quiet := embeddedStart(t, Config{})
		if len(quiet.logs.files) != 0 || quiet.logs.queries != nil {
			t.Fatal("quiet server opened files")
		}
		a, b := &embeddedSwitchWriter{}, &embeddedSwitchWriter{}
		first, second := embeddedStart(t, Config{LogWriter: a}), embeddedStart(t, Config{LogWriter: b})
		if err := first.Log(context.Background(), slog.LevelInfo, "only-first", "identity", 7); err != nil {
			t.Fatal(err)
		}
		if err := second.Log(context.Background(), slog.LevelInfo, "only-second"); err != nil {
			t.Fatal(err)
		}
		for _, s := range []*Server{first, second, quiet} {
			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
		}
		if !strings.Contains(a.text(), `"message":"only-first"`) || !strings.Contains(a.text(), `"identity":7`) || strings.Contains(a.text(), "only-second") || strings.Contains(b.text(), "only-first") || a.closes != 0 || b.closes != 0 {
			t.Fatal("record format, isolation or writer ownership violated")
		}
	})
	t.Run("owned files close once", func(t *testing.T) {
		s := embeddedStart(t, Config{LogDir: t.TempDir(), QueryLog: true})
		files := append([]io.Closer(nil), s.logs.files...)
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		for _, file := range files {
			if _, err := file.(*os.File).Write(nil); !errors.Is(err, os.ErrClosed) {
				t.Fatal("owned file remains open", err)
			}
		}
	})
	t.Run("short writer is fatal", func(t *testing.T) {
		s := embeddedOpen(t, Config{LogWriter: loggingShortWriter{}})
		if err := s.Prepare(); err != nil {
			t.Fatal(err)
		}
		if err := s.Start(); !errors.Is(err, io.ErrShortWrite) {
			t.Fatal(err)
		}
	})
}
