package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"testing"
)

func TestReferenceArrayOutputEnvironments(t *testing.T) {
	arrayReferenceScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE public.array_environment (b BYTEA[],d DATE[],z TIMESTAMPTZ[],i INTERVAL[])", 0))
		run(referenceExec(`INSERT INTO public.array_environment VALUES ('{"\\x005c61ff",NULL}','{2024-02-29,NULL}','{"2024-02-29 12:00:00+00",NULL}','{"1 day 02:03:04",NULL}')`, 1))
		types := []string{"_BYTEA", "_DATE", "_TIMESTAMPTZ", "_INTERVAL"}
		baseline := arrayReferenceSelect("SELECT b,d,z,i FROM public.array_environment", []string{"b", "d", "z", "i"}, types, types, [][]any{{`{"\\x005c61ff",NULL}`, "{2024-02-29,NULL}", `{"2024-02-29 12:00:00+00",NULL}`, `{"1 day 02:03:04",NULL}`}}, b.pg)
		run(baseline)
		run(referencePrepared(baseline))
		var held *sql.Rows
		if !b.pg {
			var err error
			held, err = c.QueryContext(p.ctx, baseline.query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
		}
		for _, setting := range []string{"SET bytea_output TO escape", "SET DateStyle TO 'SQL, DMY'", "SET TimeZone TO '+02:00'", "SET IntervalStyle TO iso_8601"} {
			run(referenceExec(setting, 0))
		}
		changed := arrayReferenceSelect(baseline.query, baseline.columns, types, types, [][]any{{`{"\\000\\\\a\\377",NULL}`, "{29/02/2024,NULL}", `{"29/02/2024 10:00:00 ",NULL}`, "{P1DT2H3M4S,NULL}"}}, b.pg)
		run(changed)
		run(referencePrepared(changed))
		run(referencePrepared(arrayReferenceSelect("SELECT $1::DATE[] AS d,$2::TIMESTAMPTZ[] AS z,$3::INTERVAL[] AS i FROM public.one", []string{"d", "z", "i"}, []string{"_DATE", "_TIMESTAMPTZ", "_INTERVAL"}, []string{"_DATE", "_TIMESTAMPTZ", "_INTERVAL"}, [][]any{{"{29/02/2024}", `{"29/02/2024 12:00:00 "}`, "{P1DT2H3M4S}"}}, b.pg, "{29/02/2024}", `{"29/02/2024 12:00:00"}`, "{P1DT2H3M4S}")))
		if held != nil {
			arrayReferenceConsumeHeld(t, held, baseline.rows, baseline.types)
		}
		for _, setting := range []string{"SET bytea_output TO hex", "SET DateStyle TO 'ISO, MDY'", "SET TimeZone TO UTC", "SET IntervalStyle TO postgres"} {
			run(referenceExec(setting, 0))
		}
		run(baseline)
		// Scalar transaction-clock input independently supplies the session's civil
		// day; the array route must agree without deriving its own expected output.
		run(referenceExec("BEGIN", 0))
		transactionOpen := true
		defer func() {
			if transactionOpen {
				if _, err := c.ExecContext(p.ctx, "ROLLBACK"); err != nil {
					t.Error(err)
				}
			}
		}()
		var today string
		if err := c.QueryRowContext(p.ctx, "SELECT TIMESTAMP 'now'::DATE::TEXT AS d FROM public.one").Scan(&today); err != nil {
			t.Fatal(err)
		}
		query := "SELECT '{today}'::DATE[] AS a FROM public.one"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		expected := arrayReferenceSelect(query, []string{"a"}, []string{"_DATE"}, []string{"_DATE"}, [][]any{{"{" + today + "}"}}, b.pg)
		for range 2 {
			run(referencePrepared(expected))
			referenceRunStep(t, p.ctx, retained, b.recorder, expected)
			run(referencePrepared(arrayReferenceSelect("SELECT $1::DATE[] AS a FROM public.one", []string{"a"}, []string{"_DATE"}, []string{"_DATE"}, [][]any{{"{" + today + "}"}}, b.pg, "{today}")))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(referenceExec("COMMIT", 0))
		transactionOpen = false
		if !b.pg {
			arrayReferenceNativeRoundtrip(t, p, c, b, baseline)
			var stable bytes.Buffer
			if err := stuntgres.Dump(p.ctx, p.native, &stable); err != nil {
				t.Fatal(err)
			}
			target := newReferenceFixture(t, false, true)
			for _, setting := range []string{"SET bytea_output TO escape", "SET DateStyle TO 'SQL, DMY'", "SET TimeZone TO '+02:00'", "SET IntervalStyle TO iso_8601"} {
				referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referenceExec(setting, 0))
			}
			if err := stuntgres.Restore(target.ctx, target.native, strings.NewReader(stable.String())); err != nil {
				t.Fatal(err)
			}
			referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, changed)
			referenceRunStep(t, target.ctx, target.memory, target.memoryRecord, referencePrepared(changed))
			var redump bytes.Buffer
			if err := stuntgres.Dump(target.ctx, target.native, &redump); err != nil {
				t.Fatal(err)
			}
			if stable.String() != redump.String() {
				t.Fatal("output settings changed stable array control serialization")
			}
		}
	})
}
