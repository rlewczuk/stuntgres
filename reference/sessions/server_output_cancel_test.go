//go:build !noservertests

package reference_test

import (
	"context"
	"fmt"
	"net"
	"runtime"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"time"
)

// This cancels a real row stream, not execution waiting for a relation lock.
// The first observed DataRow comes from the active protocol SELECT producer;
// cancellation must still fail the SQL transaction after output has begun.
func TestReferenceServerOutputCancellation(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			owner := serverAuthenticated(t, dsn)
			serverWantCode(t, owner.query(t, "CREATE TABLE server_output_rows(v integer); INSERT INTO server_output_rows VALUES (0); CREATE TABLE server_output_sentinel(v integer)"), "")
			// A small real table supplies 4096 rows without an unsupported SRF.
			// Only the wire result is large; neither the table nor this consumer
			// retains thousands of copies of the payload.
			const rowCount = 4096
			for n := 1; n < rowCount; n *= 2 {
				serverWantCode(t, owner.query(t, fmt.Sprintf("INSERT INTO server_output_rows SELECT v + %d FROM server_output_rows", n)), "")
			}
			serverScalar(t, owner, "SELECT count(*) FROM server_output_rows", "4096")

			victim := serverDial(t, dsn)
			tcp, ok := victim.Conn.(*net.TCPConn)
			if !ok {
				t.Fatal("row-stream backpressure requires a TCP connection")
			}
			// Bound the receive window before SQL output. The32MiB result
			// exceeds this window and the server send buffer, so the observed
			// first row cannot imply that the entire command has completed.
			if err := tcp.SetReadBuffer(64 << 10); err != nil {
				t.Fatal(err)
			}
			serverWantCode(t, victim.login(t, dsn, 196608), "")
			serverWantCode(t, victim.query(t, "BEGIN; INSERT INTO server_output_sentinel VALUES (77)"), "")
			serverScalar(t, victim, "SELECT count(*) FROM server_output_sentinel", "1")

			const payloadBytes = 8 << 10
			payload := strings.Repeat("x", payloadBytes)
			if err := victim.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
				t.Fatal(err)
			}
			victim.send(t, serverPacket('Q', []byte("SELECT '"+payload+"'::text FROM server_output_rows\x00")))
			if frame := victim.frame(t); frame.tag != 'T' {
				t.Fatalf("expected RowDescription, got %c", frame.tag)
			}
			first := victim.frame(t)
			if first.tag != 'D' {
				t.Fatalf("expected observed first DataRow, got %c", first.tag)
			}
			values := serverValues(t, []serverFrame{first})
			if len(values) != 1 || len(values[0]) != 1 || values[0][0] != payload {
				t.Fatal("first streamed row differs from the requested payload")
			}

			if b.PG {
				// PostgreSQL exposes an independent, exact socket-write wait.
				// The victim has no background reader: observing ClientWrite
				// cannot accidentally drain the result or finish the command.
				ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
				defer cancel()
				observer := f.RawPool(t, b)
				for {
					var blocked bool
					if err := observer.QueryRowContext(ctx, "SELECT COALESCE(wait_event_type = 'Client' AND wait_event = 'ClientWrite', false) FROM pg_stat_activity WHERE pid = $1", victim.pid).Scan(&blocked); err != nil {
						t.Fatal("row-output socket-write barrier", err)
					}
					if blocked {
						break
					}
					runtime.Gosched()
				}
			}
			// The separate cancellation connection is read through EOF by
			// serverCancel. That acknowledges handling before this sole owner
			// resumes reading the victim, fencing out later SQL commands.
			serverCancel(t, dsn, victim.pid, victim.key)
			if err := tcp.SetReadBuffer(1 << 20); err != nil {
				t.Fatal(err)
			}

			// Discard each DataRow immediately rather than accumulating the
			// result in ready(). Retain only counters and the one diagnostic.
			rows := 1
			code := ""
			ready := ""
			completed := false
		readStream:
			for {
				frame := victim.frame(t)
				switch frame.tag {
				case 'D':
					if code != "" {
						t.Fatal("DataRow followed cancellation ErrorResponse")
					}
					rows++
				case 'E':
					if code != "" {
						t.Fatal("multiple cancellation errors")
					}
					fields := serverFields(frame.body)
					code = fields['C']
					if fields['M'] != "canceling statement due to user request" {
						t.Fatalf("cancellation message %q", fields['M'])
					}
				case 'C':
					completed = true
				case 'Z':
					ready = string(frame.body)
					break readStream
				case 'S', 'N':
				default:
					t.Fatalf("unexpected row-stream frame %c", frame.tag)
				}
			}
			if code != "57014" || ready != "E" || completed || rows >= rowCount {
				t.Fatalf("canceled output: SQLSTATE=%q Ready=%q completed=%v rows=%d/%d; want 57014, E and incomplete result", code, ready, completed, rows, rowCount)
			}
			failed := victim.query(t, "SELECT 42")
			serverWantCode(t, failed, "25P02")
			if string(failed[len(failed)-1].body) != "E" {
				t.Fatal("failed transaction did not remain aborted")
			}
			commit := victim.query(t, "COMMIT")
			serverWantCode(t, commit, "")
			if serverTags(commit) != "CZ" || string(commit[0].body) != "ROLLBACK\x00" || string(commit[1].body) != "I" {
				t.Fatalf("COMMIT after output cancellation: %s", serverFrameSummary(commit))
			}
			serverScalar(t, victim, "SELECT count(*) FROM server_output_sentinel", "0")
			serverScalar(t, owner, "SELECT count(*) FROM server_output_sentinel", "0")
			serverScalar(t, victim, "SELECT 42", "42")
		})
	}
}
