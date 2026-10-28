package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func jsonArrayLengthStored(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) fixture.Step {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		`CREATE TABLE jal_stored(id int,j json,b jsonb,d int DEFAULT json_array_length('[1,2]'),g int GENERATED ALWAYS AS (json_array_length(j)) STORED,h int GENERATED ALWAYS AS (jsonb_array_length(b)) VIRTUAL,CHECK(json_array_length(j)>=0))`,
		`CREATE INDEX jal_stored_idx ON jal_stored ((json_array_length(j)))`,
		`CREATE INDEX jal_stored_b_idx ON jal_stored ((jsonb_array_length(b)))`,
		`CREATE VIEW jal_view AS SELECT id,json_array_length(j) AS one,jsonb_array_length(b) AS two FROM jal_stored`,
	} {
		run(fixture.Exec(q, 0))
	}
	run(fixture.Exec("INSERT INTO jal_stored(id,j,b) VALUES(1,'[]','[1]'),(2,'[1,2]','[]'),(3,NULL,NULL)", 3))
	check := fixture.Select("SELECT id,d,g,h FROM jal_stored ORDER BY id", []string{"id", "d", "g", "h"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(2), int64(0), int64(1)}, {int64(2), int64(2), int64(2), int64(0)}, {int64(3), int64(2), nil, nil}})
	jsonArrayLengthBoth(t, p, b, c, check)
	jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT id,one,two FROM jal_view ORDER BY id", []string{"id", "one", "two"}, []string{"INT4", "INT4", "INT4"}, [][]any{{int64(1), int64(0), int64(1)}, {int64(2), int64(2), int64(0)}, {int64(3), nil, nil}}))
	jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT id FROM jal_stored WHERE json_array_length(j)=2", []string{"id"}, []string{"INT4"}, [][]any{{int64(2)}}))
	return check
}

func jsonArrayLengthAtomic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	for _, q := range []string{
		"INSERT INTO jal_stored(id,j,b) VALUES(4,'[1]','[]'),(5,'{}','[]')",
		"UPDATE jal_stored SET j=CASE WHEN id=1 THEN '[9]'::json ELSE '{}'::json END WHERE id<=2",
		"INSERT INTO jal_stored(id,j,b) VALUES(4,'[1]','[]'),(5,'[]','{}')",
		"UPDATE jal_stored SET b=CASE WHEN id=1 THEN '[9]'::jsonb ELSE '{}'::jsonb END WHERE id<=2",
	} {
		s := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("22023", "cannot get array length of a non-array"))
		s.ErrorStage = "execute"
		jsonArrayLengthBoth(t, p, b, c, s)
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	}
}

