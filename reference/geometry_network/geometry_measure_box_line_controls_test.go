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

func geometryMeasureBoxLineAtomicity(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec("CREATE TABLE geom_box_line_atomic(id integer,v float8)", 0))
	run(fixture.Exec("INSERT INTO geom_box_line_atomic VALUES(0,99)", 1))
	for _, tc := range []struct {
		name, call string
		good, bad  []any
		want       float64
	}{
		{"width", "width($1::box)", []any{"(4,6),(1,2)"}, []any{"(1e308,0),(-1e308,0)"}, 3},
		{"height", "height($1::box)", []any{"(4,6),(1,2)"}, []any{"(0,1e308),(0,-1e308)"}, 4},
		{"slope", "slope($1::point,$2::point)", []any{"(0,0)", "(2,1)"}, []any{"(0,1e308)", "(1,-1e308)"}, 0.5},
	} {
		t.Run(tc.name, func(t *testing.T) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			second := strings.ReplaceAll(tc.call, "$1", "$2")
			if tc.name == "slope" {
				second = "slope($3::point,$4::point)"
			}
			q := "INSERT INTO geom_box_line_atomic VALUES(1," + tc.call + "),(2," + second + ")"
			stmt, err := c.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			args := append(append([]any(nil), tc.good...), tc.bad...)
			d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
			prior := func() {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, geometryMeasureSelect(b, "SELECT id,v FROM geom_box_line_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(0), float64(99)}}))
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.ErrorDiagnostic(q, d, args...))
			prior()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, args...))
			prior()
			run(fixture.Exec("BEGIN", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, d, args...))
			run(fixture.Exec("ROLLBACK", 0))
			prior()
			args = append(append([]any(nil), tc.good...), tc.good...)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 2, args...))
			run(geometryMeasureSelect(b, "SELECT id,v FROM geom_box_line_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "FLOAT8"}, [][]any{{int64(0), float64(99)}, {int64(1), tc.want}, {int64(2), tc.want}}))
			if !b.PG {
				ctx, cancel := context.WithCancel(p.Ctx)
				cancel()
				for _, prepared := range []bool{false, true} {
					var result sql.Result
					if prepared {
						result, err = stmt.ExecContext(ctx, args...)
					} else {
						result, err = c.ExecContext(ctx, q, args...)
					}
					if result != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("precancel write: %v %v", result, err)
					}
				}
				selectSQL := "SELECT " + tc.call + " AS v"
				selectStmt, err := c.PrepareContext(p.Ctx, selectSQL)
				if err != nil {
					t.Fatal(err)
				}
				defer selectStmt.Close()
				for _, prepared := range []bool{false, true} {
					var rows *sql.Rows
					if prepared {
						rows, err = selectStmt.QueryContext(ctx, tc.good...)
					} else {
						rows, err = c.QueryContext(ctx, selectSQL, tc.good...)
					}
					if rows != nil {
						rows.Close()
					}
					if rows != nil || !errors.Is(err, context.Canceled) {
						t.Fatalf("precancel query: %v %v", rows, err)
					}
				}
				run(fixture.Select("SELECT count(*) AS n FROM geom_box_line_atomic", []string{"n"}, []string{"INT8"}, [][]any{{int64(3)}}))
			}
			run(fixture.Exec("DELETE FROM geom_box_line_atomic WHERE id>0", 2))
		})
	}
}

func TestReferenceGeometryMeasureBoxLineControls(t *testing.T) {
	geometryMeasureScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		geometryMeasureBoxLineAtomicity(t, p, b, c)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		geometryMeasureBoxLineCreateConsumers(run)
		geometryMeasureBoxLineConsumerChecks(t, b, run)
		if b.PG {
			return
		} // Native logical host replacement has no PG API counterpart.
		var dump bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
			t.Fatal(err)
		}
		old, err := c.QueryContext(p.Ctx, "SELECT d,s FROM geom_box_line_view WHERE id=1")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		retained := geometryMeasureSelect(b, "", []string{"d", "s"}, []string{"LSEG", "FLOAT8"}, [][]any{{"[(4,6),(1,2)]", float64(0.5)}})
		retained.CheckMetadata(t, old)
		cols, err := old.ColumnTypes()
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
		geometryMeasureBoxLineConsumerChecks(t, b, run)
		retained.CheckMetadata(t, old)
		if !old.Next() {
			t.Fatalf("retained row: %v", old.Err())
		}
		var diagonal, slope any
		if err := old.Scan(&diagonal, &slope); err != nil {
			t.Fatal(err)
		}
		retained.CheckRaw(t, 0, 0, diagonal)
		retained.CheckRaw(t, 1, 0, slope)
		if diagonal != "[(4,6),(1,2)]" || slope != float64(0.5) {
			t.Fatalf("retained values %v %v", diagonal, slope)
		}
		if old.Next() || old.Err() != nil {
			t.Fatalf("retained exhaustion: %v", old.Err())
		}
		old.Close()
		if len(cols) != 2 || cols[0].DatabaseTypeName() != "LSEG" || cols[1].DatabaseTypeName() != "FLOAT8" {
			t.Fatal("retained metadata", cols)
		}
		target := fixture.NewFixture(t, false, true)
		if err := stuntgres.Restore(target.Ctx, target.Native, bytes.NewReader(dump.Bytes())); err != nil {
			t.Fatal(err)
		}
		targetRun := func(s fixture.Step) { fixture.RunStep(t, target.Ctx, target.Memory, target.MemoryRecord, s) }
		targetRun(fixture.Exec("SET extra_float_digits=3", 0))
		geometryMeasureBoxLineConsumerChecks(t, b, targetRun)
		bad := "CREATE TABLE geom_box_line_bad(b box,w float8 GENERATED ALWAYS AS (width(b)) STORED); INSERT INTO geom_box_line_bad(b) VALUES(box '(4,6),(1,2)'); INSERT INTO geom_box_line_bad(b) VALUES(box '(1e308,0),(-1e308,0)');"
		d := fixture.ControlsReferenceDiagnostic("22003", "value out of range: overflow")
		fixture.AssertDiagnostic(t, stuntgres.Restore(target.Ctx, target.Native, strings.NewReader(bad)), &d)
		geometryMeasureBoxLineConsumerChecks(t, b, targetRun)
		targetRun(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='geom_box_line_bad'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		sentinel := errors.New("box/line measurement restore I/O")
		if err := stuntgres.Restore(target.Ctx, target.Native, boxFailReader{sentinel}); !errors.Is(err, sentinel) {
			t.Fatalf("read: %v", err)
		}
		geometryMeasureBoxLineConsumerChecks(t, b, targetRun)
		ctx, cancel := context.WithCancel(target.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, target.Native, bytes.NewReader(dump.Bytes())); !errors.Is(err, context.Canceled) {
			t.Fatalf("cancelled restore: %v", err)
		}
		geometryMeasureBoxLineConsumerChecks(t, b, targetRun)
		if p.Postgres != nil {
			defer func() {
				if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
					t.Error(err)
				}
			}()
			if _, err := p.Postgres.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
				t.Fatal(err)
			}
			if _, err := p.Postgres.ExecContext(p.Ctx, dump.String()); err != nil {
				t.Fatalf("emitted dump replay: %v", err)
			}
			pgRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, p.Postgres, p.PostgresRecord, s) }
			pgRun(fixture.Exec("SET extra_float_digits=3", 0))
			geometryMeasureBoxLineConsumerChecks(t, fixture.Backend{PG: true}, pgRun)
		}
	})
}
