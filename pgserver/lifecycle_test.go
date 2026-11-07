package pgserver

import (
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"
)

// A barrier around a real required publication log observes actual dump work;
// it neither replaces the publisher nor adds a production testing callback.
type embeddedDumpWriter struct {
	mu          sync.Mutex
	output      bytes.Buffer
	entered     chan struct{}
	release     chan struct{}
	once        sync.Once
	releaseOnce sync.Once
}

func newEmbeddedDumpWriter(t *testing.T) *embeddedDumpWriter {
	w := &embeddedDumpWriter{entered: make(chan struct{}), release: make(chan struct{})}
	t.Cleanup(w.unblock)
	return w
}
func (w *embeddedDumpWriter) unblock() { w.releaseOnce.Do(func() { close(w.release) }) }
func (w *embeddedDumpWriter) Write(p []byte) (int, error) {
	if bytes.Contains(p, []byte(`"message":"database dumped"`)) {
		w.once.Do(func() { close(w.entered); <-w.release })
	}
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.output.Write(p)
}
func embeddedDatabaseServer(t *testing.T, cfg Config, names ...string) *Server {
	t.Helper()
	s := embeddedOpen(t, cfg)
	if writer, ok := cfg.LogWriter.(*embeddedDumpWriter); ok {
		t.Cleanup(writer.unblock)
	}
	if err := s.Prepare(); err != nil {
		t.Fatal(err)
	}
	databases, err := newDatabases(context.Background(), nil, names)
	if err != nil {
		t.Fatal(err)
	}
	s.databases = databases
	if err := s.Start(); err != nil {
		t.Fatal(err)
	}
	return s
}
func embeddedUntil(t *testing.T, condition func() bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), testLiveness)
	defer cancel()
	for !condition() {
		select {
		case <-ctx.Done():
			t.Fatal("ownership condition not reached")
		default:
			runtime.Gosched()
		}
	}
}
func embeddedDumpFiles(t *testing.T, dir string) []os.DirEntry {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if strings.HasPrefix(entry.Name(), ".stuntgres-dump-") {
			t.Fatal("temporary publication survived")
		}
	}
	return entries
}
func embeddedJoined(t *testing.T, s *Server) {
	t.Helper()
	embeddedWait(t, s.Done())
	embeddedWait(t, s.acceptDone)
	embeddedWait(t, s.watchDone)
	s.core.mu.Lock()
	defer s.core.mu.Unlock()
	if len(s.core.clients) != 0 || len(s.core.backends) != 0 || !s.core.admissionClosed {
		t.Fatal("client/backend ownership survived shutdown")
	}
}

