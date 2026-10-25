package reference_test

import (
	"bytes"
	"database/sql"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func circleControlCycle(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, check fixture.Step, replaySteps ...fixture.Step) {
	t.Helper()
	if b.PG {
		return
	} // Native controls have no PostgreSQL API equivalent.
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=0", 0))
	var dump bytes.Buffer
	if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(dump.String(), "CREATE TYPE") || strings.Contains(dump.String(), "CREATE EXTENSION") {
		t.Fatal("builtin circle emitted standalone", dump.String())
	}
	retained, err := c.QueryContext(p.Ctx, check.Query)
	if err != nil {
		t.Fatal(err)
	}
	defer retained.Close()
	metadata, err := retained.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
		t.Fatal(err)
	}
	if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
		t.Fatal(err)
	}
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET extra_float_digits=3", 0))
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Prepared(check))
	var old [][]any
	for retained.Next() {
		v := make([]any, len(check.Columns))
		dest := make([]any, len(v))
		for i := range v {
			dest[i] = &v[i]
		}
		if err := retained.Scan(dest...); err != nil {
			t.Fatal(err)
		}
		old = append(old, v)
	}
	if err := retained.Err(); err != nil {
		t.Fatal(err)
	}
	retained.Close()
	// The low-precision cursor is detached from the newly restored full value.
	wantOld := make([][]any, len(check.Rows))
	for i, row := range check.Rows {
		wantOld[i] = append([]any(nil), row...)
		for j, v := range row {
			if s, ok := v.(string); ok {
				wantOld[i][j] = strings.ReplaceAll(strings.ReplaceAll(s, "0.10000000000000002", "0.1"), "5e-324", "4.94065645841247e-324")
			}
		}
	}
	if !reflect.DeepEqual(old, wantOld) {
		t.Fatalf("retained rows %#v want %#v", old, wantOld)
	}
	for i, col := range metadata {
		if col.Name() != check.Columns[i] || col.DatabaseTypeName() != check.Types[i] {
			t.Fatalf("retained metadata %s/%s", col.Name(), col.DatabaseTypeName())
		}
	}
	bad := "CREATE TABLE circle_staged(v circle); INSERT INTO circle_staged VALUES('0,0,1'); INSERT INTO circle_staged VALUES('bad');"
	want := circleInputDiagnostic("bad")
	want.Position = strconv.Itoa(strings.Index(bad, "'bad'") + 1)
	fixture.AssertDiagnostic(t, stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(bad)), &want)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, check)
	fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='circle_staged'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	if p.Postgres != nil {
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
			t.Fatal("actual circle dump SQL oracle replay", err)
		}
		pg := fixture.Backend{PG: true}
		pgCheck := circleSelect(pg, check.Query, check.Columns, check.Types, check.Rows)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Exec("SET extra_float_digits=3", 0))
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, pgCheck)
		fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, fixture.Prepared(pgCheck))
		for _, step := range replaySteps {
			fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, step)
		}
		if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
			t.Fatal(err)
		}
	}
}

func TestReferenceCircleControls(t *testing.T) {
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_controls(id integer NOT NULL,v circle,r double precision GENERATED ALWAYS AS (radius(v)) STORED,same_area boolean GENERATED ALWAYS AS (v = '9,9,2'::circle) STORED,CHECK (radius(v)>=0 OR v IS NULL))", 0))
		run(fixture.Exec("INSERT INTO circle_controls(id,v) VALUES(0,'0,0,2'),(1,'1,2,3'),(2,NULL),(3,'0.10000000000000002,0,1'),(4,'5e-324,0,1'),(5,'-0,0,-0'),(6,'NaN,Infinity,NaN')", 7))
		// Equal radii have equal areas despite different centers; unequal finite
		// areas and NaN compare false, while a NULL input remains unknown.
		check := circleSelect(b, "SELECT id,v,same_area FROM circle_controls ORDER BY id", []string{"id", "v", "same_area"}, []string{"INT4", "CIRCLE", "BOOL"}, [][]any{{int64(0), "<(0,0),2>", true}, {int64(1), "<(1,2),3>", false}, {int64(2), nil, nil}, {int64(3), "<(0.10000000000000002,0),1>", false}, {int64(4), "<(5e-324,0),1>", false}, {int64(5), "<(-0,0),-0>", false}, {int64(6), "<(NaN,Infinity),NaN>", false}})
		run(check)
		run(fixture.Prepared(check))
		run(fixture.Select("SELECT r::text AS radius FROM circle_controls ORDER BY id", []string{"radius"}, []string{"TEXT"}, [][]any{{"2"}, {"3"}, {nil}, {"1"}, {"1"}, {"-0"}, {"NaN"}}))
		// Execute these same inserts/checks after native restoration and after
		// actual dump replay in PostgreSQL, not just against persisted results.
		inserted := fixture.Select("SELECT id,r,same_area FROM circle_controls WHERE id>=7 ORDER BY id", []string{"id", "r", "same_area"}, []string{"INT4", "FLOAT8", "BOOL"}, [][]any{{int64(7), float64(6), false}, {int64(8), float64(2), true}})
		later := []fixture.Step{
			fixture.Exec("INSERT INTO circle_controls(id,v) VALUES(7,'4,5,6')", 1),
			fixture.Exec("INSERT INTO circle_controls(id,v) VALUES(8,'4,5,2')", 1),
			inserted,
			fixture.Prepared(inserted),
		}
		circleControlCycle(t, p, b, c, check, later...)
		for _, step := range later {
			run(step)
		}
	})
}

func TestReferenceCircleFeatureSmoke(t *testing.T) {
	circleScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE circle_smoke(id integer NOT NULL,v circle)", 0))
		for i, v := range []string{"<(1,2),3>", "((1,2),3)", "(1,2),3", "1,2,3"} {
			run(fixture.Exec("INSERT INTO circle_smoke VALUES($1,$2)", 1, i+1, v))
		}
		q := "INSERT INTO circle_smoke VALUES($1,$2)"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for i, v := range []any{"4,5,6", []byte("7,8,9"), nil} {
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, i+5, v))
		}
		check := circleSelect(b, "SELECT id,v,radius(v) AS r FROM circle_smoke ORDER BY id", []string{"id", "v", "r"}, []string{"INT4", "CIRCLE", "FLOAT8"}, [][]any{{int64(1), "<(1,2),3>", float64(3)}, {int64(2), "<(1,2),3>", float64(3)}, {int64(3), "<(1,2),3>", float64(3)}, {int64(4), "<(1,2),3>", float64(3)}, {int64(5), "<(4,5),6>", float64(6)}, {int64(6), "<(7,8),9>", float64(9)}, {int64(7), nil, nil}})
		run(check)
		run(fixture.Select("SELECT '0,0,2'::circle='9,9,2'::circle AS area,'0,0,2'::circle~='9,9,2'::circle AS same,NULL::circle='0,0,2'::circle AS unknown,'0,0,1'::circle<->'5,0,1'::circle AS distance", []string{"area", "same", "unknown", "distance"}, []string{"BOOL", "BOOL", "BOOL", "FLOAT8"}, [][]any{{true, false, nil, float64(3)}}))
		want := circleInputDiagnostic("0,0,-1")
		want.Context = "unnamed portal parameter $1 = '...'"
		run(fixture.ErrorDiagnostic("INSERT INTO circle_smoke VALUES(8,$1)", want, "0,0,-1"))
		run(fixture.Select("SELECT count(*) AS n FROM circle_smoke", []string{"n"}, []string{"INT8"}, [][]any{{int64(7)}}))
		circleControlCycle(t, p, b, c, check)
	})
}
