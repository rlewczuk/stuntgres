package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"testing"
)

func TestReferenceSessionPhysicalControlsAndPoolClosure(t *testing.T) {
	p := newReferenceScenario(t)
	for _, backend := range p.backends() {
		a, err := backend.db.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.db.Conn(p.ctx)
		if err != nil {
			a.Close()
			t.Fatal(err)
		}
		defer b.Close()
		run := func(e referenceExecutor, s referenceStep) { referenceRunStep(t, p.ctx, e, backend.recorder, s) }
		run(a, referenceExec("CREATE TABLE sentinel(v INT)", 0))
		run(a, referenceExec("INSERT INTO sentinel VALUES(7)", 1))
		run(a, referenceExec("SET SESSION application_name TO 'session-a'", 0))
		run(b, referencePrepared(referenceExec("SET application_name = 'session-b'", 0)))
		first, second := settingsPhysical(t, a), settingsPhysical(t, b)
		if first == second {
			t.Fatal("owner and peer share physical connection")
		}
		if !backend.pg {
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.ctx, p.native, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Reset(p.ctx, p.native); err != nil {
				t.Fatal(err)
			}
			run(a, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "session-a"))
			if err = stuntgres.Restore(p.ctx, p.native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		run(a, localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "session-a"))
		run(b, referencePrepared(localReferenceSelect(backend.pg, "SHOW application_name", "application_name", "session-b")))
		run(a, referenceSelect("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if settingsPhysical(t, a) != first || settingsPhysical(t, b) != second {
			t.Fatal("controls replaced physical identity")
		}
		if err = a.Close(); err != nil {
			t.Fatal(err)
		}
		if err = b.Close(); err != nil {
			t.Fatal(err)
		}
		// Close every native pool for the selected Driver/opaque DSN, then reopen
		// through the same public connector; committed catalog lifetime is independent.
		var reopened *sql.DB
		if backend.pg {
			if err = p.postgres.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err = sql.Open("stuntgres_reference_postgres", referenceDSN)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			if err = p.memory.Close(); err != nil {
				t.Fatal(err)
			}
			if err = p.native.Close(); err != nil {
				t.Fatal(err)
			}
			reopened = sql.OpenDB(p.nativeConnector)
		}
		defer reopened.Close()
		c, err := reopened.Conn(p.ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		sessionShow(t, c, "", true)
		var value int64
		if err = c.QueryRowContext(p.ctx, "SELECT v FROM sentinel").Scan(&value); err != nil || value != 7 {
			t.Fatal(value, err)
		}
	}
}
