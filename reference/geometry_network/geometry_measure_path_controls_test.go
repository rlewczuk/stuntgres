package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func geometryMeasurePathAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE geom_measure_path_atomic(id integer,p path,g float8 GENERATED ALWAYS AS (pg_catalog.length(p)) STORED)", 0))
	run(fixture.Exec("INSERT INTO geom_measure_path_atomic(id,p) VALUES(0,path '[(0,0),(3,4)]')", 1))
	q := "INSERT INTO geom_measure_path_atomic(id,p) VALUES(1,$1::path),(2,$2::path)"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	good, bad := "[(0,0),(4,0),(0,3)]", "[(0,0),(1e308,0),(0,0)]"
	d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
	prior := func() {
		run(geometryMeasurePathStoredSelect(b, "SELECT id,p,g FROM geom_measure_path_atomic ORDER BY id", []string{"id", "p", "g"}, []string{"INT4", "PATH", "FLOAT8"}, [][]any{{int64(0), "[(0,0),(3,4)]", float64(5)}}))
	}
	s := fixture.ErrorDiagnostic(q, d, good, bad)
	run(s)
	prior()
	run(fixture.Prepared(s))
	prior()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	prior()
	run(fixture.Exec("BEGIN", 0))
	fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
	run(fixture.Exec("ROLLBACK", 0))
	prior()
	fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, good, good))
	run(geometryMeasurePathStoredSelect(b, "SELECT id,g FROM geom_measure_path_atomic ORDER BY id", []string{"id", "g"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(0), float64(5)}, {int64(1), float64(9)}, {int64(2), float64(9)}}))
	if !b.PG {
		canceled, cancel := context.WithCancel(p.Ctx)
		cancel()
		expired, expire := context.WithDeadline(p.Ctx, time.Unix(1, 0))
		defer expire()
		for _, tc := range []struct {
			ctx  context.Context
			want error
		}{{canceled, context.Canceled}, {expired, context.DeadlineExceeded}} {
			result, err := c.ExecContext(tc.ctx, "INSERT INTO geom_measure_path_atomic(id,p) VALUES(3,path '[(0,0),(3,4)]')")
			if result != nil || !errors.Is(err, tc.want) {
				t.Fatalf("canceled publication: %v/%v want %v", result, err, tc.want)
			}
		}
		run(fixture.Select("SELECT count(*) AS n FROM geom_measure_path_atomic WHERE id=3", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
	}
}

func TestReferenceGeometryMeasurePathControls(t *testing.T) {
	geometryMeasurePathScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		geometryMeasurePathAtomicity(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryMeasurePathCreateConsumers(run)
		geometryMeasurePathConsumerChecks(t, b, run)
		if b.PG {
			return
		} // Native replacement controls have no PostgreSQL API.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT closed,g FROM geom_measure_path_view WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		retained := geometryMeasurePathStoredSelect(b, "", []string{"closed", "g"}, []string{"PATH", "FLOAT8"}, [][]any{{"((0,0),(4,0),(0,3))", float64(12)}})
		retained.CheckMetadata(t, old)
		columns, err := old.ColumnTypes()
		if err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		geometryMeasurePathConsumerChecks(t, b, run)
		retained.CheckMetadata(t, old)
		if !old.Next() {
			t.Fatalf("old row: %v", old.Err())
		}
		var path, length any
		if err := old.Scan(&path, &length); err != nil {
			t.Fatal(err)
		}
		retained.CheckRaw(t, 0, 0, path)
		retained.CheckRaw(t, 1, 0, length)
		if path != "((0,0),(4,0),(0,3))" || length != float64(12) {
			t.Fatalf("old cells %v/%v", path, length)
		}
		if old.Next() || old.Err() != nil {
			t.Fatalf("old exhaustion: %v", old.Err())
		}
		old.Close()
		if len(columns) != 2 || columns[0].DatabaseTypeName() != "PATH" || columns[1].DatabaseTypeName() != "FLOAT8" {
			t.Fatalf("detached columns: %v", columns)
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }
		geometryMeasurePathConsumerChecks(t, b, targetRun)
		bad := "CREATE TABLE geom_measure_path_bad(p path,g float8 GENERATED ALWAYS AS (pg_catalog.length(p)) STORED); INSERT INTO geom_measure_path_bad(p) VALUES(path '[(0,0),(3,4)]'); INSERT INTO geom_measure_path_bad(p) VALUES(path '[(0,0),(1e308,0),(0,0)]');"
		d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		geometryMeasurePathConsumerChecks(t, b, targetRun)
		targetRun(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='geom_measure_path_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if err := stuntgres.Restore(target.Ctx, target.Native, strings.NewReader("CREATE TABLE geom_measure_path_bad(v integer); INSERT INTO geom_measure_path_bad VALUES (")); err == nil {
			t.Fatal("malformed restore succeeded")
		}
		geometryMeasurePathConsumerChecks(t, b, targetRun)
		targetRun(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='geom_measure_path_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		sentinel := errors.New("path measurement restore reader")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("reader: %v", err)
		}
		geometryMeasurePathConsumerChecks(t, b, targetRun)
		canceled, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled restore: %v", err)
		}
		geometryMeasurePathConsumerChecks(t, b, targetRun)
		if p.Postgres != nil {
			// Replay actual emitted bytes; never construct a facsimile of the dump.
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("emitted SQL: %v", err)
			}
			pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
			geometryMeasurePathConsumerChecks(t, fixture.Backend{PG: true}, pgRun)
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
		}
	})
}
