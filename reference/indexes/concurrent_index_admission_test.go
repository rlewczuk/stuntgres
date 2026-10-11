package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexAdmission(t *testing.T) {
	fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := fixture.ConcurrentIndexConn(t, p, b)
		fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		for _, tc := range []struct {
			q, name string
			want    fixture.Diagnostic
		}{
			{"CREATE INDEX CONCURRENTLY ix ON missing(n)", "ix", fixture.ControlsReferenceDiagnostic("42P01", "relation \"missing\" does not exist")},
			{"CREATE INDEX CONCURRENTLY ix ON t(absent)", "ix", fixture.ControlsReferenceDiagnostic("42703", "column \"absent\" does not exist")},
			{"CREATE UNIQUE INDEX CONCURRENTLY ix ON t USING hash(n)", "ix", fixture.ControlsReferenceDiagnostic("0A000", `access method "hash" does not support unique indexes`)},
		} {
			fixture.ConcurrentIndexError(t, p, b, c, tc.q, tc.want)
			if _, found := fixture.ConcurrentIndexState(t, p, b, tc.name); found {
				t.Fatal("admission failure published descriptor")
			}
		}
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(0)", 1)
		want := fixture.ControlsReferenceDiagnostic("22012", "division by zero")
		_, err := c.ExecContext(p.Ctx, "CREATE INDEX CONCURRENTLY ix ON t((10/n))")
		fixture.AssertDiagnostic(t, err, &want)
		fixture.ConcurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, 0, 0, "")
		fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(0)", 1)
		fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 0, 0)
		fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ix", 0)
	})
}
