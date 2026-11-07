package pgserver

import (
	"context"
	"errors"
	"io"
	"net"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"sync"
	"testing"
	"time"
)

func TestEmbeddedServerPipe(t *testing.T) {
	t.Run("arbitrary route and shutdown EOF", func(t *testing.T) {
		s := embeddedStart(t, Config{})
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if c, err := s.DialFunc()(ctx, "wrong", "anything"); !errors.Is(err, context.Canceled) || c != nil {
			t.Fatal(c, err)
		}
		live, cancelLive := context.WithCancel(context.Background())
		c, err := s.DialFunc()(live, "pipe", "pipe")
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		cancelLive()
		if c.RemoteAddr().Network() != "pipe" {
			t.Fatal(c.RemoteAddr())
		}
		if err := c.SetDeadline(time.Now().Add(testLiveness)); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Write(appendInt32(appendInt32(nil, 8), sslRequestCode)); err != nil {
			t.Fatal("dial context closed a successful connection", err)
		}
		var refusal [1]byte
		if _, err := io.ReadFull(c, refusal[:]); err != nil || refusal[0] != 'N' {
			t.Fatal(refusal, err)
		}
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := c.Read(make([]byte, 1)); err == nil {
			t.Fatal("transport survived shutdown")
		}
		if c, err := s.DialFunc()(context.Background(), "arbitrary", "route"); !errors.Is(err, net.ErrClosed) || c != nil {
			t.Fatal(c, err)
		}
		embeddedJoined(t, s)
	})
	t.Run("concurrent admission and shutdown joins every client", func(t *testing.T) {
		s := embeddedStart(t, Config{Listen: "127.0.0.1:0"})
		start := make(chan struct{})
		var workers sync.WaitGroup
		results := make(chan error, 32)
		for range 32 {
			workers.Add(1)
			go func() {
				defer workers.Done()
				<-start
				c, err := s.DialFunc()(context.Background(), "anything", "anything")
				if c != nil {
					_ = c.Close()
				}
				if err != nil && !errors.Is(err, net.ErrClosed) {
					results <- err
				}
			}()
		}
		joined := make(chan struct{})
		go func() { workers.Wait(); close(joined) }()
		t.Cleanup(func() { _ = s.Close(); embeddedWait(t, joined) })
		close(start)
		if err := s.Shutdown(context.Background()); err != nil {
			t.Fatal(err)
		}
		embeddedWait(t, joined)
		close(results)
		for err := range results {
			t.Error(err)
		}
		embeddedJoined(t, s)
	})
	t.Run("one shot passive watcher", func(t *testing.T) {
		local, remote := newPipe()
		defer local.Close()
		defer remote.Close()
		observed := make(chan struct{})
		stop, err := watchPeerShutdown(remote, func() { close(observed) }, func(err error) { t.Error(err) })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(stop)
		_ = local.Close()
		_ = local.Close()
		embeddedWait(t, observed)
		stop()
		stop()
		if _, err := remote.Read(make([]byte, 1)); !errors.Is(err, io.EOF) {
			t.Fatal(err)
		}
		rawA, rawB := net.Pipe()
		defer rawA.Close()
		defer rawB.Close()
		if stop, err := watchPeerShutdown(rawA, func() {}, func(error) {}); !errors.Is(err, errPeerWatchUnsupported) {
			if stop != nil {
				stop()
			}
			t.Fatal(err)
		}
	})
	for _, terminate := range []bool{false, true} {
		name := "backpressure disconnect cancels owner"
		if terminate {
			name = "decoded Terminate wins peer closure"
		}
		t.Run(name, func(t *testing.T) {
			s := embeddedStart(t, Config{})
			local, remote := newPipe()
			defer local.Close()
			defer remote.Close()
			c, err := s.core.newClient(remote)
			if err != nil {
				t.Fatal(err)
			}
			c.session = enginetest.StartSession(t, engine.New(), nil)
			c.stopPeerWatch, err = watchPeerShutdown(remote, func() { c.cancel(); _ = remote.Close() }, func(err error) { t.Error(err) })
			if err != nil {
				t.Fatal(err)
			}
			c.messages = c.startReader()
			c.sessionEnded = c.watchSessionEnd()
			finished := false
			t.Cleanup(func() {
				if !finished {
					_ = local.Close()
					c.finish(session.InvalidConnection)
				}
			})
			command, finish := c.beginCommand()
			defer finish()
			writeDone := make(chan error, 1)
			writeJoined := make(chan struct{})
			t.Cleanup(func() { _ = local.Close(); embeddedWait(t, writeJoined) })
			go func() {
				defer close(writeJoined)
				packet := framingPacket('H', 4, nil)
				if terminate {
					packet = framingPacket('X', 4, nil)
				} else {
					packet = append(packet, packet...)
				}
				_, err := local.Write(packet)
				writeDone <- err
			}()
			if err := embeddedResult(t, writeDone); err != nil {
				t.Fatal(err)
			}
			if terminate {
				embeddedWait(t, c.readDone)
			} else {
				embeddedUntil(t, func() bool { return len(c.messages) == 1 && len(c.freeBuffers) == 0 })
			}
			_ = local.Close()
			embeddedWait(t, command.Done())
			c.finish(session.InvalidConnection)
			finished = true
			if terminate {
				c.session.Mu.Lock()
				cause := c.session.DisconnectCause
				c.session.Mu.Unlock()
				if cause != session.DisconnectNormal {
					t.Fatal("peer watcher overrode decoded Terminate", cause)
				}
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			embeddedJoined(t, s)
		})
	}
}
