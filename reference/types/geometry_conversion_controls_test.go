package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"math"
	"reflect"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryConversionControlChecks(b fixture.Backend) []fixture.Step {
	return []fixture.Step{
		geometryConversionSelect(b, "SELECT id,c,radius(c) AS r FROM geom_conversion_control ORDER BY id", []string{"id", "c", "r"}, []string{"INT4", "CIRCLE", "FLOAT8"}, [][]any{{int64(1), "<(0,0),-1>", float64(-1)}, {int64(2), "<(2,3),-Infinity>", math.Inf(-1)}, {int64(3), nil, nil}, {int64(4), "<(3,4),5>", float64(5)}, {int64(5), "<(0.10000000000000002,5e-324),-1.0000000000000002>", float64(-1.0000000000000002)}}),
		geometryConversionSelect(b, "SELECT b,l FROM geom_conversion_view WHERE id=1", []string{"b", "l"}, []string{"BOX", "LINE"}, [][]any{{"(-0.7071067811865475,-0.7071067811865475),(0.7071067811865475,0.7071067811865475)", "{2,-1,1}"}}),
		fixture.Select("SELECT radius(d) AS r,radius((v).c) AS member FROM geom_conversion_carrier", []string{"r", "member"}, []string{"FLOAT8", "FLOAT8"}, [][]any{{float64(-1), float64(-1)}}),
		fixture.Select("SELECT radius((a[1]).c) AS r FROM geom_conversion_carrier", []string{"r"}, []string{"FLOAT8"}, [][]any{{float64(-1)}}),
		fixture.Select("SELECT radius(c) AS r FROM geom_conversion_materialized", []string{"r"}, []string{"FLOAT8"}, [][]any{{float64(-1)}}),
	}
}

