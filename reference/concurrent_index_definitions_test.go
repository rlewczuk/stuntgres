package reference_test

import (
	"stuntgres"
	"testing"
)

func TestReferenceConcurrentIndexUniqueDefinitions(t *testing.T) {
	t.Run("first-scan", func(t *testing.T) {
		concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			c := concurrentIndexConn(t, p, b)
			concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1),(1),(NULL),(NULL)", 4)
			concurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", concurrentIndexUnique("t", "ui", "Key (n)=(1) is duplicated.", true))
			concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
			concurrentIndexError(t, p, b, c, "CREATE INDEX CONCURRENTLY ui ON t(n)", controlsReferenceDiagnostic("42P07", "relation \"ui\" already exists"))
			concurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
		})
	})
	for _, def := range []struct{ name, ddl, seed, detail string }{
		{"lower", "lower(v)", "('A',1),('a',2)", "Key (lower(v))=(a) is duplicated."},
		{"decorated", "lower(v) COLLATE \"C\" text_ops DESC NULLS LAST", "('A',1),('a',2)", "Key (lower(v))=(a) is duplicated."},
		{"bpchar", "v bpchar_ops", "('a',1),('a ',2)", "Key (v)=(a) is duplicated."},
	} {
		t.Run(def.name, func(t *testing.T) {
			concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
				c := concurrentIndexConn(t, p, b)
				concurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,payload INTEGER)", 0)
				concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES"+def.seed, 2)
				q := "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(" + def.ddl + ") INCLUDE(payload)"
				want := concurrentIndexUnique("t", "ui", def.detail, true)
				_, err := c.ExecContext(p.ctx, q)
				referenceAssertDiagnostic(t, err, &want)
				concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
				concurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
			})
		})
	}
	t.Run("computed-enforcement", func(t *testing.T) { concurrentIndexScenario(t, concurrentIndexComputed) })
	t.Run("null-and-text", func(t *testing.T) {
		concurrentIndexScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			c := concurrentIndexConn(t, p, b)
			concurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER,payload TEXT)", 0)
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'same'),(NULL,'same'),(1,'same')", 3)
			concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n DESC NULLS FIRST) INCLUDE(payload)", 0)
			concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'same'),(2,'same')", 2)
			concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(2,'different')", concurrentIndexUnique("t", "ui", "Key (n)=(2) already exists.", false))
			concurrentIndexExec(t, p, b, c, "CREATE TABLE words(v TEXT)", 0)
			concurrentIndexExec(t, p, b, c, "INSERT INTO words VALUES('a'),('a ')", 2)
			concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY wi ON words(v text_ops)", 0)
		})
	})
}

func concurrentIndexComputed(t *testing.T, p *referencePair, b referenceBackend) {
	c := concurrentIndexConn(t, p, b)
	concurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
	concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'same'),(NULL,1,'same'),(NULL,1,'same')", 3)
	concurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE \"C\" text_ops DESC NULLS LAST,(n+1),(n+1)) INCLUDE(payload)", 0)
	concurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
	want := concurrentIndexUnique("t", "ui", "Key (lower(v), (n + 1), (n + 1))=(a, 2, 2) already exists.", false)
	concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',1,'other')", want)
	concurrentIndexExec(t, p, b, c, "UPDATE t SET payload='changed' WHERE v='A'", 1)
	concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('B',1,'x'),('a',1,'y')", want)
	concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 1)
	concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('B',1,'x')", 1)
	concurrentIndexError(t, p, b, c, "UPDATE t SET v='a' WHERE v='B'", want)
	concurrentIndexExec(t, p, b, c, "BEGIN", 0)
	concurrentIndexExec(t, p, b, c, "SAVEPOINT s", 0)
	concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('C',1,'x')", 1)
	duplicate := concurrentIndexUnique("t", "ui", "Key (lower(v), (n + 1), (n + 1))=(c, 2, 2) already exists.", false)
	concurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('c',1,'x')", duplicate)
	concurrentIndexExec(t, p, b, c, "ROLLBACK TO SAVEPOINT s", 0)
	concurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('C',1,'x')", 1)
	concurrentIndexExec(t, p, b, c, "COMMIT", 0)
	stmt, err := c.PrepareContext(p.ctx, "INSERT INTO t VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(p.ctx, "a", 1, "parameter")
	referenceAssertDiagnostic(t, err, &want)
	concurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 1, 1, 1)
}
