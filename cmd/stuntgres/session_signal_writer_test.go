package main

import (
	"bytes"
	"context"
	"encoding/binary"
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

// Observation wraps an actual synchronous pipe write; it does not replace I/O.
type signalObservedConn struct {
	net.Conn
	entered chan struct{}
	once    sync.Once
}

func (c *signalObservedConn) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	return c.Conn.Write(p)
}

func signalWriterClient(t *testing.T, socket net.Conn) *client {
	t.Helper()
	s := enginetest.StartSession(t, engine.New(), nil)
	ctx, cancel := context.WithCancel(context.Background())
	c := &client{socket: socket, wire: newWire(socket), ctx: ctx, session: s, server: &server{logs: &serverLogs{ops: logHandler(io.Discard, false), sql: logHandler(io.Discard, false)}}}
	c.sessionEnded = c.watchSessionEnd()
	t.Cleanup(func() { cancel(); <-c.sessionDone })
	return c
}
func signalWriterTerminate(c *client) {
	c.session.Mu.Lock()
	c.session.TerminateLocked()
	c.session.Mu.Unlock()
}
func TestSessionSignalTerminalWriter(t *testing.T) {
	for _, flush := range []bool{false, true} {
		t.Run(map[bool]string{false: "send", true: "flush"}[flush], func(t *testing.T) {
			local, peer := net.Pipe()
			defer local.Close()
			defer peer.Close()
			socket := &signalObservedConn{Conn: local, entered: make(chan struct{})}
			c := signalWriterClient(t, socket)
			held, _, err := c.session.AcquireCommand(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer held.Done()
			done := make(chan error, 1)
			joined := false
			t.Cleanup(func() {
				local.Close()
				peer.Close()
				if !joined {
					<-done
				}
			})
			go func() {
				payload := bytes.Repeat([]byte{'x'}, 8192)
				if flush {
					payload = payload[:1]
				}
				err := c.wire.send('D', payload)
				if err == nil {
					err = c.wire.flush()
				}
				done <- err
			}()
			select {
			case <-socket.entered:
			case <-time.After(2 * time.Second):
				t.Fatal("writer never entered real socket")
			}
			signalWriterTerminate(c)
			select {
			case err := <-done:
				joined = true
				if err == nil {
					t.Fatal("blocked write succeeded without reader")
				}
			case <-time.After(2 * time.Second):
				t.Fatal("termination did not wake socket write")
			}
			select {
			case <-c.session.Completed:
				t.Fatal("terminal write still owned an operation lease")
			default:
			}
			// Clearing the deadline must never clear bufio's latched partial-frame error.
			local.SetWriteDeadline(time.Time{})
			if err := c.wire.flush(); err == nil {
				t.Fatal("broken frame writer was resurrected")
			}
		})
	}
	t.Run("intact_terminal_frame", func(t *testing.T) {
		local, peer := net.Pipe()
		defer local.Close()
		defer peer.Close()
		c := signalWriterClient(t, local)
		reply := make(chan error, 1)
		joined := false
		t.Cleanup(func() {
			local.Close()
			peer.Close()
			if !joined {
				<-reply
			}
		})
		go func() {
			defer local.Close()
			signalWriterTerminate(c)
			fatal := <-c.sessionEnded
			reply <- c.writeContextError(context.Background(), session.ProtocolContext{}, fatal)
		}()
		peer.SetReadDeadline(time.Now().Add(2 * time.Second))
		var header [5]byte
		if _, err := io.ReadFull(peer, header[:]); err != nil {
			t.Fatal(err)
		}
		if header[0] != 'E' {
			t.Fatal(header)
		}
		body := make([]byte, int(binary.BigEndian.Uint32(header[1:]))-4)
		if _, err := io.ReadFull(peer, body); err != nil {
			t.Fatal(err)
		}
		fields := framingFields(t, body)
		if fields['C'] != "57P01" || fields['S'] != "FATAL" || fields['M'] != "terminating connection due to administrator command" {
			t.Fatal(fields)
		}
		if n, err := peer.Read(header[:]); n != 0 || err != io.EOF {
			t.Fatal(n, err)
		}
		err := <-reply
		joined = true
		var fatal interface{ SQLState() string }
		if !errors.As(err, &fatal) || fatal.SQLState() != "57P01" {
			t.Fatal(err)
		}
		c.interruptTerminalWrite() // Late watcher cannot replace the writer's budget.
	})
	t.Run("unread_terminal_peer", func(t *testing.T) {
		local, peer := net.Pipe()
		defer local.Close()
		defer peer.Close()
		c := signalWriterClient(t, local)
		done := make(chan error, 1)
		joined := false
		t.Cleanup(func() {
			local.Close()
			peer.Close()
			if !joined {
				<-done
			}
		})
		go func() {
			signalWriterTerminate(c)
			fatal := <-c.sessionEnded
			done <- c.writeContextError(context.Background(), session.ProtocolContext{}, fatal)
		}()
		select {
		case err := <-done:
			joined = true
			if err == nil {
				t.Fatal("unread pipe accepted FATAL")
			}
		case <-time.After(2 * time.Second):
			t.Fatal("terminal output held physical retirement indefinitely")
		}
	})
}
