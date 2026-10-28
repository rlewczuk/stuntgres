package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceArrayRelationalHeldLifecycle(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		const q = "SELECT x,y,n FROM unnest($1::int[],$2::text[]) WITH ORDINALITY AS u(x,y,n) ORDER BY n"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for range 2 {
			for _, tc := range []struct {
				a, z any
				rows [][]any
			}{
				{"[0:1]={1,NULL}", "{a,b,c}", [][]any{{int64(1), "a", int64(1)}, {nil, "b", int64(2)}, {nil, "c", int64(3)}}},
				{"{}", nil, [][]any{}}, {nil, "{}", [][]any{}}, {"{7}", "{z}", [][]any{{int64(7), "z", int64(1)}}},
			} {
				s := fixture.Select(q, []string{"x", "y", "n"}, []string{"INT4", "TEXT", "INT8"}, tc.rows, tc.a, tc.z)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		bad := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		bad.Detail, bad.Context = `Unexpected "," character.`, "unnamed portal parameter $1 = '...'"
		s := fixture.ErrorDiagnostic(q, bad, "{1,,2}", "{a}")
		s.SelectRows, s.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		good := fixture.Select(q, []string{"x", "y", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(9), "r", int64(1)}}, "{9}", "{r}")
		fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
		const sample = "SELECT array_sample($1::int[],$2::int) AS v"
		sampling, err := c.PrepareContext(p.Ctx, sample)
		if err != nil {
			t.Fatal(err)
		}
		defer sampling.Close()
		sampler := fixture.HeldStatement{Executor: c, Statement: sampling, SQL: sample}
		for _, tc := range []struct {
			a, n any
			want any
		}{{"{7}", 1, "{7}"}, {"{7}", 0, "{}"}, {"{}", 0, "{}"}, {nil, -1, nil}, {"{7}", nil, nil}} {
			fixture.RunStep(t, p.Ctx, sampler, b.Recorder, fixture.Select(sample, []string{"v"}, []string{"_INT4"}, [][]any{{tc.want}}, tc.a, tc.n))
		}
		countError := fixture.ErrorDiagnostic(sample, fixture.ControlsReferenceDiagnostic("22023", "sample size must be between 0 and 1"), "{7}", 2)
		countError.SelectRows, countError.ErrorStage = true, "execute"
		fixture.RunStep(t, p.Ctx, sampler, b.Recorder, countError)
		fixture.RunStep(t, p.Ctx, sampler, b.Recorder, fixture.Select(sample, []string{"v"}, []string{"_INT4"}, [][]any{{"{7}"}}, "{7}", 1))
		input := []byte("[0:1]={8,NULL}")
		retained, err := st.QueryContext(p.Ctx, input, "{a,b,c}")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		for i := range input {
			input[i] = 'x'
		}
		fixture.ArrayReferenceConsumeHeld(t, retained, [][]any{{int64(8), "a", int64(1)}, {nil, "b", int64(2)}, {nil, "c", int64(3)}}, []string{"INT4", "TEXT", "INT8"})
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE ar_atomic_source(id INT,a INT[],n INT)", 0))
		run(fixture.Exec("INSERT INTO ar_atomic_source VALUES (1,'{1,2}',1),(2,'{3}',2)", 2))
		run(fixture.Exec("CREATE TABLE ar_atomic_target(id INT,a INT[])", 0))
		run(fixture.Exec("INSERT INTO ar_atomic_target VALUES (0,'{99}')", 1))
		for _, prepared := range []bool{false, true} {
			failed := fixture.ErrorDiagnostic("INSERT INTO ar_atomic_target SELECT id,array_sample(a,n) FROM ar_atomic_source ORDER BY id", fixture.ControlsReferenceDiagnostic("22023", "sample size must be between 0 and 1"))
			failed.Prepared, failed.ErrorStage = prepared, "execute"
			run(failed)
			run(fixture.Select("SELECT id,a FROM ar_atomic_target ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, [][]any{{int64(0), "{99}"}}))
		}
		// Retain the stored operand across both random functions: no payload mutation.
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,cardinality(array_shuffle(a)) AS n,cardinality(array_sample(a,1)) AS m FROM ar_atomic_source ORDER BY id", []string{"id", "n", "m"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(1)}, {int64(2), int64(1), int64(1)}}))
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT id,a FROM ar_atomic_source ORDER BY id", []string{"id", "a"}, []string{"INT4", "_INT4"}, [][]any{{int64(1), "{1,2}"}, {int64(2), "{3}"}}))
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			for _, query := range []string{"SELECT array_shuffle(ARRAY[1,2])", "SELECT array_sample(ARRAY[1,2],1)", q} {
				args := []any(nil)
				if query == q {
					args = []any{"{1,2}", "{a}"}
				}
				rows, err := c.QueryContext(ctx, query, args...)
				if rows != nil {
					rows.Close()
					t.Fatal("precancel published cursor")
				}
				if !errors.Is(err, context.Canceled) {
					t.Fatalf("precancel error %v", err)
				}
			}
			fixture.RunStep(t, p.Ctx, held, b.Recorder, good)
			// Shared public native controls additionally prove retained result/schema
			// ownership across Reset/Restore and independently reminted catalogs.
			fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, fixture.Select("SELECT id,x,u.n FROM ar_atomic_source,unnest(a) WITH ORDINALITY AS u(x,n) ORDER BY id,u.n", []string{"id", "x", "n"}, []string{"INT4", "INT4", "INT8"}, [][]any{{int64(1), int64(1), int64(1)}, {int64(1), int64(2), int64(2)}, {int64(2), int64(3), int64(1)}}))
		}
	})
}

