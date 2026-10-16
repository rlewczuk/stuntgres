//go:build !noservertests

package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceServerPreparedCopyQuery(t *testing.T) {
	f := fixture.NewServerFixture(t)
	for _, b := range f.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			for _, extended := range []bool{false, true} {
				name := "simple"
				if extended {
					name = "extended"
				}
				t.Run(name, func(t *testing.T) {
					dsn := f.DSNFor(b)
					w := serverAuthenticated(t, dsn)
					peer := serverAuthenticated(t, dsn)
					sequence := "server_prepared_copy_query_" + name
					serverWantCode(t, w.query(t, "CREATE SEQUENCE "+sequence+" START 1"), "")
					// Identical source rows require no portable source ordering.
					// The first demand produces10, the second consumes2 then fails.
					source := "COPY (SELECT 10/(2-nextval('" + sequence + "')) FROM (VALUES (1),(1)) AS q(v)) TO STDOUT"
					serverWantTags(t, serverCycle(t, w, serverParse("cp", source)), "1Z")
					serverScalar(t, peer, "SELECT is_called FROM "+sequence, "f")
					if extended {
						serverWantCode(t, w.query(t, "BEGIN"), "")
					}
					// Requires H before execution failure, with text format and
					// exactly one output column; no SQL PREPARE COPY is involved.
					serverPreparedCopyStart(t, w, extended, false, 0, 1, "EXECUTE cp")
					frames := serverThrough(t, w, 'E')
					serverWantTags(t, frames, "dE")
					if string(frames[0].body) != "10\n" {
						t.Fatalf("COPY prefix %q want 10\\n", frames[0].body)
					}
					serverNotificationError(t, frames, "22012", "division by zero", "")
					serverWantCompletions(t, frames)
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "2")
					if extended {
						w.send(t, serverPacket('S', nil))
					}
					frames = append(frames, w.ready(t)...)
					serverWantTags(t, frames, "dEZ")
					serverWantCompletions(t, frames)
					ready := byte('I')
					if extended {
						ready = 'E'
					}
					serverWantReady(t, frames, ready)
					if extended {
						frames = w.query(t, "ROLLBACK")
						serverWantTags(t, frames, "CZ")
						serverWantCompletions(t, frames, "ROLLBACK")
						serverWantReady(t, frames, 'I')
					}
					serverScalar(t, w, "SELECT 42", "42")
					serverScalar(t, peer, "SELECT last_value FROM "+sequence, "2")
				})
			}
		})
	}
}
