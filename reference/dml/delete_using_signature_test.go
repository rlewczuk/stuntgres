package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceDeleteUsingPreparedSignature(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE t(id INT,v INT)", 0))
		run(fixture.Exec("CREATE TABLE s(k INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1,10)", 1))
		run(fixture.Exec("INSERT INTO s VALUES(1)", 1))
		q := "DELETE FROM t USING s WHERE t.id=s.k RETURNING s.k"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		run(fixture.Exec("ALTER TABLE s ALTER COLUMN k TYPE BIGINT", 0))
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("0A000", "cached plan must not change result type")))
		run(deleteUsingTargets([][]any{{int64(1), int64(10)}}))
		run(fixture.Select("SELECT k FROM s", []string{"k"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}

func TestReferenceDeleteUsingIndexSource(t *testing.T) {
	deleteUsingReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := updateFromRun(t, p, b, c)
		deleteUsingSeed(run)
		run(fixture.Exec("CREATE INDEX source_index ON s(k)", 0))
		want := fixture.ControlsReferenceDiagnostic("42809", `cannot open relation "source_index"`)
		want.Detail = "This operation is not supported for indexes."
		want.Position = "21"
		for _, prepared := range []bool{false, true} {
			updateFromDiagnostic(t, p.Ctx, c, "DELETE FROM t USING source_index", want, prepared)
			run(deleteUsingTargets(deleteUsingOriginal()))
			deleteUsingSourcesUnchanged(run)
		}
	})
}
