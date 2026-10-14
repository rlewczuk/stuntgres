package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func arrayInspectControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	}
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"array_dims", "array_length", "array_lower", "array_upper", "array_ndims", "cardinality"} {
		if !strings.Contains(dump.String(), "pg_catalog."+name+"(") {
			t.Fatalf("dump lacks symbolic %s: %s", name, dump.String())
		}
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("dump PostgreSQL replay", err)
		}
		pgCheck := arrayInspectViewSelect(check.Query, check.Rows, true)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pgCheck))
		// Release replay objects before the same shared PostgreSQL body runs.
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP TABLE IF EXISTS ai_store,ai_store_renamed,ai_copy,ai_target,ai_atomic,ai_smoke CASCADE; DROP DOMAIN IF EXISTS ai_stored_array CASCADE"); err != nil {
			t.Fatal(err)
		}
	}
	fixture.ArrayReferenceNativeRoundtrip(t, p, c, b, check)
}

func TestReferenceArrayInspectConsumersControls(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN ai_stored_array AS INT[]",
			"CREATE TABLE ai_store(id INT,a ai_stored_array,n INT GENERATED ALWAYS AS (cardinality(a)) STORED,stamp INT DEFAULT cardinality(ARRAY[1,NULL]),CHECK(cardinality(a)>=0))",
			"CREATE INDEX ai_cardinality_idx ON ai_store ((cardinality(a)))",
			"CREATE VIEW ai_view AS SELECT " + arrayInspectTuple("a", "2") + ",id,n,stamp FROM ai_store",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO ai_store(id,a) VALUES (1,'"+arrayInspectInput+"'),(2,'{}'),(3,NULL)", 3))
		check := arrayInspectViewSelect("SELECT dims,len,lo,hi,rank,count FROM ai_view ORDER BY id", [][]any{
			{"[-2:-1][3:5]", int64(3), int64(3), int64(5), int64(2), int64(6)}, {nil, nil, nil, nil, nil, int64(0)}, {nil, nil, nil, nil, nil, nil},
		}, b.PG)
		arrayInspectBoth(t, p, b, c, check)
		viewStatement, viewError := c.PrepareContext(p.Ctx, check.Query)
		if viewError != nil {
			t.Fatal(viewError)
		}
		defer viewStatement.Close()
		viewHeld := fixture.HeldStatement{Executor: c, Statement: viewStatement, SQL: check.Query}
		fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, check)
		for _, s := range []fixture.Step{
			fixture.Select("SELECT id,n,stamp FROM ai_store ORDER BY id", []string{"id", "n", "stamp"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(6), int64(2)}, {int64(2), int64(0), int64(2)}, {int64(3), nil, int64(2)}}),
			fixture.Select("SELECT cardinality(a) AS n,count(*) AS copies FROM ai_store GROUP BY cardinality(a) ORDER BY n", []string{"n", "copies"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(6), int64(1)}, {nil, int64(1)}}),
			fixture.Select("SELECT count(cardinality(a)) AS n,sum(cardinality(a)) AS total FROM ai_store", []string{"n", "total"}, []string{"INT8", "INT8"}, [][]any{{int64(2), int64(6)}}),
			fixture.Select("SELECT CASE WHEN false THEN array_length(ARRAY[1],1/0) ELSE cardinality(ARRAY[1,NULL]) END AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}}),
			fixture.Select("SELECT l.id FROM ai_store l JOIN ai_store r ON cardinality(l.a)=r.n ORDER BY l.id", []string{"id"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}),
			fixture.Select("SELECT pg_get_indexdef('ai_cardinality_idx'::regclass) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{"CREATE INDEX ai_cardinality_idx ON public.ai_store USING btree (cardinality((a)::integer[]))"}}),
		} {
			arrayInspectBoth(t, p, b, c, s)
		}
		run(fixture.Exec("CREATE TABLE ai_copy AS SELECT id,cardinality(a) AS n FROM ai_store", 3))
		run(fixture.Exec("CREATE TABLE ai_target(id INT,n INT)", 0))
		returning := fixture.Select("INSERT INTO ai_target SELECT id,cardinality(a) FROM ai_store ORDER BY id RETURNING id,n", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(6)}, {int64(2), int64(0)}, {int64(3), nil}})
		run(returning)
		run(fixture.Exec("DELETE FROM ai_target", 3))
		run(fixture.Prepared(returning))
		arrayInspectBoth(t, p, b, c, fixture.Select("SELECT id,n FROM ai_copy ORDER BY id", returning.Columns, returning.Types, returning.Rows))
		run(fixture.Exec("CREATE TABLE ai_atomic(id INT,n INT)", 0))
		const q = "INSERT INTO ai_atomic VALUES (1,cardinality($1::int[])),(2,cardinality($2::int[]))"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		d := fixture.ControlsReferenceDiagnostic("22P02", `malformed array literal: "{1,,2}"`)
		d.Detail, d.Context = `Unexpected "," character.`, "unnamed portal parameter $2 = '...'"
		bad := fixture.ErrorDiagnostic(q, d, "{1}", "{1,,2}")
		bad.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, held, b.Recorder, bad)
		arrayInspectBoth(t, p, b, c, fixture.Select("SELECT id,n FROM ai_atomic ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{}))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, "{1}", "{}"))
		arrayInspectBoth(t, p, b, c, fixture.Select("SELECT id,n FROM ai_atomic ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(1)}, {int64(2), int64(0)}}))
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		rows, err := b.DB.QueryContext(ctx, check.Query)
		if rows != nil {
			rows.Close()
			t.Fatal("canceled query published rows")
		}
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled query: %v", err)
		}
		// Exercise captured view identities after source-column and table remapping.
		run(fixture.Exec("ALTER TABLE ai_store RENAME COLUMN a TO choices", 0))
		run(fixture.Exec("ALTER TABLE ai_store RENAME TO ai_store_renamed", 0))
		fixture.RunStep(t, p.Ctx, viewHeld, b.Recorder, check)
		arrayInspectBoth(t, p, b, c, check)
		const viewDefinition = " SELECT array_dims(choices::integer[]) AS dims,\n    array_length(choices::integer[], 2) AS len,\n    array_lower(choices::integer[], 2) AS lo,\n    array_upper(choices::integer[], 2) AS hi,\n    array_ndims(choices::integer[]) AS rank,\n    cardinality(choices::integer[]) AS count,\n    id,\n    n,\n    stamp\n   FROM ai_store_renamed;"
		arrayInspectBoth(t, p, b, c, fixture.Select("SELECT pg_get_viewdef('ai_view'::regclass,true) AS definition", []string{"definition"}, []string{"TEXT"}, [][]any{{viewDefinition}}))
		arrayInspectControls(t, p, b, c, check)
	})
}

