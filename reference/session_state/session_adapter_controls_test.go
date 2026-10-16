package reference_test

import (
	"bytes"
	"database/sql"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSessionPhysicalControlsAndPoolClosure(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		a, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer a.Close()
		b, err := backend.DB.Conn(p.Ctx)
		if err != nil {
			a.Close()
			t.Fatal(err)
		}
		defer b.Close()
		run := func(e fixture.Executor, s fixture.Step) { fixture.RunStep(t, p.Ctx, e, backend.Recorder, s) }
		run(a, fixture.Exec("CREATE TABLE sentinel(v INT)", 0))
		run(a, fixture.Exec("INSERT INTO sentinel VALUES(7)", 1))
		run(a, fixture.Exec("SET SESSION application_name TO 'session-a'", 0))
		run(b, fixture.Prepared(fixture.Exec("SET application_name = 'session-b'", 0)))
		first, second := fixture.SettingsPhysical(t, a), fixture.SettingsPhysical(t, b)
		if first == second {
			t.Fatal("owner and peer share physical connection")
		}
		if !backend.PG {
			var dump bytes.Buffer
			if err = stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			if err = stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			run(a, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "session-a"))
			if err = stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				t.Fatal(err)
			}
		}
		run(a, localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "session-a"))
		run(b, fixture.Prepared(localReferenceSelect(backend.PG, "SHOW application_name", "application_name", "session-b")))
		run(a, fixture.Select("SELECT v FROM sentinel", []string{"v"}, []string{"INT4"}, [][]any{{int64(7)}}))
		if fixture.SettingsPhysical(t, a) != first || fixture.SettingsPhysical(t, b) != second {
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
		if backend.PG {
			if err = p.Postgres.Close(); err != nil {
				t.Fatal(err)
			}
			reopened, err = sql.Open("stuntgres_reference_postgres", fixture.DSN)
			if err != nil {
				t.Fatal(err)
			}
		} else {
			if err = p.Memory.Close(); err != nil {
				t.Fatal(err)
			}
			if err = p.Native.Close(); err != nil {
				t.Fatal(err)
			}
			reopened = sql.OpenDB(p.NativeConnector)
		}
		defer reopened.Close()
		c, err := reopened.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer c.Close()
		sessionShow(t, c, "", true)
		var value int64
		if err = c.QueryRowContext(p.Ctx, "SELECT v FROM sentinel").Scan(&value); err != nil || value != 7 {
			t.Fatal(value, err)
		}
	}
}
