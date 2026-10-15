package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceArrayOrderConsumersControls(t *testing.T) {
	arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN public.ao_stored_array AS int[]",
			"CREATE TYPE public.ao_stored_enum AS ENUM ('z','a')",
			"CREATE DOMAIN public.ao_stored_enum_leaf AS public.ao_stored_enum",
			"CREATE TABLE public.ao_stored(id int,a public.ao_stored_array,b public.ao_stored_array,d bool DEFAULT ('{1}'::int[]<'{2}'::int[]),g bool GENERATED ALWAYS AS (a<b) STORED,CONSTRAINT ao_guard CHECK (a<=b))",
			"CREATE VIEW public.ao_view AS SELECT id,a=b AS eq,a<b AS lt,d,g FROM public.ao_stored",
			"CREATE INDEX ao_equal_index ON public.ao_stored ((a=b))",
			"CREATE TABLE public.ao_enum_owner(id int,a public.ao_stored_enum_leaf,b public.ao_stored_enum_leaf,g bool GENERATED ALWAYS AS (ARRAY[a]<ARRAY[b]) STORED)",
			"CREATE VIEW public.ao_enum_view AS SELECT id,ARRAY[a]=ARRAY[b] AS eq,g FROM public.ao_enum_owner",
		} {
			s := fixture.Exec(q, 0)
			if q == "CREATE INDEX ao_equal_index ON public.ao_stored ((a=b))" {
				s.Prepared = true
			}
			run(s)
		}
		run(fixture.Exec("INSERT INTO public.ao_stored(id,a,b) VALUES(1,'{1}','{2}'),(2,'{NULL}','{NULL}'),(3,NULL,'{1}')", 3))
		run(fixture.Prepared(fixture.Exec("INSERT INTO public.ao_enum_owner(id,a,b) VALUES($1,$2,$3)", 1, 1, "z", "a")))
		check := fixture.Select("SELECT id,eq,lt,d,g FROM public.ao_view ORDER BY id", []string{"id", "eq", "lt", "d", "g"}, []string{"INT4", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{int64(1), false, true, true, true}, {int64(2), true, false, true, false}, {int64(3), nil, nil, true, nil}})
		enumCheck := fixture.Select("SELECT id,eq,g FROM public.ao_enum_view ORDER BY id", []string{"id", "eq", "g"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), false, true}})
		arrayCompareBoth(t, p, b, c, check)
		arrayCompareBoth(t, p, b, c, enumCheck)
		stmt, err := c.PrepareContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: check.Query}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		}
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,CASE a WHEN b THEN true ELSE false END AS matched,NULLIF(a,b) IS NULL AS nulled FROM public.ao_stored ORDER BY id", []string{"id", "matched", "nulled"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), false, false}, {int64(2), true, true}, {int64(3), false, true}}),
			fixture.Select("SELECT a=b AS eq,count(*) AS n FROM public.ao_stored GROUP BY a=b ORDER BY eq", []string{"eq", "n"}, []string{"BOOL", "INT8"}, [][]any{{false, int64(1)}, {true, int64(1)}, {nil, int64(1)}}),
			fixture.Select("SELECT count(*) FILTER(WHERE a<b) AS n FROM public.ao_stored", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}),
			fixture.Select("SELECT DISTINCT a=b AS eq FROM public.ao_stored ORDER BY eq", []string{"eq"}, []string{"BOOL"}, [][]any{{false}, {true}, {nil}}),
			fixture.Select("SELECT id,a IS DISTINCT FROM b AS different FROM public.ao_stored ORDER BY id", []string{"id", "different"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}, {int64(3), true}}),
			fixture.Select("SELECT l.id FROM public.ao_stored l JOIN public.ao_stored r ON l.a=r.b ORDER BY l.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT l.id FROM public.ao_stored l WHERE EXISTS(SELECT 1 FROM public.ao_stored r WHERE l.a=r.b) ORDER BY l.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
		} {
			arrayCompareBoth(t, p, b, c, s)
		}
		d := fixture.ControlsReferenceDiagnostic("23514", `new row for relation "ao_stored" violates check constraint "ao_guard"`)
		d.Schema, d.Table, d.Constraint = "public", "ao_stored", "ao_guard"
		d.Detail = "Failing row contains (5, {2}, {1}, t, f)."
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO public.ao_stored(id,a,b) VALUES(4,'{1}','{2}'),(5,'{2}','{1}')", d)
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(check)
		}
		bad := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		bad.Detail = `Unexpected "," character.`
		bad.Context = "unnamed portal parameter $2 = '...'"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("INSERT INTO public.ao_stored(id,a,b) VALUES(4,$1,'{2}'),(5,$2,'{2}')", bad, "{1}", "{1,,2}")
			s.Prepared, s.ErrorStage = prepared, "execute"
			run(s)
			run(check)
		}
		run(fixture.Exec("CREATE TABLE public.ao_mutation(id int,a int[],b int[])", 0))
		run(fixture.Exec("INSERT INTO public.ao_mutation VALUES(1,'{1}','{2}'),(2,'{NULL}','{NULL}')", 2))
		run(fixture.Select("UPDATE public.ao_mutation SET a=b WHERE a<b RETURNING id,a=b AS eq", []string{"id", "eq"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}}))
		s := fixture.Select("DELETE FROM public.ao_mutation WHERE a=b RETURNING id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}})
		s.Bag = true
		run(fixture.Prepared(s))
		run(fixture.Exec("ALTER TABLE public.ao_stored RENAME COLUMN a TO lhs", 0))
		run(check)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		// Repeated validation and dump must compare recursive plans structurally.
		if b.PG {
			arrayOrderDomainRecreate(t, p, b, c, enumCheck)
			return
		}
		var dump, again bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Dump(p.Ctx, p.Native, &again); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(dump.Bytes(), again.Bytes()) {
			t.Fatal("repeated dump changed")
		}
		if p.Postgres != nil {
			defer func() {
				for _, q := range []string{"DROP TABLE IF EXISTS public.ao_stored,public.ao_enum_owner,public.ao_mutation CASCADE", "DROP DOMAIN IF EXISTS public.ao_stored_array,public.ao_stored_enum_leaf CASCADE", "DROP TYPE IF EXISTS public.ao_stored_enum CASCADE"} {
					if _, err := p.Postgres.ExecContext(p.Ctx, q); err != nil {
						t.Error(err)
					}
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatal("emitted SQL replay", err)
			}
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(check))
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, enumCheck)
		}
		retained, err := c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		cols, err := retained.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		// Replace the catalog with deliberately different current values while
		// the original eager cursor still owns its captured metadata and rows.
		run(fixture.Exec("UPDATE public.ao_stored SET b=lhs WHERE id=1", 1))
		var replacement bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &replacement); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(replacement.Bytes())); err != nil {
			t.Fatal(err)
		}
		fixture.ArrayReferenceConsumeHeld(t, retained, check.Rows, check.Types)
		fixture.ScalarAssertDescription(t, cols, check.Columns, check.Types)
		check.Rows = [][]any{{int64(1), true, false, true, false}, {int64(2), true, false, true, false}, {int64(3), nil, nil, true, nil}}
		arrayCompareBoth(t, p, b, c, check)
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		arrayCompareBoth(t, p, b, c, enumCheck)
		var restored bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &restored); err != nil {
			t.Fatal(err)
		}
		if !bytes.Equal(replacement.Bytes(), restored.Bytes()) {
			t.Fatal("restore changed deterministic dump")
		}
		arrayOrderDomainRecreate(t, p, b, c, enumCheck)
	})
}

