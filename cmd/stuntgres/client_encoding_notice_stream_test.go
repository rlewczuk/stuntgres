package main

import (
	"context"
	"errors"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
)

// A top-level DO block sends each RAISE's notice while it still runs, as
// PostgreSQL does. The block then sleeps: only a streamed notice can reach the
// socket, and the unread peer keeps that Write blocked inside the statement.
func TestClientEncodingNoticeStreaming(t *testing.T) {
	for _, route := range []string{"simple", "extended"} {
		t.Run(route, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			enginetest.SessionSQL(t, s, "CREATE TABLE ce_notice_stream(v int)")
			enginetest.SessionSQL(t, s, "SET client_encoding='LATIN1'")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			logs := &serverLogs{ops: logHandler(io.Discard, false), sql: logHandler(io.Discard, false)}
			c := &client{server: &server{logs: logs}, db: &database{engine: e}, session: s, ctx: ctx, cycle: s.NewProtocolCycle(), wire: newWire(framingInput(nil)), freeBuffers: make(chan []byte, 4), lastSettings: make(map[string]string)}
			c.copyStream.client = c
			c.copyHooks = core.CopyProtocolHooks{Start: c.copyStream.start, FinishInput: c.copyStream.finishInput, Notices: c.copyStream.notices}
			c.refreshEncoding()
			source := "DO $$ BEGIN INSERT INTO ce_notice_stream VALUES(1); RAISE NOTICE USING MESSAGE='" + strings.Repeat("é", 1<<16) + "'; PERFORM pg_sleep(600); END $$"
			if route == "extended" {
				body := appendString(appendString(nil, "s"), strings.ReplaceAll(source, "é", "\xe9"))
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
					case <-time.After(testLiveness):
						t.Error("notice worker did not join")
					}
				}
			}()
			go func() {
				if route == "extended" {
					done <- c.extendedCommand(ctx, frontendMessage{tag: 'E', body: appendInt32(appendString(nil, "p"), 0)})
				} else {
					done <- c.executeSimple(ctx, source)
				}
			}()
			select {
			case <-barrier.entered:
			case err := <-done:
				joined = true
				t.Fatal("DO finished before its notice reached the socket", err)
			case <-time.After(testLiveness):
				t.Fatal("notice was not streamed before the DO block finished")
			}
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
			if !admissionFree || !replacementFree || !engineFree {
				t.Errorf("streamed notice Write retained locks: admissionFree=%t replacementFree=%t engineFree=%t", admissionFree, replacementFree, engineFree)
			}
			// Disconnect fails the RAISE's own Write; the block and its INSERT roll back.
			peer.Close()
			select {
			case err := <-done:
				joined = true
				if !errors.Is(err, io.ErrClosedPipe) {
					t.Error("streamed notice disconnect lost actual transport error", err)
				}
			case <-time.After(testLiveness):
				t.Fatal("streamed notice disconnect did not join")
			}
			observer := enginetest.StartSession(t, e, nil)
			if rows := enginetest.Tuples(enginetest.SessionSQL(t, observer, "SELECT count(*) FROM ce_notice_stream")); len(rows) != 1 || len(rows[0]) != 1 || rows[0][0] != int64(0) {
				t.Fatal("disconnected DO block published its INSERT", rows)
			}
		})
	}
}
