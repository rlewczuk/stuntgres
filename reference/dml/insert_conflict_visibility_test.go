package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictReadCommittedInvisibleConflict(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c := fixture.LockConnections(t, p.Ctx, b.DB)
		peer := fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, a, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(k INT UNIQUE)", 0))
		run(fixture.Exec("CREATE TABLE source(k INT)", 0))
		run(fixture.Exec("INSERT INTO source VALUES(1)", 1))
		run(fixture.Exec("CREATE TABLE witness(k INT)", 0))
		run(fixture.Exec("INSERT INTO witness VALUES(1)", 1))
		run(fixture.Exec("CREATE SEQUENCE s", 0))
		query := "INSERT INTO t SELECT a.k+0*nextval('s')::INT AS k FROM source a JOIN witness b ON a.k=b.k ORDER BY k FOR UPDATE OF a ON CONFLICT DO NOTHING"
		stmt, err := c.PrepareContext(p.Ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		holder, err := a.BeginTx(p.Ctx, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer holder.Rollback()
		fixture.RunStep(t, p.Ctx, holder, b.Recorder, fixture.Select("SELECT k FROM source FOR UPDATE", []string{"k"}, []string{"INT4"}, [][]any{{int64(1)}}))
		var ap, cp int
		if b.PG {
			ap, cp = fixture.RowPIDs(t, p.Ctx, a, c)
		}
		done := fixture.RowAsync(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}, query, false, false)
		if b.PG {
			fixture.RowWait(t, p.Ctx, cp, ap)
		} else {
			// JOIN's volatile ORDER output is an existing consuming frontier before
			// the physical rowmark. Its positive public effect proves capture of the
			// pre-peer-commit source candidate, not an invented native wait catalog.
			fixture.LimitSequenceCapture(t, p.Ctx, holder)
		}
		fixture.LockExec(t, p.Ctx, peer, "INSERT INTO t VALUES(1)", 1)
		if err := holder.Commit(); err != nil {
			t.Fatal(err)
		}
		result := fixture.LockResult(t, done)
		fixture.State(t, result.Err, "")
		fixture.LockAffected(t, result.Result, 0)
		run(insertConflictRows("SELECT k FROM t", []any{int64(1)}))
	})
}
func TestReferenceInsertConflictPlainRoutes(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(map[bool]string{false: "direct", true: "prepared"}[prepared], func(t *testing.T) {
			insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				run := func(s fixture.Step) {
					s.NonContext = true
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
				}
				run(fixture.Exec("CREATE TABLE conflict(k INT CONSTRAINT u UNIQUE)", 0))
				run(fixture.Exec("INSERT INTO conflict VALUES(1)", 1))
				run(fixture.Exec("INSERT INTO conflict VALUES(1),(2) ON CONFLICT DO NOTHING", 1))
				run(fixture.Exec("INSERT INTO conflict VALUES(1),(2) ON CONFLICT ON CONSTRAINT u DO NOTHING", 0))
				run(insertConflictRows("SELECT k FROM conflict ORDER BY k", []any{int64(1)}, []any{int64(2)}))
			})
		})
	}
}
