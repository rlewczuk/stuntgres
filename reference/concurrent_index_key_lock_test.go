package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexKeyLockEligibility(t *testing.T) {
	for _, tc := range []struct {
		name, definition, update string
		blocked                  bool
		n, p                     int64
	}{
		{"simple_key", "n) INCLUDE(p)", "UPDATE t SET n=2 WHERE n=1", true, 2, 10},
		{"parenthesized_key", "(n)) INCLUDE(p)", "UPDATE t SET n=2 WHERE n=1", true, 2, 10},
		{"simple_include", "n) INCLUDE(p)", "UPDATE t SET p=20 WHERE n=1", false, 1, 20},
		{"expression_dependency", "(n+1)) INCLUDE(p)", "UPDATE t SET n=2 WHERE n=1", false, 2, 10},
		{"mixed_simple_component", "(n+1),p)", "UPDATE t SET p=20 WHERE n=1", false, 1, 20},
	} {
		for _, held := range []bool{false, true} {
			t.Run(tc.name+"/"+fmtConcurrentCase(held, false, false), func(t *testing.T) {
				concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					writer, builder, reader, updater := concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b), concurrentIndexConn(t, p, b)
					ap, bp := concurrentIndexPID(t, p, b, writer), concurrentIndexPID(t, p, b, builder)
					concurrentIndexExec(t, p, b, updater, "CREATE TABLE t(n INTEGER,p INTEGER)", 0)
					concurrentIndexExec(t, p, b, updater, "INSERT INTO t VALUES(1,10)", 1)
					concurrentIndexExec(t, p, b, writer, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
					concurrentIndexExec(t, p, b, writer, "INSERT INTO t VALUES(3,30)", 1)
					build := concurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+tc.definition, false)
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
					concurrentIndexExec(t, p, b, reader, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
					referenceRunStep(t, p.ctx, reader, b.recorder, referenceSelect("SELECT n FROM t WHERE n=1 FOR KEY SHARE", []string{"n"}, []string{"INT4"}, [][]any{{int64(1)}}))
					concurrentIndexExec(t, p, b, updater, "SET lock_timeout='100ms'", 0)
					outcome := referenceRowExecute(p.ctx, updater, tc.update, held)
					if tc.blocked {
						want := controlsReferenceDiagnostic("55P03", "canceling statement due to lock timeout")
						if b.pg {
							want.Context = `while updating tuple (0,1) in relation "t"`
						}
						referenceAssertDiagnostic(t, outcome.err, &want)
					} else {
						referenceState(t, outcome.err, "")
						referenceLockAffected(t, outcome.result, 1)
					}
					// The positive cases have completed while KEY SHARE and the first-writer
					// transaction remain held. Native U5 separately observes the exact bitmap.
					concurrentIndexExec(t, p, b, reader, "ROLLBACK", 0)
					concurrentIndexExec(t, p, b, updater, "SET lock_timeout=0", 0)
					if tc.blocked {
						referenceLockExec(t, p.ctx, updater, tc.update, 1)
					}
					concurrentIndexExec(t, p, b, writer, "ROLLBACK", 0)
					if err := build.join(t, p); err != nil {
						t.Fatal(err)
					}
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
					referenceRunStep(t, p.ctx, updater, b.recorder, referenceSelect("SELECT n,p FROM t ORDER BY n", []string{"n", "p"}, []string{"INT4", "INT4"}, [][]any{{tc.n, tc.p}}))
				})
			})
		}
	}
}