func TestReferenceArrayInspectFeatureSmoke(t *testing.T) {
	arrayInspectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{"CREATE TABLE ai_smoke(id INT,a INT[])", "CREATE VIEW ai_smoke_view AS SELECT " + arrayInspectTuple("a", "2") + ",id FROM ai_smoke"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO ai_smoke VALUES (1,'"+arrayInspectInput+"'),(2,'{}'),(3,NULL)", 3))
		want := [][]any{{"[-2:-1][3:5]", int64(3), int64(3), int64(5), int64(2), int64(6)}, {nil, nil, nil, nil, nil, int64(0)}, {nil, nil, nil, nil, nil, nil}}
		arrayInspectBoth(t, p, b, c, arrayInspectSelect("SELECT "+arrayInspectTuple("a", "2")+" FROM ai_smoke ORDER BY id", want, b.PG))
		check := arrayInspectViewSelect("SELECT dims,len,lo,hi,rank,count FROM ai_smoke_view ORDER BY id", want, b.PG)
		arrayInspectBoth(t, p, b, c, check)
		arrayInspectHeld(t, p, b, c)
		q := "SELECT array_dims(1)"
		arrayInspectDiagnosticBoth(t, p, b, c, q, arrayInspectDiagnostic(q, "array_dims", "42883", "function array_dims(integer) does not exist", arrayInspectHint))
		q = "SELECT array_length('{1}'::int[], 'bad')"
		arrayInspectDiagnosticBoth(t, p, b, c, q, arrayInspectDiagnostic(q, "'bad'", "22P02", `invalid input syntax for type integer: "bad"`, ""))
		arrayInspectControls(t, p, b, c, check)
	})
}
