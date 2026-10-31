//go:build !noservertests

package reference_test

import (
	"io"
	"strconv"
	"stuntgres/reference/fixture"
	"testing"
)

func sessionSignalWirePID(t *testing.T, w *serverWire) int {
	t.Helper()
	frames := w.query(t, "SELECT pg_backend_pid()")
	serverWantCode(t, frames, "")
	rows := serverRawRows(t, frames)
	if len(rows) != 1 || len(rows[0]) != 1 {
		t.Fatal(rows)
	}
	pid, err := strconv.Atoi(string(rows[0][0]))
	if err != nil || pid <= 0 {
		t.Fatal(rows, err)
	}
	return pid
}
func TestReferenceSessionSignalWire(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE session_signal_wire(v integer); INSERT INTO session_signal_wire VALUES(99)"), "")
			victim := serverAuthenticated(t, dsn)
			pid := sessionSignalWirePID(t, victim)
			serverWantCode(t, victim.query(t, "BEGIN"), "")
			serverCopyStart(t, victim, "COPY session_signal_wire FROM STDIN", true, 0, 1)
			serverObserveCopyInput(t, f, b, victim)
			var sent bool
			if err := f.RawPool(t, b).QueryRowContext(f.Ctx, "SELECT pg_cancel_backend($1)", pid).Scan(&sent); err != nil || !sent {
				t.Fatal(sent, err)
			}
			victim.send(t, serverPacket('d', []byte("1\n")))
			frames := serverThrough(t, victim, 'E')
			serverWantTags(t, frames, "E")
			serverNotificationError(t, frames, "57014", "canceling statement due to user request", "", "COPY session_signal_wire, line 1: \"1\"")
			serverWantCompletions(t, frames)
			victim.send(t, serverPacket('c', nil))
			victim.send(t, serverPacket('S', nil))
			frames = append(frames, victim.ready(t)...)
			serverWantTags(t, frames, "EZ")
			serverWantReady(t, frames, 'E')
			serverWantCompletions(t, frames)
			serverWantCode(t, victim.query(t, "ROLLBACK"), "")
			serverScalar(t, victim, "SELECT 42", "42")
			serverScalar(t, peer, "SELECT v FROM session_signal_wire", "99")
			if after := sessionSignalWirePID(t, victim); after != pid {
				t.Fatal(pid, after)
			}
			// A fresh pending COPY also terminates without any frontend completion input.
			serverCopyStart(t, victim, "COPY session_signal_wire FROM STDIN", true, 0, 1)
			serverObserveCopyInput(t, f, b, victim)
			if err := f.RawPool(t, b).QueryRowContext(f.Ctx, "SELECT pg_terminate_backend($1,1000)", pid).Scan(&sent); err != nil || !sent {
				t.Fatal(sent, err)
			}
			terminal := victim.frame(t)
			serverWantTags(t, []serverFrame{terminal}, "E")
			serverWantCompletions(t, []serverFrame{terminal})
			fields := serverFields(terminal.body)
			for key, want := range map[byte]string{'S': "FATAL", 'V': "FATAL", 'C': "57P01", 'M': "terminating connection due to administrator command", 'W': "COPY session_signal_wire, line 1", 'P': "", 'D': "", 'H': "", 'p': "", 'q': "", 's': "", 't': "", 'c': "", 'd': "", 'n': ""} {
				if fields[key] != want {
					t.Fatalf("terminal field %c=%q want %q: %v", key, fields[key], want, fields)
				}
			}
			if frame, err := victim.readFrame(); err != io.EOF {
				t.Fatal("expected terminal EOF without Ready", frame, err)
			}
			serverScalar(t, peer, "SELECT 42", "42")
			serverScalar(t, peer, "SELECT v FROM session_signal_wire", "99")
		})
	}
}
