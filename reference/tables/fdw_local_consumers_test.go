package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWLocalConsumers(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		fdwLive(t, p, b, r, run)
		run(fixture.Exec("CREATE TABLE fdw_local(id integer PRIMARY KEY,label text)", 0))
		data := func(q string, rows [][]any) fixture.Step {
			return fixture.Select(q, []string{"id", "label"}, []string{"INT4", "TEXT"}, rows)
		}
		all := [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}}
		operation := func(q string, count int64) {
			t.Helper()
			if !t.Run(q, func(t *testing.T) {
				before := r.snapshot()
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, count))
				if !b.PG {
					fdwReleased(t, r, before, 1)
				}
			}) {
				t.FailNow()
			}
		}
		operation("INSERT INTO fdw_local SELECT id,label FROM fdw_data", 3)
		run(data("SELECT id,label FROM fdw_local ORDER BY id", all))
		run(fixture.Exec("TRUNCATE fdw_local", 0))
		beforePrepared := r.snapshot()
		run(fixture.Prepared(fixture.Exec("INSERT INTO fdw_local SELECT id,label FROM fdw_data", 3)))
		if !b.PG {
			fdwReleased(t, r, beforePrepared, 1)
		}
		run(data("SELECT id,label FROM fdw_local ORDER BY id", all))
		run(fixture.Exec("TRUNCATE fdw_local", 0))
		run(fixture.Exec("INSERT INTO fdw_local VALUES(1,'old'),(2,'old'),(9,'old')", 3))
		operation("UPDATE fdw_local SET label=f.label FROM fdw_data f WHERE fdw_local.id=f.id", 2)
		run(data("SELECT id,label FROM fdw_local ORDER BY id", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(9), "old"}}))
		operation("DELETE FROM fdw_local USING fdw_data f WHERE fdw_local.id=f.id", 2)
		run(data("SELECT id,label FROM fdw_local ORDER BY id", [][]any{{int64(9), "old"}}))
		run(fixture.Exec("TRUNCATE fdw_local", 0))
		run(fixture.Exec("INSERT INTO fdw_local VALUES(1,'old'),(9,'keep')", 2))
		merge := "MERGE INTO fdw_local l USING (SELECT id,label FROM fdw_data) f ON l.id=f.id WHEN MATCHED THEN UPDATE SET label=f.label WHEN NOT MATCHED THEN INSERT(id,label) VALUES(f.id,f.label)"
		mergeCount := int64(3)
		// As in reference/dml MERGE consumers, pinned lib/pq does not decode
		// the MERGE command tag. Native keeps its actual affected-row count.
		if b.PG {
			mergeCount = 0
		}
		operation(merge, mergeCount)
		run(data("SELECT id,label FROM fdw_local ORDER BY id", [][]any{{int64(1), "one"}, {int64(2), nil}, {int64(3), "three"}, {int64(9), "keep"}}))
		operation("CREATE TABLE fdw_snapshot AS SELECT id,label FROM fdw_data", 3)
		run(data("SELECT id,label FROM fdw_snapshot ORDER BY id", all))
		run(fixture.Exec("TRUNCATE fdw_local", 0))
		before := r.snapshot()
		run(fixture.Select("WITH added AS (INSERT INTO fdw_local SELECT id,label FROM fdw_data RETURNING id) SELECT count(*) FROM added", []string{"count"}, []string{"INT8"}, [][]any{{int64(3)}}))
		if !b.PG {
			fdwReleased(t, r, before, 1)
		}
		run(data("SELECT id,label FROM fdw_local ORDER BY id", all))
		if b.PG {
			return
		}

		failure := errors.New("actual foreign SELECT consumer End failed")
		for _, query := range []string{
			"INSERT INTO fdw_local SELECT id,label FROM fdw_data WHERE id=2 RETURNING id,label",
			"UPDATE fdw_local SET label=f.label FROM fdw_data f WHERE fdw_local.id=f.id RETURNING fdw_local.id,fdw_local.label",
			"DELETE FROM fdw_local USING fdw_data f WHERE fdw_local.id=f.id RETURNING fdw_local.id,fdw_local.label",
			merge + " RETURNING l.id,l.label",
			"WITH added AS (INSERT INTO fdw_local SELECT id,label FROM fdw_data WHERE id=2 RETURNING id) SELECT count(*) FROM added",
			"CREATE TABLE fdw_failed_snapshot AS WITH added AS (INSERT INTO fdw_local SELECT id,label FROM fdw_data WHERE id=2 RETURNING id) SELECT id FROM added",
		} {
			run(fixture.Exec("TRUNCATE fdw_local", 0))
			run(fixture.Exec("INSERT INTO fdw_local VALUES(1,'unchanged'),(9,'keep')", 2))
			r.setHook(func(ctx context.Context, method string) error {
				if method == "EndForeignScan" {
					return failure
				}
				return ctx.Err()
			})
			before = r.snapshot()
			fdwHostQueryError(t, p.Ctx, c, query, failure)
			fdwReleased(t, r, before, 1)
			r.setHook(nil)
			run(data("SELECT id,label FROM fdw_local ORDER BY id", [][]any{{int64(1), "unchanged"}, {int64(9), "keep"}}))
			run(fixture.Select("SELECT count(*) FROM pg_class WHERE relname='fdw_failed_snapshot'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		}
		unchanged := [][]any{{int64(1), "unchanged"}, {int64(9), "keep"}}
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("INSERT INTO fdw_local VALUES(77,'prior write')", 1))
		run(fixture.Exec("SAVEPOINT fdw_save", 0))
		r.setHook(func(ctx context.Context, method string) error {
			if method == "EndForeignScan" {
				return failure
			}
			return ctx.Err()
		})
		before = r.snapshot()
		fdwHostQueryError(t, p.Ctx, c, "INSERT INTO fdw_local SELECT id,label FROM fdw_data WHERE id=2 RETURNING id,label", failure)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		run(fixture.Exec("ROLLBACK TO fdw_save", 0))
		run(data("SELECT id,label FROM fdw_local ORDER BY id", [][]any{{int64(1), "unchanged"}, {int64(9), "keep"}, {int64(77), "prior write"}}))
		run(fixture.Exec("ROLLBACK", 0))
		run(data("SELECT id,label FROM fdw_local ORDER BY id", unchanged))
		r.setHook(func(ctx context.Context, method string) error {
			if method == "EndForeignScan" {
				return failure
			}
			return ctx.Err()
		})
		before = r.snapshot()
		run(fixture.ErrorDiagnostic("INSERT INTO fdw_local SELECT id,label FROM fdw_data WHERE 1/(id-id)>0 RETURNING id,label", fdwDiagnostic("22012", "division by zero")))
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		run(data("SELECT id,label FROM fdw_local ORDER BY id", unchanged))
		opened := 0
		r.setHook(func(ctx context.Context, method string) error {
			if method == "GetForeignRelSize" {
				opened++
				if opened == 2 {
					return failure
				}
			}
			return ctx.Err()
		})
		before = r.snapshot()
		fdwHostQueryError(t, p.Ctx, c, "INSERT INTO fdw_local SELECT a.id,a.label FROM fdw_data a CROSS JOIN fdw_data b WHERE b.id=1 RETURNING id,label", failure)
		fdwReleased(t, r, before, 1)
		r.setHook(nil)
		if opened != 2 {
			t.Fatal("sibling failure did not follow first real source admission", opened)
		}
		run(data("SELECT id,label FROM fdw_local ORDER BY id", unchanged))
	})
}
