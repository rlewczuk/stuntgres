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

func geometryMeasureAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE geom_measure_atomic(id integer,v float8)", 0))
	run(fixture.Exec("INSERT INTO geom_measure_atomic VALUES(0,99)", 1))
	q := "INSERT INTO geom_measure_atomic VALUES(1,area($1::path)),(2,area($2::path))"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
	good, bad := "((0,0),(4,0),(0,3))", "((1e308,1e308))"
	checkPrior := func() {
		run(fixture.Select("SELECT id,v FROM geom_measure_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(0), float64(99)}}))
	}
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, good, bad))
	checkPrior()
	run(fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, good, bad))
	run(fixture.Exec("ROLLBACK", 0))
	checkPrior()
	run(fixture.ErrorDiagnostic("INSERT INTO geom_measure_atomic VALUES(1,area(path '((0,0),(4,0),(0,3))')),(2,area(circle '<(0,0),1e308>'))", d))
	checkPrior()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, good, good))
	run(fixture.Select("SELECT id,v FROM geom_measure_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(0), float64(99)}, {int64(1), float64(6)}, {int64(2), float64(6)}}))
	if !b.PG {
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		result, err := c.ExecContext(ctx, "INSERT INTO geom_measure_atomic VALUES(3,area(path '((0,0),(4,0),(0,3))'))")
		if !errors.Is(err, context.Canceled) || result != nil {
			t.Fatalf("precancel: result=%v error=%v", result, err)
		}
		run(fixture.Select("SELECT count(*) AS n FROM geom_measure_atomic WHERE id=3", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

func TestReferenceGeometryMeasureControls(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		geometryMeasureAtomicity(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryMeasureCreateConsumers(run)
		geometryMeasureConsumerChecks(t, b, run)
		if b.PG {
			return
		} // Public host replacement has no PostgreSQL API equivalent.
		run(fixture.Exec("SET extra_float_digits=0", 0))
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET extra_float_digits=3", 0))
		old, err := c.QueryContext(p.Ctx, "SELECT cc,ac FROM geom_measure_view WHERE id=5")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		retained := geometryMeasureSelect(b, "", []string{"cc", "ac"}, []string{"POINT", "FLOAT8"}, [][]any{{"(0.10000000000000002,5e-324)", float64(0)}})
		retained.CheckMetadata(t, old)
		ownedColumns, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("SET extra_float_digits=3", 0))
		geometryMeasureConsumerChecks(t, b, run)
		retained.CheckMetadata(t, old)
		if !old.Next() {
			t.Fatalf("retained row: %v", old.Err())
		}
		var point, area any
		if err := old.Scan(&point, &area); err != nil {
			t.Fatal(err)
		}
		retained.CheckRaw(t, 0, 0, point)
		retained.CheckRaw(t, 1, 0, area)
		if point != "(0.10000000000000002,5e-324)" || area != float64(0) {
			t.Fatalf("retained %v %v", point, area)
		}
		if old.Next() || old.Err() != nil {
			t.Fatalf("retained exhaustion: %v", old.Err())
		}
		old.Close()
		if len(ownedColumns) != 2 || ownedColumns[0].DatabaseTypeName() != "POINT" || ownedColumns[1].DatabaseTypeName() != "FLOAT8" {
			t.Fatalf("retained detached metadata: %v", ownedColumns)
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }
		targetRun(fixture.Exec("SET extra_float_digits=3", 0))
		geometryMeasureConsumerChecks(t, b, targetRun)
		bad := "CREATE TABLE geom_measure_bad(c circle,v float8 GENERATED ALWAYS AS (area(c)) STORED); INSERT INTO geom_measure_bad(c) VALUES(circle '<(0,0),2>'); INSERT INTO geom_measure_bad(c) VALUES(circle '<(0,0),1e308>');"
		d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		geometryMeasureConsumerChecks(t, b, targetRun)
		targetRun(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='geom_measure_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		sentinel := errors.New("geometry measurement restore I/O")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("read: %v", err)
		}
		geometryMeasureConsumerChecks(t, b, targetRun)
		cancelled, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(cancelled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled restore: %v", err)
		}
		geometryMeasureConsumerChecks(t, b, targetRun)
		if p.Postgres != nil {
			// Replay the actual emitted SQL, not a separately authored facsimile.
			defer func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("emitted SQL replay: %v", err)
			}
			pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
			pgRun(fixture.Exec("SET extra_float_digits=3", 0))
			geometryMeasureConsumerChecks(t, fixture.Backend{PG: true}, pgRun)
		}
	})
}
