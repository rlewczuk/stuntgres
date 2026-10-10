package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexTypeReconstruction(t *testing.T) {
	for _, absent := range []bool{false, true} {
		t.Run(map[bool]string{false: "valid_private", true: "absent_private"}[absent], func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				c := concurrentIndexConn(t, p, b)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER,payload TEXT)", 0)
				if absent {
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1,'a'),(1,'b')", 2)
					concurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t((n+1)) INCLUDE(payload)", concurrentIndexUnique("t", "ui", "Key ((n + 1))=(2) is duplicated.", true))
					concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
					concurrentIndexExec(t, p, b, c, "DELETE FROM t", 2)
					concurrentIndexExec(t, p, b, c, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(2,'own')", 1)
				} else {
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1,'a'),(2,'b')", 2)
					concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t((n+1)) INCLUDE(payload)", 0)
					concurrentIndexExec(t, p, b, c, "BEGIN ISOLATION LEVEL READ COMMITTED", 0)
					concurrentIndexExec(t, p, b, c, "UPDATE t SET n=3 WHERE n=2", 1)
					concurrentIndexExec(t, p, b, c, "SAVEPOINT before_type", 0)
					concurrentIndexError(t, p, b, c, "ALTER TABLE t ALTER n TYPE BIGINT USING 0", concurrentIndexUnique("t", "ui", "Key ((n + 1))=(1) is duplicated.", true))
					concurrentIndexExec(t, p, b, c, "ROLLBACK TO before_type", 0)
				}
				concurrentIndexExec(t, p, b, c, "ALTER TABLE t ALTER n TYPE BIGINT", 0)
				concurrentIndexExec(t, p, b, c, "SAVEPOINT typed", 0)
				query := "INSERT INTO t VALUES(1,'duplicate')"
				detail := "Key ((n + 1))=(2) already exists."
				if absent {
					query = "INSERT INTO t VALUES(2,'duplicate')"
					detail = "Key ((n + 1))=(3) already exists."
				}
				concurrentIndexError(t, p, b, c, query, concurrentIndexUnique("t", "ui", detail, false))
				concurrentIndexExec(t, p, b, c, "ROLLBACK TO typed", 0)
				if absent {
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(3,'later')", 1)
				} else {
					concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(4,'later')", 1)
				}
				concurrentIndexExec(t, p, b, c, "COMMIT", 0)
				concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
				expected := [][]any{{int64(1)}, {int64(3)}, {int64(4)}}
				if absent {
					expected = [][]any{{int64(2)}, {int64(3)}}
				}
				referenceRunStep(t, p.ctx, c, b.recorder, referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT8"}, expected))
			})
		})
	}
}