func TestEmbeddedServerLifecycle(t *testing.T) {
	t.Run("concurrent real dumps serialize", func(t *testing.T) {
		dir := t.TempDir()
		s := embeddedDatabaseServer(t, Config{DumpDir: dir}, "app")
		results := make([]<-chan error, 8)
		for i := range results {
			results[i] = embeddedRequest(t, s, func() error { return s.Dump(context.Background()) })
		}
		for _, result := range results {
			if err := embeddedResult(t, result); err != nil {
				t.Fatal(err)
			}
		}
		if got := len(embeddedDumpFiles(t, dir)); got != 8 {
			t.Fatalf("dump count %d, want 8", got)
		}
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if got := len(embeddedDumpFiles(t, dir)); got != 9 {
			t.Fatalf("final dump count %d, want 9", got)
		}
		embeddedJoined(t, s)
		for range 2 {
			if err := s.Shutdown(context.Background()); err != nil {
				t.Fatal(err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
		}
	})
	t.Run("shutdown rejects queued publication", func(t *testing.T) {
		w := newEmbeddedDumpWriter(t)
		dir := t.TempDir()
		s := embeddedDatabaseServer(t, Config{DumpDir: dir, LogWriter: w}, "app")

		first := embeddedRequest(t, s, func() error { return s.Dump(context.Background()) })
		embeddedWait(t, w.entered)
		queued := embeddedRequest(t, s, func() error { return s.Dump(context.Background()) })
		embeddedUntil(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return len(s.dumps) == 1 })
		stopped := embeddedRequest(t, s, func() error { return s.Shutdown(context.Background()) })
		embeddedUntil(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.terminal == terminalGraceful })
		if err := s.Dump(context.Background()); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
		w.unblock()
		if err := embeddedResult(t, first); err != nil {
			t.Fatal(err)
		}
		if err := embeddedResult(t, queued); !errors.Is(err, net.ErrClosed) {
			t.Fatal(err)
		}
		if err := embeddedResult(t, stopped); err != nil {
			t.Fatal(err)
		}
		if len(embeddedDumpFiles(t, dir)) != 2 {
			t.Fatal("queued dump ran after finalization selection")
		}
		embeddedJoined(t, s)
	})
	t.Run("caller cancellation leaves no abandoned completion", func(t *testing.T) {
		w := newEmbeddedDumpWriter(t)
		dir := t.TempDir()
		s := embeddedDatabaseServer(t, Config{DumpDir: dir, LogWriter: w}, "app", "other")
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		result := embeddedRequest(t, s, func() error { return s.Dump(ctx) })
		embeddedWait(t, w.entered)
		cancel()
		if err := embeddedResult(t, result); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		w.unblock()
		// The following request must be processed after the abandoned caller's cycle.
		if err := s.Dump(context.Background()); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if got := len(embeddedDumpFiles(t, dir)); got != 3 {
			t.Fatalf("canceled cycle published second database or blocked next request: %d", got)
		}
		embeddedJoined(t, s)
	})
	t.Run("deadline bounds waiter not cleanup", func(t *testing.T) {
		w := newEmbeddedDumpWriter(t)
		s := embeddedDatabaseServer(t, Config{DumpDir: t.TempDir(), LogWriter: w}, "app")
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := s.Shutdown(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		embeddedWait(t, w.entered)
		select {
		case <-s.Done():
			t.Fatal("cleanup abandoned by canceled waiter")
		default:
		}
		w.unblock()
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		embeddedJoined(t, s)
	})
	t.Run("Close aborts incomplete final dump", func(t *testing.T) {
		w := newEmbeddedDumpWriter(t)
		dir := t.TempDir()
		s := embeddedDatabaseServer(t, Config{DumpDir: dir, LogWriter: w}, "app", "other")
		stopped := embeddedRequest(t, s, func() error { return s.Shutdown(context.Background()) })
		embeddedWait(t, w.entered)
		closed := embeddedRequest(t, s, s.Close)
		embeddedUntil(t, func() bool { s.mu.Lock(); defer s.mu.Unlock(); return s.terminal == terminalAbort })
		w.unblock()
		if err := embeddedResult(t, closed); err != nil {
			t.Fatal(err)
		}
		if err := embeddedResult(t, stopped); err != nil {
			t.Fatal(err)
		}
		if len(embeddedDumpFiles(t, dir)) != 1 {
			t.Fatal("abort published an unstarted database dump")
		}
		w.mu.Lock()
		output := w.output.String()
		w.mu.Unlock()
		if strings.Contains(output, "server stopped") {
			t.Fatal(output)
		}
		embeddedJoined(t, s)
	})
	t.Run("Close skips final persistence", func(t *testing.T) {
		dir := t.TempDir()
		s := embeddedDatabaseServer(t, Config{DumpDir: dir}, "app")
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
		if len(embeddedDumpFiles(t, dir)) != 0 {
			t.Fatal("abort ran final dump")
		}
		embeddedJoined(t, s)
	})
	t.Run("parent cancellation finalizes", func(t *testing.T) {
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		var output bytes.Buffer
		s, err := Start(ctx, Config{Listen: "127.0.0.1:0", LogWriter: &output})
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = s.Close() })
		cancel()
		embeddedJoined(t, s)
		if s.Err() != nil || !strings.Contains(output.String(), "server stopped") {
			t.Fatal(s.Err(), output.String())
		}
	})
	t.Run("dump failure is nonfatal", func(t *testing.T) {
		dir := t.TempDir()
		s := embeddedDatabaseServer(t, Config{DumpDir: dir}, "app")
		if err := os.Remove(dir); err != nil {
			t.Fatal(err)
		}
		if err := s.Dump(context.Background()); err == nil {
			t.Fatal("failed publication reported success")
		}
		select {
		case <-s.Done():
			t.Fatal("dump failure stopped healthy server")
		default:
		}
		if err := s.Close(); err != nil {
			t.Fatal(err)
		}
	})
}

func embeddedResult(t *testing.T, result <-chan error) error {
	t.Helper()
	timer := time.NewTimer(testLiveness)
	defer timer.Stop()
	select {
	case err := <-result:
		return err
	case <-timer.C:
		t.Fatal("owned request did not complete")
		return nil
	}
}

func embeddedRequest(t *testing.T, s *Server, call func() error) <-chan error {
	t.Helper()
	result, done := make(chan error, 1), make(chan struct{})
	go func() { defer close(done); result <- call() }()
	t.Cleanup(func() {
		if writer, ok := s.cfg.LogWriter.(*embeddedDumpWriter); ok {
			writer.unblock()
		}
		_ = s.Close()
		embeddedWait(t, done)
	})
	return result
}
