package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceArrayOutputEnvironments(t *testing.T) {
	fixture.ArrayReferenceScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE public.array_environment (b BYTEA[],d DATE[],z TIMESTAMPTZ[],i INTERVAL[])", 0))
		run(fixture.Exec(`INSERT INTO public.array_environment VALUES ('{"\\x005c61ff",NULL}','{2024-02-29,NULL}','{"2024-02-29 12:00:00+00",NULL}','{"1 day 02:03:04",NULL}')`, 1))
		types := []string{"_BYTEA", "_DATE", "_TIMESTAMPTZ", "_INTERVAL"}
		baseline := fixture.ArrayReferenceSelect("SELECT b,d,z,i FROM public.array_environment", []string{"b", "d", "z", "i"}, types, types, [][]any{{`{"\\x005c61ff",NULL}`, "{2024-02-29,NULL}", `{"2024-02-29 12:00:00+00",NULL}`, `{"1 day 02:03:04",NULL}`}}, b.PG)
		run(baseline)
		run(fixture.Prepared(baseline))
		var held *sql.Rows
		if !b.PG {
			var err error
			held, err = c.QueryContext(p.Ctx, baseline.Query)
			if err != nil {
				t.Fatal(err)
			}
			defer held.Close()
		}
		for _, setting := range []string{"SET bytea_output TO escape", "SET DateStyle TO 'SQL, DMY'", "SET TimeZone TO '+02:00'", "SET IntervalStyle TO iso_8601"} {
			run(fixture.Exec(setting, 0))
		}
		changed := fixture.ArrayReferenceSelect(baseline.Query, baseline.Columns, types, types, [][]any{{`{"\\000\\\\a\\377",NULL}`, "{29/02/2024,NULL}", `{"29/02/2024 10:00:00 ",NULL}`, "{P1DT2H3M4S,NULL}"}}, b.PG)
		run(changed)
		run(fixture.Prepared(changed))
		run(fixture.Prepared(fixture.ArrayReferenceSelect("SELECT $1::DATE[] AS d,$2::TIMESTAMPTZ[] AS z,$3::INTERVAL[] AS i FROM public.one", []string{"d", "z", "i"}, []string{"_DATE", "_TIMESTAMPTZ", "_INTERVAL"}, []string{"_DATE", "_TIMESTAMPTZ", "_INTERVAL"}, [][]any{{"{29/02/2024}", `{"29/02/2024 12:00:00 "}`, "{P1DT2H3M4S}"}}, b.PG, "{29/02/2024}", `{"29/02/2024 12:00:00"}`, "{P1DT2H3M4S}")))
		if held != nil {
			fixture.ArrayReferenceConsumeHeld(t, held, baseline.Rows, baseline.Types)
		}
		for _, setting := range []string{"SET bytea_output TO hex", "SET DateStyle TO 'ISO, MDY'", "SET TimeZone TO UTC", "SET IntervalStyle TO postgres"} {
			run(fixture.Exec(setting, 0))
		}
		run(baseline)
		// Scalar transaction-clock input independently supplies the session's civil
		// day; the array route must agree without deriving its own expected output.
		run(fixture.Exec("BEGIN", 0))
		transactionOpen := true
		defer func() {
			if transactionOpen {
				if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
					t.Error(err)
				}
			}
		}()
		var today string
		if err := c.QueryRowContext(p.Ctx, "SELECT TIMESTAMP 'now'::DATE::TEXT AS d FROM public.one").Scan(&today); err != nil {
			t.Fatal(err)
		}
		query := "SELECT '{today}'::DATE[] AS a FROM public.one"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		retained := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		expected := fixture.ArrayReferenceSelect(query, []string{"a"}, []string{"_DATE"}, []string{"_DATE"}, [][]any{{"{" + today + "}"}}, b.PG)
		for range 2 {
			run(fixture.Prepared(expected))
			fixture.RunStep(t, p.Ctx, retained, b.Recorder, expected)
			run(fixture.Prepared(fixture.ArrayReferenceSelect("SELECT $1::DATE[] AS a FROM public.one", []string{"a"}, []string{"_DATE"}, []string{"_DATE"}, [][]any{{"{" + today + "}"}}, b.PG, "{today}")))
		}
		if err := stmt.Close(); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("COMMIT", 0))
		transactionOpen = false
		if !b.PG {
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, baseline)
			var stable bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &stable); err != nil {
				t.Fatal(err)
			}
			target := fixture.NewFixture(t, false, true)
			for _, setting := range []string{"SET bytea_output TO escape", "SET DateStyle TO 'SQL, DMY'", "SET TimeZone TO '+02:00'", "SET IntervalStyle TO iso_8601"} {
				fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Exec(setting, 0))
			}
			if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(stable.String())); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, changed)
			fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, fixture.Prepared(changed))
			var redump bytes.Buffer
			if err := stuntgres.Dump(target.Ctx, target.Native, &redump); err != nil {
				t.Fatal(err)
			}
			if stable.String() != redump.String() {
				t.Fatal("output settings changed stable array control serialization")
			}
		}
	})
}
