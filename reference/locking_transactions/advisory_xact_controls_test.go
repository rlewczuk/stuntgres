package reference_test

import (
	"bytes"
	"errors"
	"io"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

// These are native-control phases of the common lifecycle scenario, not an
// invented PostgreSQL replacement API. SQL lifetime witnesses run in both modes.
func advisoryXactControls(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	if b.PG {
		return
	}
	a, peer, observer := advisoryXactConn(t, p, b), advisoryXactConn(t, p, b), advisoryXactConn(t, p, b)
	ap, bp := advisoryXactPID(t, p, b, a), advisoryXactPID(t, p, b, peer)
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, k := range advisorySessionKeys {
		for _, restore := range []bool{false, true} {
			other := advisorySessionKeys[2-k.space]
			advisorySessionValue(t, p.Ctx, a, other.call("pg_advisory_lock"), "VOID", "")
			advisorySessionExec(t, p.Ctx, a, "BEGIN", 0)
			advisorySessionValue(t, p.Ctx, a, k.call("pg_advisory_xact_lock"), "VOID", "")
			if err := stuntgres.Restore(p.Ctx, p.Native, advisorySessionFailReader{io.ErrUnexpectedEOF}); !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Fatal(err)
			}
			advisoryXactProbe(t, p, observer, k, false, false)
			advisoryXactProbe(t, p, observer, other, false, false)
			advisorySessionExec(t, p.Ctx, peer, "BEGIN", 0)
			q := k.call("pg_advisory_xact_lock")
			x := advisoryXactHeld(t, p, peer, q)
			w := advisoryXactStart(t, p.Ctx, x, q)
			advisoryXactWait(t, p, observer, bp, ap, k, false)
			var err error
			if restore {
				err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes()))
			} else {
				err = stuntgres.Reset(p.Ctx, p.Native)
			}
			if err != nil {
				t.Fatal(err)
			}
			fixture.TransactionOrdinary(t, w.await(t), "stuntgres: catalog replaced before publication")
			// Old transaction contributions are gone; real session ownership survives.
			advisoryXactProbe(t, p, observer, k, false, true)
			advisoryXactProbe(t, p, observer, other, false, false)
			fixture.TransactionOrdinary(t, func() error { _, err := a.ExecContext(p.Ctx, "COMMIT"); return err }(), "stuntgres: catalog replaced before publication")
			// ROLLBACK is valid recovery for the waiter's fenced/aborted block;
			// unlike COMMIT, it does not attempt old-epoch publication.
			advisorySessionExec(t, p.Ctx, peer, "ROLLBACK", 0)
			advisorySessionValue(t, p.Ctx, peer, "SELECT true", "BOOL", true)
			advisorySessionClear(t, p.Ctx, a)
			advisoryXactProbe(t, p, observer, other, false, true)
		}
	}
}
