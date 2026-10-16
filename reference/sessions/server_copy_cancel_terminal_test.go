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
			serverWantCode(t, peer.query(t, "CREATE TABLE server_copy_cancel_terminal(v integer)"), "")
			w := serverAuthenticated(t, dsn)
			defer w.Close()
			serverWantCode(t, w.query(t, "BEGIN"), "")
			serverCopyStart(t, w, "COPY server_copy_cancel_terminal FROM STDIN", true, 0, 1)
			serverObserveCopyInput(t, f, b, w)
			serverCancel(t, dsn, w.pid, w.key)
			// Deliberately send no CopyData, CopyDone or Sync. These are
			// independent real retirement mechanisms, not a native SQL-provider
			// claim: PostgreSQL terminates one backend; native stops its whole
			// actual process through the existing fixture control.
			serverScalar(t, peer, "SELECT count(*) FROM server_copy_cancel_terminal", "0")
			if b.PG {
				var terminated bool
				if err := f.RawPool(t, b).QueryRowContext(f.Ctx, "SELECT pg_terminate_backend($1)", w.pid).Scan(&terminated); err != nil || !terminated {
					t.Fatalf("actual PostgreSQL backend termination=%v: %v", terminated, err)
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
			} else {
				// Stop requires a real successful child join, not suppression of
				// a shutdown error. No native SQL FATAL frame is promised here.
				f.Stop(t)
			}
			if next, err := w.readFrame(); err != io.EOF {
				t.Fatalf("terminal COPY must close without completion/ReadyForQuery: tag=%c body=%x error=%v", next.tag, next.body, err)
			}
			w.Close()
			if !b.PG {
				// Native uses whole-process Stop/Dump and genuine Restore,
				// stronger than PostgreSQL's per-backend termination boundary.
				// The original committed table must survive with no COPY rows.
				f.Restart(t, true)
				dsn = f.DSNFor(b)
				peer = serverAuthenticated(t, dsn)
			}
			serverWantCode(t, peer.query(t, "SET lock_timeout='1000ms'"), "")
			serverWantCode(t, peer.query(t, "BEGIN; LOCK TABLE server_copy_cancel_terminal IN ACCESS EXCLUSIVE MODE; ROLLBACK"), "")
			serverScalar(t, peer, "SELECT count(*) FROM server_copy_cancel_terminal", "0")
			fresh := serverAuthenticated(t, dsn)
			serverScalar(t, fresh, "SELECT 42", "42")
		})
	}
}
