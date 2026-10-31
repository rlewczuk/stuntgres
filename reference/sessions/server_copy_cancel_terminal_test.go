//go:build !noservertests

package reference_test

import (
	"io"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerCopyCancelTerminal(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			dsn := f.DSNFor(b)
			peer := serverAuthenticated(t, dsn)
			serverWantCode(t, peer.query(t, "CREATE TABLE server_copy_cancel_terminal(v integer); INSERT INTO server_copy_cancel_terminal VALUES (99)"), "")
			w := serverAuthenticated(t, dsn)
			defer w.Close()
			pid := sessionSignalWirePID(t, w)
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverCopyStart(t, w, "COPY server_copy_cancel_terminal FROM STDIN", true, 0, 1)
			serverObserveCopyInput(t, f, b, w)
			serverCancel(t, dsn, w.pid, w.key)
			// No further frontend input is needed to retire a canceled COPY.
			serverScalar(t, peer, "SELECT v FROM server_copy_cancel_terminal", "99")
			var terminated bool
			if err := f.RawPool(t, b).QueryRowContext(f.Ctx, "SELECT pg_terminate_backend($1)", pid).Scan(&terminated); err != nil || !terminated {
				t.Fatalf("backend termination=%v: %v", terminated, err)
			}
			frame := w.frame(t)
			serverWantTags(t, []serverFrame{frame}, "E")
			serverWantCompletions(t, []serverFrame{frame})
			fields := serverFields(frame.body)
			for key, want := range map[byte]string{
				'S': "FATAL", 'V': "FATAL", 'C': "57P01",
				'M': "terminating connection due to administrator command",
				'W': "COPY server_copy_cancel_terminal, line 1",
				'P': "", 'D': "", 'H': "", 'p': "", 'q': "",
				's': "", 't': "", 'c': "", 'd': "", 'n': "",
			} {
				if fields[key] != want {
					t.Fatalf("terminal COPY diagnostic field %c=%q want %q: %v", key, fields[key], want, fields)
				}
			}
			if next, err := w.readFrame(); err != io.EOF {
				t.Fatalf("terminal COPY must close without completion/ReadyForQuery: tag=%c body=%x error=%v", next.tag, next.body, err)
			}
			w.Close()
			serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
			serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_copy_cancel_terminal IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
			serverScalar(t, peer, "SELECT v FROM server_copy_cancel_terminal", "99")
			fresh := serverAuthenticated(t, dsn)
			serverScalar(t, fresh, "SELECT 42", "42")
			serverScalar(t, fresh, "SELECT v FROM server_copy_cancel_terminal", "99")
		})
	}
}
