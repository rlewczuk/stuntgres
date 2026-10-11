package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceConcurrentIndexUniqueDefinitions(t *testing.T) {
	t.Run("first-scan", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1),(1),(NULL),(NULL)", 4)
			fixture.ConcurrentIndexError(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) is duplicated.", true))
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1)", 1)
			fixture.ConcurrentIndexError(t, p, b, c, "CREATE INDEX CONCURRENTLY ui ON t(n)", fixture.ControlsReferenceDiagnostic("42P07", "relation \"ui\" already exists"))
			fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
		})
	})
	for _, def := range []struct{ name, ddl, seed, detail string }{
		{"lower", "lower(v)", "('A',1),('a',2)", "Key (lower(v))=(a) is duplicated."},
		{"decorated", "lower(v) COLLATE \"C\" text_ops DESC NULLS LAST", "('A',1),('a',2)", "Key (lower(v))=(a) is duplicated."},
		{"bpchar", "v bpchar_ops", "('a',1),('a ',2)", "Key (v)=(a) is duplicated."},
	} {
		t.Run(def.name, func(t *testing.T) {
			fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := fixture.ConcurrentIndexConn(t, p, b)
				fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,payload INTEGER)", 0)
				fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES"+def.seed, 2)
				q := "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(" + def.ddl + ") INCLUDE(payload)"
				want := fixture.ConcurrentIndexUnique("t", "ui", def.detail, true)
				_, err := c.ExecContext(p.Ctx, q)
				fixture.AssertDiagnostic(t, err, &want)
				fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, 0, 0, "")
				fixture.ConcurrentIndexExec(t, p, b, c, "DROP INDEX ui", 0)
			})
		})
	}
	t.Run("computed-enforcement", func(t *testing.T) { fixture.ConcurrentIndexScenario(t, concurrentIndexComputed) })
	t.Run("null-and-text", func(t *testing.T) {
		fixture.ConcurrentIndexScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			c := fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INTEGER,payload TEXT)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'same'),(NULL,'same'),(1,'same')", 3)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n DESC NULLS FIRST) INCLUDE(payload)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(NULL,'same'),(2,'same')", 2)
			fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES(2,'different')", fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(2) already exists.", false))
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE words(v TEXT)", 0)
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO words VALUES('a'),('a ')", 2)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY wi ON words(v text_ops)", 0)
		})
	})
}

func concurrentIndexComputed(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	c := fixture.ConcurrentIndexConn(t, p, b)
	fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(v TEXT,n INTEGER,payload TEXT)", 0)
	fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('A',1,'same'),(NULL,1,'same'),(NULL,1,'same')", 3)
	fixture.ConcurrentIndexExec(t, p, b, c, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v) COLLATE \"C\" text_ops DESC NULLS LAST,(n+1),(n+1)) INCLUDE(payload)", 0)
	fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Valid: true, Live: true}, 0, 0, "")
	want := fixture.ConcurrentIndexUnique("t", "ui", "Key (lower(v), (n + 1), (n + 1))=(a, 2, 2) already exists.", false)
	fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('a',1,'other')", want)
	fixture.ConcurrentIndexExec(t, p, b, c, "UPDATE t SET payload='changed' WHERE v='A'", 1)
	fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('B',1,'x'),('a',1,'y')", want)
	fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 1)
	fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('B',1,'x')", 1)
	fixture.ConcurrentIndexError(t, p, b, c, "UPDATE t SET v='a' WHERE v='B'", want)
	fixture.ConcurrentIndexExec(t, p, b, c, "BEGIN", 0)
	fixture.ConcurrentIndexExec(t, p, b, c, "SAVEPOINT s", 0)
	fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('C',1,'x')", 1)
	duplicate := fixture.ConcurrentIndexUnique("t", "ui", "Key (lower(v), (n + 1), (n + 1))=(c, 2, 2) already exists.", false)
	fixture.ConcurrentIndexError(t, p, b, c, "INSERT INTO t VALUES('c',1,'x')", duplicate)
	fixture.ConcurrentIndexExec(t, p, b, c, "ROLLBACK TO SAVEPOINT s", 0)
	fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES('C',1,'x')", 1)
	fixture.ConcurrentIndexExec(t, p, b, c, "COMMIT", 0)
	stmt, err := c.PrepareContext(p.Ctx, "INSERT INTO t VALUES($1,$2,$3)")
	if err != nil {
		t.Fatal(err)
	}
	defer stmt.Close()
	_, err = stmt.ExecContext(p.Ctx, "a", 1, "parameter")
	fixture.AssertDiagnostic(t, err, &want)
	fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t ORDER BY n", 1, 1, 1, 1, 1)
}