func TestReferenceJSONArrayLengthConsumers(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonArrayLengthStored(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		group := fixture.Select("SELECT json_array_length(j) AS n,count(*) AS count FROM jal_stored GROUP BY json_array_length(j)", []string{"n", "count"}, []string{"INT4", "INT8"}, [][]any{{int64(0), int64(1)}, {int64(2), int64(1)}, {nil, int64(1)}})
		group.Bag = true
		jsonArrayLengthBoth(t, p, b, c, group)
		jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT min(json_array_length(j)) AS jlo,max(json_array_length(j)) AS jhi,min(jsonb_array_length(b)) AS blo,max(jsonb_array_length(b)) AS bhi FROM jal_stored", []string{"jlo", "jhi", "blo", "bhi"}, []string{"INT4", "INT4", "INT4", "INT4"}, [][]any{{int64(0), int64(2), int64(0), int64(1)}}))
		for _, q := range []string{
			"CREATE MATERIALIZED VIEW jal_mat AS SELECT id,json_array_length(j) AS n FROM jal_stored",
			"CREATE TABLE jal_ctas AS SELECT id,json_array_length(j) AS n FROM jal_stored",
		} {
			run(fixture.Exec(q, 3))
		}
		for _, table := range []string{"jal_mat", "jal_ctas"} {
			jsonArrayLengthBoth(t, p, b, c, fixture.Select("SELECT id,n FROM "+table+" ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(0)}, {int64(2), int64(2)}, {int64(3), nil}}))
		}
		jsonArrayLengthAtomic(t, p, b, c, check)
		run(fixture.Select("INSERT INTO jal_stored(id,j,b) VALUES(4,'[null]','[{},[]]') RETURNING json_array_length(j) AS one,jsonb_array_length(b) AS two", []string{"one", "two"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}))
		run(fixture.Prepared(fixture.Select("UPDATE jal_stored SET j=$1,b=$2 WHERE id=4 RETURNING g,h", []string{"g", "h"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(0)}}, "[1,2]", "[]")))
		run(fixture.Exec("DELETE FROM jal_stored WHERE id=4", 1))
		run(check)
		// Exact symbolic definitions retain calls and typed arguments.
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"default_generated", fixture.Select(`SELECT d.adnum,pg_get_expr(d.adbin,d.adrelid,false) AS expression FROM pg_catalog.pg_attrdef d JOIN pg_catalog.pg_class r ON d.adrelid=r.oid WHERE r.relname='jal_stored' ORDER BY d.adnum`, []string{"adnum", "expression"}, []string{"INT2", "TEXT"}, [][]any{{int64(4), `json_array_length('[1,2]'::json)`}, {int64(5), `json_array_length(j)`}, {int64(6), `jsonb_array_length(b)`}})},
			{"check", fixture.Select(`SELECT pg_get_expr(c.conbin,c.conrelid,false) AS expression FROM pg_catalog.pg_constraint c JOIN pg_catalog.pg_class r ON c.conrelid=r.oid WHERE r.relname='jal_stored' AND c.contype='c'`, []string{"expression"}, []string{"TEXT"}, [][]any{{`(json_array_length(j) >= 0)`}})},
			{"view", fixture.Select(`SELECT pg_get_viewdef('jal_view'::regclass,false) AS definition`, []string{"definition"}, []string{"TEXT"}, [][]any{{" SELECT id,\n    json_array_length(j) AS one,\n    jsonb_array_length(b) AS two\n   FROM jal_stored;"}})},
			{"index", fixture.Select(`SELECT pg_get_indexdef('jal_stored_idx'::regclass,0,false) AS definition`, []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX jal_stored_idx ON public.jal_stored USING btree (json_array_length(j))`}})},
			{"jsonb_index", fixture.Select(`SELECT pg_get_indexdef('jal_stored_b_idx'::regclass,0,false) AS definition`, []string{"definition"}, []string{"TEXT"}, [][]any{{`CREATE INDEX jal_stored_b_idx ON public.jal_stored USING btree (jsonb_array_length(b))`}})},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonArrayLengthBoth(t, p, b, c, tc.step) })
		}
	})
}

func jsonArrayLengthControls(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Native controls have no PostgreSQL API counterpart.
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	old, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	meta, err := old.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jal_replacement(n int)", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jal_replacement VALUES(99)", 1))
	fixture.StringCoreRetainedRows(t, old, nil, check.Columns, check.Types, check.Rows, false)
	if len(meta) != len(check.Types) {
		t.Fatal(meta)
	}
	for i, ct := range meta {
		if ct.DatabaseTypeName() != check.Types[i] {
			t.Fatal(ct)
		}
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	jsonArrayLengthBoth(t, p, b, c, check)
	var redump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &redump); err != nil {
		t.Fatal(err)
	}
	if dump.String() != redump.String() {
		t.Fatalf("redump changed\n%s\n%s", dump.String(), redump.String())
	}
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual dump replay", err)
		}
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, check)
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
	unchanged := func() {
		var after bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
			t.Fatal(err)
		}
		if after.String() != dump.String() {
			t.Fatal("failed Restore published replacement")
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	}
	for _, f := range []string{"json_array_length", "jsonb_array_length"} {
		script := "CREATE TABLE jal_bad(n int); INSERT INTO jal_bad VALUES(" + f + "('[1]')); INSERT INTO jal_bad VALUES(" + f + "('{}'));"
		d := fixture.ControlsReferenceDiagnostic("22023", "cannot get array length of a non-array")
		fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
		unchanged()
		for _, arg := range []string{"$1", "CASE WHEN TRUE THEN $1 ELSE '[]' END"} {
			script = "/*é*/ CREATE TABLE jal_bad(n int); INSERT INTO jal_bad VALUES(" + f + "(" + arg + "));"
			d = fixture.ControlsReferenceDiagnostic("42P02", "parameters are not allowed in restore")
			d.Position = fixture.Position(len([]rune(script[:strings.Index(script, "$1")])) + 1)
			fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(script)), &d)
			unchanged()
		}
	}
	valid := "CREATE TABLE jal_restored(j int,b int); INSERT INTO jal_restored VALUES(json_array_length('[[],{}]'),jsonb_array_length('[null]'));"
	if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(valid)); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT j,b FROM jal_restored", []string{"j", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(2), int64(1)}}))
}

func TestReferenceJSONArrayLengthControls(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		check := jsonArrayLengthStored(t, p, b, c)
		jsonArrayLengthControls(t, p, b, c, check)
	})
}

func TestReferenceJSONArrayLengthFeatureSmoke(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonArrayLengthBoth(t, p, b, c, fixture.Select(`SELECT json_array_length('[]') AS j,jsonb_array_length('[1,2,3,{"f1":1,"f2":[5,6]},4]') AS b`, []string{"j", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(5)}}))
		for _, f := range []string{"json_array_length", "jsonb_array_length"} {
			jsonArrayLengthFrames(t, p, b, c, f)
		}
		check := jsonArrayLengthStored(t, p, b, c)
		jsonArrayLengthAtomic(t, p, b, c, check)
		jsonArrayLengthControls(t, p, b, c, check)
	})
}

func TestReferenceJSONArrayLengthCancellation(t *testing.T) {
	jsonArrayLengthScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jal_active_input(id int,j json)", 0))
		run(fixture.Exec("CREATE TABLE jal_active_out(id int,n int)", 0))
		run(fixture.Exec("INSERT INTO jal_active_out VALUES(0,99)", 1))
		// A retained 32MiB JSON token forces genuine lexer byte polling. Repeated
		// rows sustain server admission without sleeps, hooks or substitute SQL.
		large := `["` + strings.Repeat("x", 32<<20) + `"]`
		run(fixture.Exec("INSERT INTO jal_active_input VALUES(1,'[]'),(2,'[]'),(3,'[]'),(4,'[]'),(5,'[]'),(6,'[]'),(7,'[]'),(8,'[]'),(9,'[]')", 9))
		// Import/validate the immutable token once, then share it across the
		// same eight large rows instead of reparsing 32MiB per setup statement.
		run(fixture.Exec("UPDATE jal_active_input SET j=$1 WHERE id>1", 8, large))
		readSQL := "SELECT sum(json_array_length(j)) AS n FROM jal_active_input"
		writeSQL := "INSERT INTO jal_active_out(id,n) SELECT id,json_array_length(j) FROM jal_active_input ORDER BY id"
		read, err := c.PrepareContext(p.Ctx, readSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer read.Close()
		write, err := c.PrepareContext(p.Ctx, writeSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer write.Close()
		if !b.PG {
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			rows, err := read.QueryContext(ctx)
			if rows != nil {
				rows.Close()
				t.Fatal("precanceled read published cursor")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
			result, err := write.ExecContext(ctx)
			if result != nil {
				t.Fatal("precanceled write published result")
			}
			fixture.LexicalOrdinary(t, err, context.Canceled)
		}
		fixture.BinaryConversionActiveCancel(t, p, b, c, readSQL, stuntgres.WorkJSONArrayCount, func(ctx context.Context) fixture.RelationOutcome {
			rows, err := read.QueryContext(ctx)
			return fixture.RelationOutcome{Rows: rows, Err: err}
		})
		run(fixture.Exec("UPDATE jal_active_input SET j='[]' WHERE id>1", 8))
		heldRead := fixture.HeldStatement{Executor: c, Statement: read, SQL: readSQL}
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("UPDATE jal_active_input SET j=$1 WHERE id>1", 8, large))
		fixture.BinaryConversionActiveCancel(t, p, b, c, writeSQL, stuntgres.WorkJSONArrayCount, func(ctx context.Context) fixture.RelationOutcome {
			result, err := write.ExecContext(ctx)
			return fixture.RelationOutcome{Result: result, Err: err}
		})
		run(fixture.Select("SELECT id,n FROM jal_active_out ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(0), int64(99)}}))
		run(fixture.Exec("UPDATE jal_active_input SET j='[]' WHERE id>1", 8))
		fixture.RunStep(t, p.Ctx, heldRead, b.Recorder, fixture.Select(readSQL, []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: write, SQL: writeSQL}, b.Recorder, fixture.Exec(writeSQL, 9))
		want := [][]any{{int64(0), int64(99)}}
		for id := int64(1); id <= 9; id++ {
			want = append(want, []any{id, int64(0)})
		}
		run(fixture.Select("SELECT id,n FROM jal_active_out ORDER BY id", []string{"id", "n"}, []string{"INT4", "INT4"}, want))
	})
}