func TestReferenceArrayRelationalFeatureSmoke(t *testing.T) {
	arrayRelationalScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE ar_smoke(id INT,a INT[],b TEXT[])", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(fixture.Exec("INSERT INTO ar_smoke VALUES (1,$1,$2),(2,$3,$4)", 2, "[-2:-1][5:6]={{1,NULL},{3,4}}", "{a,b,c}", "{1,2}", "{a,b,c}")))
		unary := "SELECT x,n FROM ar_smoke,unnest(a) WITH ORDINALITY AS u(x,n) WHERE id=1 ORDER BY n"
		check := fixture.Select(unary, []string{"x", "n"}, []string{"INT4", "INT8"}, [][]any{{int64(1), int64(1)}, {nil, int64(2)}, {int64(3), int64(3)}, {int64(4), int64(4)}})
		arrayRelationalBoth(t, p, b, c, check)
		st, err := c.PrepareContext(p.Ctx, unary)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: unary}
		for range 2 {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE VIEW ar_smoke_view AS SELECT x,y,n FROM ar_smoke,unnest(a,b) WITH ORDINALITY AS u(x,y,n) WHERE id=2", 0))
		zipped := fixture.Select("SELECT x,y,n FROM ar_smoke_view ORDER BY n", []string{"x", "y", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{int64(1), "a", int64(1)}, {int64(2), "b", int64(2)}, {nil, "c", int64(3)}})
		arrayRelationalBoth(t, p, b, c, zipped)
		wrapper := fixture.Select("SELECT abs(unnest(ARRAY[-1,NULL,-3])) AS x", []string{"x"}, []string{"INT4"}, [][]any{{int64(1)}, {nil}, {int64(3)}})
		wrapper.Bag = true
		arrayRelationalBoth(t, p, b, c, wrapper)
		for _, q := range []string{"SELECT unnest(ARRAY[1],ARRAY['a'])", "SELECT * FROM pg_catalog.unnest(ARRAY[1],ARRAY['a'])"} {
			name, message := "unnest", "function unnest(integer[], text[]) does not exist"
			if q == "SELECT * FROM pg_catalog.unnest(ARRAY[1],ARRAY['a'])" {
				name, message = "pg_catalog.unnest", "function pg_catalog.unnest(integer[], text[]) does not exist"
			}
			arrayRelationalError(t, p, b, c, q, arrayInspectDiagnostic(q, name, "42883", message, arrayInspectHint), false)
		}
		arrayRelationalBoth(t, p, b, c, fixture.Select("SELECT cardinality(array_sample(a,1)) AS c,cardinality(array_shuffle(a)) AS s FROM ar_smoke WHERE id=1", []string{"c", "s"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(4)}}))
		for _, prepared := range []bool{false, true} {
			arrayRelationalRows(t, p, c, "SELECT array_sample(a,1),array_shuffle(a) FROM ar_smoke WHERE id=1", prepared, nil, func(rows *sql.Rows) {
				if !rows.Next() {
					t.Fatal("missing smoke slices")
				}
				var sample, shuffle string
				if err := rows.Scan(&sample, &shuffle); err != nil {
					t.Fatal(err)
				}
				if sample != "[1:1][5:6]={{1,NULL}}" && sample != "[1:1][5:6]={{3,4}}" {
					t.Fatalf("smoke sample lost slice/bounds: %q", sample)
				}
				if shuffle != "[-2:-1][5:6]={{1,NULL},{3,4}}" && shuffle != "[-2:-1][5:6]={{3,4},{1,NULL}}" {
					t.Fatalf("smoke shuffle lost slice/bounds: %q", shuffle)
				}
				if rows.Next() {
					t.Fatal("extra smoke slices")
				}
			})
		}
		arrayRelationalError(t, p, b, c, "SELECT array_sample(a,3) FROM ar_smoke WHERE id=1", fixture.ControlsReferenceDiagnostic("22023", "sample size must be between 0 and 2"), true)
		for _, q := range []string{"SELECT * FROM unnest(NULL::int[],'{}'::text[]) AS u(x,y)", "SELECT * FROM unnest('{}'::int[],NULL::text[]) AS u(x,y)"} {
			arrayRelationalBoth(t, p, b, c, fixture.Select(q, []string{"x", "y"}, []string{"INT4", "TEXT"}, [][]any{}))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, check)
		if !b.PG {
			arrayRelationalReplay(t, p, b, c, []fixture.Step{zipped}, []string{"ar_smoke"}, []string{"ar_smoke_view"})
		}
	})
}
