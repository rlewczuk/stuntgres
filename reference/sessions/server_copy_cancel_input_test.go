//go:build !noservertests

package reference_test

import (
	"context"
	"runtime"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceServerCopyCancelInput(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_copy_cancel_input(v integer)"), "")
			for _, c := range []struct {
				name, code, message, context string
				chunks                       [][]byte
			}{
				{
					name:   "split_record",
					chunks: [][]byte{[]byte("1"), []byte("\n")},
					code:   "57014", message: "canceling statement due to user request",
					context: "COPY server_copy_cancel_input, line 1: \"1\"",
				},
				{
					name:   "invalid_record",
					chunks: [][]byte{[]byte("bad\n")},
					code:   "22P02", message: "invalid input syntax for type integer: \"bad\"",
					context: "COPY server_copy_cancel_input, line 1, column v: \"bad\"",
				},
			} {
				t.Run(c.name, func(t *testing.T) {
					w := serverAuthenticated(t, dsn)
					serverWantCode(t, w.query(t, "BEGIN"), "")
					// The existing helper requires exact Parse/Bind completion and
					// text G metadata for one column before returning.
					serverCopyStart(t, w, "COPY server_copy_cancel_input FROM STDIN", true, 0, 1)
					serverObserveCopyInput(t, f, b, w)
					if err := w.SetDeadline(time.Now().Add(20 * time.Second)); err != nil {
						t.Fatal(err)
					}
					// EOF on the cancellation connection is not a target-executor
					// acknowledgement. The primary input/interrupt outcome below
					// is required before any terminal frontend COPY message.
					serverCancel(t, dsn, w.pid, w.key)
					for _, chunk := range c.chunks {
						w.send(t, serverPacket('d', chunk))
					}
					// Both halves of the split record are sent before waiting;
					// there is no expectation of an error on an unfinished record.
					frames := serverThrough(t, w, 'E')
					serverWantTags(t, frames, "E")
					serverNotificationError(t, frames, c.code, c.message, "", c.context)
					serverWantCompletions(t, frames)
					w.send(t, serverPacket('c', nil))
					w.send(t, serverPacket('S', nil))
					frames = append(frames, w.ready(t)...)
					serverWantTags(t, frames, "EZ")
					serverWantReady(t, frames, 'E')
					serverWantCompletions(t, frames)
					serverScalar(t, peer, "SELECT count(*) FROM server_copy_cancel_input", "0")
					serverWantCode(t, w.query(t, "ROLLBACK"), "")
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT count(*) FROM server_copy_cancel_input", "0")
				})
			}
		})
	}
}

// PostgreSQL exposes its actual socket-read wait. Native has no equivalent
// public observer; G and the required real ErrorResponse remain shared proof.
func serverObserveCopyInput(t *testing.T, f *fixture.ServerFixture, b fixture.Backend, w *serverWire) {
	t.Helper()
	if !b.PG {
		return
	}
	ctx, cancel := context.WithTimeout(f.Ctx, 10*time.Second)
	defer cancel()
	observer := f.RawPool(t, b)
	for {
		var reading bool
		if err := observer.QueryRowContext(ctx, "SELECT COALESCE(state='active' AND wait_event_type='Client' AND wait_event='ClientRead',false) FROM pg_stat_activity WHERE pid=$1", w.pid).Scan(&reading); err != nil {
			t.Fatal("COPY frontend-read observation", err)
		}
		if reading {
			return
		}
		runtime.Gosched()
	}
}