func geometryConversionControlProbe(t *testing.T, b fixture.Backend, run func(fixture.Step)) {
	t.Helper()
	run(fixture.Exec("BEGIN", 0))
	run(fixture.Exec("INSERT INTO geom_conversion_control(id) VALUES(80)", 1))
	run(geometryConversionSelect(b, "SELECT c FROM geom_conversion_control WHERE id=80", []string{"c"}, []string{"CIRCLE"}, [][]any{{"<(0,0),-1>"}}))
	d := fixture.ControlsReferenceDiagnostic("23514", "new row for relation \"geom_conversion_control\" violates check constraint \"geom_conversion_control_radius\"")
	d.Schema, d.Table, d.Constraint = "public", "geom_conversion_control", "geom_conversion_control_radius"
	d.Detail = "Failing row contains (-1, <(0,0),-2>)."
	run(fixture.ErrorDiagnostic("INSERT INTO geom_conversion_control VALUES(-1,circle(point '(0,0)',-2))", d))
	run(fixture.Exec("ROLLBACK", 0))
	run(fixture.Select("SELECT count(*) AS n FROM geom_conversion_control WHERE id IN (-1,80,99)", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
}

func TestReferenceGeometryConversionControls(t *testing.T) {
	geometryConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		for _, q := range []string{
			"CREATE DOMAIN geom_conversion_circle AS circle",
			"CREATE TYPE geom_conversion_record AS (c circle)",
			"CREATE TABLE geom_conversion_control(id integer NOT NULL,c circle DEFAULT pg_catalog.circle(point '(0,0)',-1),CONSTRAINT geom_conversion_control_radius CHECK(id>0 OR radius(c)=-1))",
			"CREATE VIEW geom_conversion_view AS SELECT id,box(c) AS b,line(point '(1,3)',point '(3,7)') AS l FROM geom_conversion_control",
			"CREATE TABLE geom_conversion_carrier(d geom_conversion_circle,v geom_conversion_record,a geom_conversion_record[])",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("CREATE MATERIALIZED VIEW geom_conversion_materialized AS SELECT circle(point '(0,0)',-1) AS c", 1))
		run(fixture.Exec("INSERT INTO geom_conversion_control VALUES(1,circle(point '(0,0)',-1)),(2,circle(point '(2,3)','-Infinity'::float8)),(3,NULL),(4,circle(box '(6,8),(0,0)')),(5,circle(point '(0.10000000000000002,5e-324)','-1.0000000000000002'::float8))", 5))
		run(fixture.Exec("INSERT INTO geom_conversion_carrier VALUES(circle(point '(0,0)',-1)::geom_conversion_circle,ROW(circle(point '(0,0)',-1))::geom_conversion_record,ARRAY[ROW(circle(point '(0,0)',-1))::geom_conversion_record])", 1))
		verify := func(run func(fixture.Step), backend fixture.Backend) {
			for _, s := range geometryConversionControlChecks(backend) {
				run(s)
				run(fixture.Prepared(s))
			}
			geometryConversionControlProbe(t, backend, run)
		}
		verify(run, b)
		if b.PG {
			return
		} // Public native host controls have no PostgreSQL equivalent.
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO geom_conversion_control VALUES(99,circle(point '(987654321,0)',-1))", 1))
		var dump bytes.Buffer
		dumpErr := stuntgres.Dump(p.Ctx, p.Native, &dump)
		_, rollbackErr := c.ExecContext(p.Ctx, "ROLLBACK")
		if dumpErr != nil || rollbackErr != nil {
			t.Fatalf("dump %v rollback %v", dumpErr, rollbackErr)
		}
		if strings.Contains(dump.String(), "987654321") {
			t.Fatal("uncommitted row captured")
		}
		old, err := c.QueryContext(p.Ctx, "SELECT c FROM geom_conversion_control WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		verify(run, b)
		if cols, err := old.ColumnTypes(); err != nil || len(cols) != 1 || cols[0].DatabaseTypeName() != "CIRCLE" {
			t.Fatalf("retained metadata %v %v", cols, err)
		}
		if !old.Next() {
			t.Fatalf("retained row: %v", old.Err())
		}
		var retained any
		if err := old.Scan(&retained); err != nil {
			t.Fatal(err)
		}
		if !reflect.DeepEqual(retained, "<(0,0),-1>") {
			t.Fatalf("retained %T %#v", retained, retained)
		}
		if old.Next() || old.Err() != nil {
			t.Fatalf("retained exhaustion: %v", old.Err())
		}
		old.Close()
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }
		verify(targetRun, b)
		bad := "CREATE TABLE geom_bad(c circle); INSERT INTO geom_bad VALUES('<(0,0),-1>');"
		d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type circle: \"<(0,0),-1>\"")
		d.Position = fixture.Position(strings.Index(bad, "'<") + 1)
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		sentinel := errors.New("geometry conversion control I/O")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("read %v", err)
		}
		if err := stuntgres.Dump(target.Ctx, target.Native, boxFailWriter{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("write %v", err)
		}
		cancelled, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(cancelled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel %v", err)
		}
		verify(targetRun, b)
		for _, prepared := range []bool{false, true} {
			s := boxError("SELECT $1::circle AS c", "22P02", "invalid input syntax for type circle: \"<(0,0),-1>\"", "", "", prepared, true, "<(0,0),-1>")
			s.Diagnostic.Context = "unnamed portal parameter $1 = '...'"
			targetRun(s)
		}
		var again bytes.Buffer
		targetRun(fixture.Exec("SET extra_float_digits=0", 0))
		targetRun(fixture.Select("SELECT radius(c) AS r FROM geom_conversion_control WHERE id=5", []string{"r"}, []string{"FLOAT8"}, [][]any{{float64(-1.0000000000000002)}}))
		if err := stuntgres.Dump(target.Ctx, target.Native, &again); err != nil {
			t.Fatal(err)
		}
		// Definition casts may acquire equivalent parentheses during replay.
		// Verify exact consumer data, not incidental SQL text idempotence.
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(again.Bytes())); err != nil {
			t.Fatal(err)
		}
		targetRun(fixture.Exec("SET extra_float_digits=3", 0))
		verify(targetRun, b) // Exact center coordinates/radius, including subnormal Y.
		if p.Postgres != nil {
			// Borrow the oracle only for this replay phase; leave it clean
			// for the same scenario's ordinary PostgreSQL backend branch.
			defer func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("emitted SQL PostgreSQL replay: %v", err)
			}
			pg := fixture.Backend{PG: true}
			pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
			// PostgreSQL ignores native snapshot directives. Assert both
			// boundaries explicitly rather than mistaking recomputation for
			// native snapshot preservation (already checked above).
			pgRun(fixture.MaterializedViewUnpopulated("geom_conversion_materialized"))
			pgRun(fixture.Exec("REFRESH MATERIALIZED VIEW geom_conversion_materialized", 0))
			for _, s := range geometryConversionControlChecks(pg) {
				pgRun(s)
				pgRun(fixture.Prepared(s))
			}
			geometryConversionControlProbe(t, pg, pgRun)
		}
	})
}
