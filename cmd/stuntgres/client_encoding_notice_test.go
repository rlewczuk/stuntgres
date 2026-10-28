package main

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

// Observe a real socket Write while the peer deliberately reads nothing. This
// is an I/O barrier, not an alternate notice sender or production test hook.
type clientEncodingNoticeSocket struct {
	net.Conn
	entered chan struct{}
	once    sync.Once
}

func (c *clientEncodingNoticeSocket) Write(p []byte) (int, error) {
	c.once.Do(func() { close(c.entered) })
	return c.Conn.Write(p)
}

func TestClientEncodingNoticeOwnership(t *testing.T) {
	for _, route := range []string{"simple", "extended", "function"} {
		t.Run(route, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE ce_notice_owner(v int)")
			enginetest.SessionSQL(t, s, "SET client_encoding='LATIN1'")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logs := &serverLogs{ops: logHandler(io.Discard, false), sql: logHandler(io.Discard, false)}
			c := &client{server: &server{logs: logs}, db: &database{engine: e}, session: s, ctx: ctx, cycle: s.NewProtocolCycle(), wire: newWire(framingInput(nil)), freeBuffers: make(chan []byte, 4), lastSettings: make(map[string]string)}
			c.copyStream.client = c
			c.copyHooks = core.CopyProtocolHooks{Start: c.copyStream.start, FinishInput: c.copyStream.finishInput}
			c.refreshEncoding()
			prefix := "DO $$ BEGIN INSERT INTO ce_notice_owner VALUES(1); RAISE NOTICE USING MESSAGE='"
			suffix := "'; END $$"
			source := prefix + strings.Repeat("é", 1<<18) + suffix
			var functionBody []byte
			if route == "function" {
				enginetest.SessionSQL(t, s, "CREATE FUNCTION ce_notice_owner_function() RETURNS integer LANGUAGE plpgsql AS $$ BEGIN INSERT INTO ce_notice_owner VALUES(1); RAISE NOTICE USING MESSAGE='"+strings.Repeat("é", 1<<18)+"'; RETURN 1; END $$")
				values := enginetest.Tuples(enginetest.SessionSQL(t, s, "SELECT 'ce_notice_owner_function()'::regprocedure::oid::text"))
				if len(values) != 1 || len(values[0]) != 1 {
					t.Fatal("function OID rows", values)
				}
				oid, err := strconv.ParseUint(fmt.Sprint(values[0][0]), 10, 32)
				if err != nil || oid == 0 {
					t.Fatal("function OID", values, err)
				}
				functionBody = appendInt32(nil, uint32(oid))
				functionBody = appendInt16(functionBody, 0) // no argument format codes
				functionBody = appendInt16(functionBody, 0) // no arguments
				functionBody = appendInt16(functionBody, 1) // binary integer result
			}
			if route == "extended" {
				body := appendString(appendString(nil, "s"), prefix+strings.Repeat("\xe9", 1<<18)+suffix)
				body = appendInt16(body, 0)
				if err := c.extendedCommand(ctx, frontendMessage{tag: 'P', body: body}); err != nil {
					t.Fatal(err)
				}
				body = appendString(appendString(nil, "p"), "s")
				body = append(body, make([]byte, 6)...)
				if err := c.extendedCommand(ctx, frontendMessage{tag: 'B', body: body}); err != nil {
					t.Fatal(err)
				}
			}
			server, peer := net.Pipe()
			barrier := &clientEncodingNoticeSocket{Conn: server, entered: make(chan struct{})}
			c.socket = barrier
			c.wire = newWire(barrier)
			c.refreshEncoding()
			done := make(chan error, 1)
			joined := false
			defer func() {
				cancel()
				peer.Close()
				server.Close()
				if !joined {
					select {
					case <-done:
					case <-time.After(10 * time.Second):
						t.Error("notice worker did not join")
					}
				}
			}()
			go func() {
				if route == "function" {
					done <- c.functionCommand(ctx, &protocolInput{body: functionBody, encoding: c.wire.encoding, ctx: ctx})
				} else if route == "extended" {
					done <- c.extendedCommand(ctx, frontendMessage{tag: 'E', body: appendInt32(appendString(nil, "p"), 0)})
				} else {
					done <- c.executeSimple(ctx, source)
				}
			}()
			select {
			case <-barrier.entered:
			case err := <-done:
				joined = true
				t.Fatal("notice did not reach actual socket Write", err)
			case <-time.After(15 * time.Second):
				t.Fatal("notice socket barrier not reached")
			}
			// Capture each result before disconnecting. Releasing after a failure
			// still joins the worker and prevents cleanup from hiding the finding.
			admissionFree := s.Transactions.Admission.TryLock()
			if admissionFree {
				s.Transactions.Admission.Unlock()
			}
			replacementFree := e.ReplacementMu.TryLock()
			if replacementFree {
				e.ReplacementMu.Unlock()
			}
			engineFree := e.Mu.TryLock()
			if engineFree {
				e.Mu.Unlock()
			}
			cancel()
			peer.Close()
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, io.ErrClosedPipe) {
					t.Error("blocked socket disconnect lost actual transport error", err)
				}
			case <-time.After(10 * time.Second):
				t.Fatal("notice disconnect did not join")
			}
			if !admissionFree || !replacementFree || !engineFree {
				t.Errorf("actual notice Write retained locks: admissionFree=%t replacementFree=%t engineFree=%t", admissionFree, replacementFree, engineFree)
			}
			if !s.Transactions.Admission.TryLock() {
				t.Fatal("terminal notice retained statement admission")
			}
			s.Transactions.Admission.Unlock()
			s.Mu.Lock()
			leases, operations := s.Leases, len(s.Operations)
			s.Mu.Unlock()
			if leases != 0 || operations != 0 || s.Transactions.CopyLease != nil || s.Transactions.CopyDone != nil {
				t.Fatal("terminal notice retained operation/reservation", leases, operations)
			}
		})
	}
}