func arrayOrderDomainRecreate(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	var retained *sql.Rows
	if !b.PG {
		var err error
		retained, err = c.QueryContext(p.Ctx, check.Query)
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
	}
	run(fixture.Exec("DROP TABLE public.ao_enum_owner CASCADE", 0))
	run(fixture.Exec("DROP DOMAIN public.ao_stored_enum_leaf", 0))
	// Enum deletion is independently excluded. Recreate the same domain and
	// stored owner names over a new enum provider with reversed declaration rank.
	run(fixture.Exec("CREATE TYPE public.ao_stored_enum_next AS ENUM ('a','z')", 0))
	run(fixture.Exec("CREATE DOMAIN public.ao_stored_enum_leaf AS public.ao_stored_enum_next", 0))
	run(fixture.Exec("CREATE TABLE public.ao_enum_owner(id int,a public.ao_stored_enum_leaf,b public.ao_stored_enum_leaf,g bool GENERATED ALWAYS AS (ARRAY[a]<ARRAY[b]) STORED)", 0))
	run(fixture.Exec("CREATE VIEW public.ao_enum_view AS SELECT id,ARRAY[a]=ARRAY[b] AS eq,g FROM public.ao_enum_owner", 0))
	run(fixture.Prepared(fixture.Exec("INSERT INTO public.ao_enum_owner(id,a,b) VALUES($1,$2,$3)", 1, 1, "z", "a")))
	if retained != nil {
		fixture.ArrayReferenceConsumeHeld(t, retained, check.Rows, check.Types)
	}
	check.Rows = [][]any{{int64(1), false, false}}
	arrayCompareBoth(t, p, b, c, check)
	arrayCompareBoth(t, p, b, c, arrayCompareBool("SELECT ARRAY['a'::public.ao_stored_enum]<ARRAY['z'::public.ao_stored_enum],ARRAY['a'::public.ao_stored_enum_next]<ARRAY['z'::public.ao_stored_enum_next]", []any{false, true}))
}

func TestReferenceArrayOrderCancellation(t *testing.T) {
	arrayOrderScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ao_cancel(a int[],b int[])", 0))
		run(fixture.Exec("INSERT INTO ao_cancel VALUES('{1}','{1}')", 1))
		const recovery = "SELECT a=b AS eq FROM ao_cancel"
		held, err := c.PrepareContext(p.Ctx, recovery)
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		const q = "SELECT pg_sleep(30) FROM ao_cancel WHERE a=b"
		request, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer request.Close()
		// Public admitted request cancellation; provider traversal has separate units.
		for _, prepared := range []bool{false, true} {
			fixture.SleepActiveCancel(t, p, b, c, q, nil, func(ctx context.Context) fixture.RelationOutcome {
				var rows *sql.Rows
				var err error
				if prepared {
					rows, err = request.QueryContext(ctx)
				} else {
					rows, err = c.QueryContext(ctx, q)
				}
				return fixture.RelationOutcome{Rows: rows, Err: err}
			})
			run(fixture.Select(recovery, []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: held, SQL: recovery}, b.Recorder, fixture.Select(recovery, []string{"eq"}, []string{"BOOL"}, [][]any{{true}}))
		}
	})
}
