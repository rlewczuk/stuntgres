package reference_test

import (
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceInsertConflictInstalledProviders(t *testing.T) {
	for _, c := range []struct{ name, columns, index, target, seed, proposal string }{
		{"lower", "v TEXT,p TEXT", "lower(v) COLLATE \"C\" text_ops", "lower(v) COLLATE \"C\" text_ops", "'A','kept'", "'a','ignored'"},
		{"arithmetic", "v INT,p TEXT", "(v+1)", "(v+1)", "1,'kept'", "1,'ignored'"},
		{"class_alias", "v TEXT,p TEXT", "v text_ops", "v varchar_ops", "'a','kept'", "'a','ignored'"},
		{"mixed", "v TEXT,n INT,p TEXT", "lower(v),n,(n+1)", "(n+1),n,lower(v),lower(v)", "'A',1,'kept'", "'a',1,'ignored'"},
		{"trimmed", "v TEXT,p TEXT", "v bpchar_ops", "v", "'a','kept'", "'a ','ignored'"},
		{"direction", "v INT,p TEXT", "v DESC NULLS FIRST", "v", "1,'kept'", "1,'ignored'"},
	} {
		t.Run(c.name, func(t *testing.T) {
			insertConflictSteps(t, fixture.Exec("CREATE TABLE t("+c.columns+")", 0),
				fixture.Exec("CREATE UNIQUE INDEX CONCURRENTLY ui ON t("+c.index+") INCLUDE(p)", 0),
				fixture.Exec("INSERT INTO t VALUES("+c.seed+")", 1),
				fixture.Exec("INSERT INTO t VALUES("+c.proposal+") ON CONFLICT("+c.target+") DO NOTHING", 0),
				fixture.Exec("INSERT INTO t VALUES("+c.proposal+") ON CONFLICT DO NOTHING", 0),
				fixture.Select("SELECT p FROM t", []string{"p"}, []string{"TEXT"}, [][]any{{"kept"}}))
		})
	}
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(v TEXT,p TEXT)", 0), fixture.Exec("CREATE UNIQUE INDEX CONCURRENTLY ui ON t(lower(v))", 0),
		insertConflictNoMatch("INSERT INTO t VALUES('A','x') ON CONFLICT(upper(v)) DO NOTHING"),
		insertConflictNoMatch("INSERT INTO t VALUES('A','x') ON CONFLICT(lower(v) bpchar_ops) DO NOTHING"))
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT UNIQUE)", 0),
		insertConflictNoMatch("INSERT INTO t VALUES(1) ON CONFLICT(k int8_ops) DO NOTHING"))
}
func TestReferenceInsertConflictPartialInference(t *testing.T) {
	insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT,active BOOL,p TEXT)", 0), fixture.Exec("CREATE UNIQUE INDEX ui ON t(k) WHERE active", 0),
		fixture.Exec("INSERT INTO t VALUES(1,TRUE,'kept')", 1), fixture.Exec("INSERT INTO t VALUES(1,TRUE,'ignored'),(1,FALSE,'outside'),(1,NULL,'null') ON CONFLICT(k) WHERE active DO NOTHING", 2),
		fixture.Select("SELECT p FROM t ORDER BY p", []string{"p"}, []string{"TEXT"}, [][]any{{"kept"}, {"null"}, {"outside"}}),
		insertConflictNoMatch("INSERT INTO t VALUES(2,TRUE,'x') ON CONFLICT(k) DO NOTHING"),
		fixture.Exec("CREATE UNIQUE INDEX all_rows ON t(p)", 0), fixture.Exec("INSERT INTO t VALUES(2,FALSE,'kept') ON CONFLICT(p) WHERE active DO NOTHING", 0))
}
func TestReferenceInsertConflictPredicateImplication(t *testing.T) {
	cases := []struct {
		index, target string
		match         bool
	}{
		{"n>0", "n>10", true}, {"n>0", "10<n", true}, {"n>0", "n>=0", false},
		{"n<10", "n<=9", true}, {"n<10", "n<=10", false},
		{"active OR n>0", "active", true}, {"active", "active OR n>0", false},
		{"active AND n>0", "n>10 AND active", true}, {"active AND n>0", "active", false},
		{"n IS NOT NULL", "n>0", true}, {"active", "active=TRUE", true}, {"NOT active", "active=FALSE", true},
		{"NOT active", "active IS NOT TRUE", false}, {"n>m", "m<n", true}, {"n>=m", "n>m", true},
		{"n>0", "n+1>2", false}, {"n>0", "n>10 OR n>20", true},
		{"n>0 OR m>0", "n>10 AND m<0", true}, {"n>0 AND m>0", "n>10 OR m>10", false},
	}
	for _, c := range cases {
		t.Run(c.index+"/"+c.target, func(t *testing.T) {
			values := "1,20,0,TRUE"
			if c.index == "n<10" {
				values = "1,5,0,TRUE"
			}
			if c.index == "NOT active" {
				values = "1,20,0,FALSE"
			}
			s := fixture.Exec("INSERT INTO t VALUES("+values+") ON CONFLICT(k) WHERE "+c.target+" DO NOTHING", 0)
			if !c.match {
				s = insertConflictNoMatch(s.Query)
			}
			insertConflictSteps(t, fixture.Exec("CREATE TABLE t(k INT,n INT,m INT,active BOOL)", 0),
				fixture.Exec("CREATE UNIQUE INDEX ui ON t(k) WHERE "+c.index, 0), fixture.Exec("INSERT INTO t VALUES("+values+")", 1), s,
				fixture.Select("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		})
	}
}
func TestReferenceInsertConflictConcurrentProviderPhases(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE t(n INT)", 0))
		run(fixture.Exec("INSERT INTO t VALUES(1)", 1))
		fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN ISOLATION LEVEL REPEATABLE READ", 0)
		fixture.ConcurrentIndexRows(t, p, b, a, "SELECT n FROM t", 1)
		ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
		w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
		fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Ready: true, Live: true}, bp, ap, "waiting for old snapshots")
		run(fixture.Exec("INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING", 0))
		run(insertConflictNoMatch("INSERT INTO t VALUES(1) ON CONFLICT(n) DO NOTHING"))
		if !b.PG {
			fixture.ConcurrentIndexDumpInvalid(t, p, "ui")
		}
		fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
		if err := w.Join(t, p); err != nil {
			t.Fatal(err)
		}
		run(fixture.Exec("INSERT INTO t VALUES(1) ON CONFLICT(n) DO NOTHING", 0))
		run(insertConflictRows("SELECT n AS k FROM t", []any{int64(1)}))
	})
	t.Run("not_ready", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
			a, c, builder := fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b), fixture.ConcurrentIndexConn(t, p, b)
			fixture.ConcurrentIndexExec(t, p, b, c, "CREATE TABLE t(n INT)", 0)
			fixture.ConcurrentIndexExec(t, p, b, a, "BEGIN", 0)
			fixture.ConcurrentIndexExec(t, p, b, a, "INSERT INTO t VALUES(1)", 1)
			ap, bp := fixture.ConcurrentIndexPID(t, p, b, a), fixture.ConcurrentIndexPID(t, p, b, builder)
			w := fixture.ConcurrentIndexStart(t, p, builder, "CREATE UNIQUE INDEX CONCURRENTLY ui ON t(n)", false)
			fixture.ConcurrentIndexWait(t, p, b, "ui", stuntgres.IndexState{Live: true}, bp, ap, "waiting for writers before build")
			fixture.ConcurrentIndexExec(t, p, b, c, "INSERT INTO t VALUES(1) ON CONFLICT DO NOTHING", 1)
			fixture.ConcurrentIndexExec(t, p, b, a, "COMMIT", 0)
			want := fixture.ConcurrentIndexUnique("t", "ui", "Key (n)=(1) is duplicated.", true)
			fixture.AssertDiagnostic(t, w.Join(t, p), &want)
			fixture.ConcurrentIndexRows(t, p, b, c, "SELECT n FROM t", 1, 1)
		})
	})
}
