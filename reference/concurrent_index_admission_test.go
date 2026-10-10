package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexAdmission(t *testing.T) {
	concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
		for _, tc := range []struct {
			q, name string
			want    referenceDiagnostic
		}{
			{"CREATE INDEX CONCURRENTLY ix ON missing(n)", "ix", controlsReferenceDiagnostic("42P01", "relation \"missing\" does not exist")},
			{"CREATE INDEX CONCURRENTLY ix ON t(absent)", "ix", controlsReferenceDiagnostic("42703", "column \"absent\" does not exist")},
			{"CREATE UNIQUE INDEX CONCURRENTLY ix ON t USING hash(n)", "ix", controlsReferenceDiagnostic("0A000", `access method "hash" does not support unique indexes`)},
		} {
			concurrentIndexError(t, p, b, c, tc.q, tc.want)
			if _, found := concurrentIndexState(t, p, b, tc.name); found {
				t.Fatal("admission failure published descriptor")
			}
		}
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(0)", 1)
		want := controlsReferenceDiagnostic("22012", "division by zero")
		_, err := c.ExecContext(p.ctx, "CREATE INDEX CONCURRENTLY ix ON t((10/n))")
		referenceAssertDiagnostic(t, err, &want)
		concurrentIndexWait(t, p, b, "ix", stuntgres.IndexState{Live: true}, 0, 0, "")
		concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(0)", 1)
		concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 0, 0)
		concurrentIndexExec(t, p, b, c, "DROP INDEX ix", 0)
	})
}
