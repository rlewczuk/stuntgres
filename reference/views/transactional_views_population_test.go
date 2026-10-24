package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceTransactionalViewsPopulation(t *testing.T) {
	transactionalViewsRoutes(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c, peer *sql.Conn, mode string, held bool) {
		base := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		base(fixture.Exec("CREATE TABLE txv_source(id INT)", 0))
		base(fixture.Exec("INSERT INTO txv_source VALUES(2),(1),(0)", 3))
		queries := []string{
			"CREATE MATERIALIZED VIEW txv_nodata AS SELECT 10/id AS value FROM txv_source WITH NO DATA",
			"CREATE MATERIALIZED VIEW txv_empty AS SELECT id FROM txv_source WHERE false WITH DATA",
			"CREATE MATERIALIZED VIEW txv_failed AS SELECT 10/id AS value FROM txv_source WITH DATA",
		}
		executors := make([]fixture.Executor, len(queries))
		for i, q := range queries {
			executors[i] = c
			if held {
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				executors[i] = fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
			}
		}
		e, finish := transactionalViewsBegin(t, p, b, c, mode)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, e, b.Recorder, s) }
		fixture.RunStep(t, p.Ctx, executors[0], b.Recorder, fixture.Exec(queries[0], 0))
		fixture.MaterializedViewPopulation(run, "txv_nodata", false)
		run(fixture.MaterializedViewUnpopulated("txv_nodata"))
		if mode != "autocommit" {
			// Reading NO DATA is an actual error and aborts the transaction.
			finish(false)
			e, finish = transactionalViewsBegin(t, p, b, c, mode)
			fixture.RunStep(t, p.Ctx, executors[0], b.Recorder, fixture.Exec(queries[0], 0))
		}
		fixture.RunStep(t, p.Ctx, executors[1], b.Recorder, fixture.Exec(queries[1], 0))
		fixture.MaterializedViewPopulation(run, "txv_empty", true)
		run(fixture.Select("SELECT id FROM txv_empty", []string{"id"}, []string{"INT4"}, nil))
		if mode != "autocommit" {
			run(fixture.Exec("SAVEPOINT producer", 0))
		}
		failure := fixture.ImplicitError(queries[2], fixture.Diagnostic{Code: "22012", Message: "division by zero"})
		failure.ErrorStage = "execute"
		fixture.RunStep(t, p.Ctx, executors[2], b.Recorder, failure)
		if mode != "autocommit" {
			run(fixture.ImplicitError("SELECT 1", fixture.Diagnostic{Code: "25P02", Message: "current transaction is aborted, commands ignored until end of transaction block"}))
			run(fixture.Exec("ROLLBACK TO producer", 0))
		}
		fixture.QueryTableCreateAbsent(t, run, "txv_failed")
		run(fixture.Select("SELECT id FROM txv_source ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}}))
		// Same spelling succeeds after the failed producer; no partial storage survives.
		transactionalViewsExec(t, p, b, e, held, fixture.Exec("CREATE MATERIALIZED VIEW txv_failed AS SELECT id FROM txv_source", 3))
		run(fixture.Select("SELECT id FROM txv_failed ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}}))
		if mode != "autocommit" {
			fixture.RunStep(t, p.Ctx, peer, b.Recorder, transactionalViewsCount("txv_failed", 0))
		}
		finish(true)
		fixture.RunStep(t, p.Ctx, peer, b.Recorder, fixture.Select("SELECT id FROM txv_failed ORDER BY id", []string{"id"}, []string{"INT4"}, [][]any{{int64(0)}, {int64(1)}, {int64(2)}}))
	})
}
