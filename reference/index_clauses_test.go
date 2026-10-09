package reference_test

import (
	"fmt"
	"strings"
	"testing"
)

func indexClausesScenario(t *testing.T, body func(*testing.T, *referencePair, referenceBackend, func(referenceStep))) {
	t.Helper()
	for _, plain := range []bool{false, true} {
		t.Run(fmt.Sprintf("plain=%v", plain), func(t *testing.T) {
			p := newReferenceScenario(t)
			for _, backend := range p.backends() {
				t.Run(backend.name, func(t *testing.T) {
					run := func(s referenceStep) {
						s.nonContext = plain
						referenceRunStep(t, p.ctx, backend.db, backend.recorder, s)
					}
					body(t, p, backend, run)
				})
			}
		})
	}
}

func indexClausesSeed(run func(referenceStep)) {
	run(referenceExec("CREATE TABLE t(a INT,b INT,p TEXT)", 0))
	run(referenceExec("INSERT INTO t VALUES(1,2,'x'),(1,1,'y'),(1,NULL,'z'),(2,1,'w'),(NULL,3,'n'),(NULL,NULL,'m')", 6))
}

func indexClausesOrders(run func(referenceStep)) {
	for _, tc := range []struct {
		order string
		rows  [][]any
	}{
		{"a ASC NULLS LAST,b DESC NULLS FIRST,p", [][]any{{"z"}, {"x"}, {"y"}, {"w"}, {"m"}, {"n"}}},
		{"a DESC NULLS FIRST,b ASC NULLS LAST,p", [][]any{{"n"}, {"m"}, {"w"}, {"y"}, {"x"}, {"z"}}},
	} {
		s := referenceSelect("SELECT p FROM t ORDER BY "+tc.order, []string{"p"}, []string{"TEXT"}, tc.rows)
		run(s)
		run(referencePrepared(s))
	}
}

func TestReferenceIndexClausesMixedOrdering(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		indexClausesSeed(run)
		indexClausesOrders(run)
		run(referenceExec("CREATE INDEX cover ON t(a ASC NULLS LAST,b DESC NULLS FIRST) INCLUDE(p)", 0))
		indexClausesOrders(run)
	})
}

func TestReferenceIndexClausesIncludeProjection(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		indexClausesSeed(run)
		run(referencePrepared(referenceExec("CREATE INDEX cover ON t(a,b) INCLUDE(p)", 0)))
		run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname='cover'", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(3)}}))
		for _, s := range []referenceStep{
			referenceSelect("SELECT a,b FROM t WHERE p='x'", []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(2)}}),
			referenceSelect("SELECT p FROM t WHERE a=1 AND b>=2", []string{"p"}, []string{"TEXT"}, [][]any{{"x"}}),
			referenceSelect("SELECT p FROM t WHERE b=1", []string{"p"}, []string{"TEXT"}, [][]any{{"y"}, {"w"}}),
			referenceSelect("SELECT p FROM t WHERE a=1", []string{"p"}, []string{"TEXT"}, [][]any{{"x"}, {"y"}, {"z"}}),
		} {
			s.bag = true
			run(s)
			run(referencePrepared(s))
		}
		run(referenceExec("CREATE DOMAIN payload AS name[]", 0))
		run(referenceExec("CREATE TABLE arrays(n INT,a payload)", 0))
		run(referenceExec("INSERT INTO arrays VALUES(1,'{alpha,NULL}'),(2,NULL)", 2))
		run(referenceExec("CREATE INDEX arr_cover ON arrays(n) INCLUDE(a)", 0))
		run(referenceSelect("SELECT a FROM arrays ORDER BY n", []string{"a"}, []string{"_NAME"}, [][]any{{"{alpha,NULL}"}, {nil}}))
		run(referenceExec("CREATE INDEX ON t(a,b) INCLUDE(a,p,p)", 0))
		run(referenceSelect("SELECT relname,relnatts FROM pg_class WHERE relname='t_a_b_a1_p_p1_idx'", []string{"relname", "relnatts"}, []string{"NAME", "INT2"}, [][]any{{"t_a_b_a1_p_p1_idx", int64(5)}}))
	})
}

func TestReferenceIndexClausesColumnLimit(t *testing.T) {
	roleReferenceCleanup(t, []string{"clauses_limit_owner"})
	schemaReferenceCleanup(t, "clauses_limit_acl")
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(n INT)", 0))
		list := func(n int) string { return strings.TrimSuffix(strings.Repeat("n,", n), ",") }
		for i, tc := range []struct{ keys, includes int }{{1, 31}, {31, 1}, {32, 0}} {
			q := fmt.Sprintf("CREATE INDEX ok%d ON t(%s)", i, list(tc.keys))
			if tc.includes > 0 {
				q += " INCLUDE(" + list(tc.includes) + ")"
			}
			run(referenceExec(q, 0))
			run(referenceSelect("SELECT relnatts FROM pg_class WHERE relname=$1", []string{"relnatts"}, []string{"INT2"}, [][]any{{int64(32)}}, fmt.Sprintf("ok%d", i)))
		}
		for _, q := range []string{
			"CREATE INDEX retry ON t(n) INCLUDE(" + list(32) + ")",
			"CREATE INDEX retry ON t(" + list(32) + ") INCLUDE(n)",
			"CREATE INDEX retry ON t(" + list(33) + ")",
			"CREATE INDEX retry ON t USING missing(" + list(32) + ",missing)",
			"CREATE INDEX retry ON t(n missing_ops," + list(32) + ")",
		} {
			s := referenceImplicitError(q, referenceDiagnostic{Code: "54011", Message: "cannot use more than 32 columns in an index"})
			s.errorStage = "execute"
			run(s)
			run(referencePrepared(s))
		}
		run(referenceSelect("SELECT relname FROM pg_class WHERE relname='retry'", []string{"relname"}, []string{"NAME"}, nil))
		run(referenceExec("CREATE INDEX retry ON t(n) INCLUDE(n)", 0))
		run(referenceExec("CREATE VIEW v AS SELECT n FROM t", 0))
		for _, tc := range []struct{ target, code, message string }{
			{"missing", "42P01", `relation "missing" does not exist`},
			{"v", "54011", "cannot use more than 32 columns in an index"},
		} {
			s := referenceImplicitError("CREATE INDEX denied ON "+tc.target+"("+list(33)+")", referenceDiagnostic{Code: tc.code, Message: tc.message})
			s.errorStage = "execute"
			run(s)
			run(referencePrepared(s))
		}
		if b.pg {
			conn, err := b.db.Conn(p.ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			crun := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, b.recorder, s) }
			for _, q := range []string{"CREATE ROLE clauses_limit_owner", "CREATE SCHEMA clauses_limit_acl", "GRANT USAGE,CREATE ON SCHEMA clauses_limit_acl TO clauses_limit_owner", "SET ROLE clauses_limit_owner", "CREATE TABLE clauses_limit_acl.t(n INT)", "RESET ROLE", "REVOKE CREATE ON SCHEMA clauses_limit_acl FROM clauses_limit_owner,PUBLIC", "SET ROLE clauses_limit_owner"} {
				crun(referenceExec(q, 0))
			}
			s := referenceImplicitError("CREATE INDEX denied ON clauses_limit_acl.t("+list(33)+")", referenceDiagnostic{Code: "54011", Message: "cannot use more than 32 columns in an index"})
			s.errorStage = "execute"
			crun(s)
			crun(referencePrepared(s))
			crun(referenceExec("RESET ROLE", 0))
			crun(referenceExec("DROP SCHEMA clauses_limit_acl CASCADE", 0))
			crun(referenceExec("DROP ROLE clauses_limit_owner", 0))
		}
	})
}

func TestReferenceIndexClausesCollations(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE t(s TEXT)", 0))
		for i, c := range []string{`"default"`, `"C"`, `"POSIX"`, `pg_c_utf8`, `pg_unicode_fast`, `pg_catalog."C"`, `pg_catalog."POSIX"`} {
			run(referenceExec(fmt.Sprintf("CREATE INDEX c%d ON t(s COLLATE %s)", i, c), 0))
			if b.pg {
				run(referenceSelect("SELECT a.attcollation::int AS coll FROM pg_attribute a JOIN pg_class c ON a.attrelid=c.oid WHERE c.relname=$1 AND a.attnum=1", []string{"coll"}, []string{"INT4"}, [][]any{{[]int64{100, 950, 951, 811, 6411, 950, 951}[i]}}, fmt.Sprintf("c%d", i)))
			}
		}
		run(referenceExec("INSERT INTO t VALUES(''),('A'),('a'),('a '),('é'),('😀'),(NULL)", 7))
		s := referenceSelect("SELECT s FROM t ORDER BY s ASC NULLS LAST", []string{"s"}, []string{"TEXT"}, [][]any{{""}, {"A"}, {"a"}, {"a "}, {"é"}, {"😀"}, {nil}})
		run(s)
		run(referencePrepared(s))
		run(referenceExec("CREATE TABLE empty(s TEXT)", 0))
		run(referenceExec(`CREATE INDEX empty_c ON empty(s COLLATE "C")`, 0))
		run(referenceExec("INSERT INTO empty VALUES(NULL),(NULL)", 2))
		run(referenceExec(`CREATE INDEX null_c ON empty(s COLLATE "POSIX" DESC NULLS LAST)`, 0))
	})
	for _, definition := range []string{`(s COLLATE "C")`, `(n int4_ops)`, `(n) INCLUDE(s)`} {
		t.Run("migrated "+definition, func(t *testing.T) {
			indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
				run(referenceExec("CREATE TABLE t(n INT,c INT,s TEXT)", 0))
				run(referenceExec("INSERT INTO t VALUES(2,1,'one')", 1))
				run(referenceExec("CREATE INDEX i ON t"+definition, 0))
				run(referencePrepared(referenceExec("CREATE INDEX j ON t"+definition, 0)))
				for _, name := range []string{"i", "j"} {
					run(referenceSelect("SELECT relname,relkind FROM pg_class WHERE relname=$1", []string{"relname", "relkind"}, []string{"NAME", "CHAR"}, [][]any{{name, "i"}}, name))
				}
				run(referenceExec("CREATE INDEX baseline ON t(n)", 0))
				run(referencePrepared(referenceExec("INSERT INTO t VALUES(3,2,'two')", 1)))
				run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(2)}, {int64(3)}}))
				run(referenceExec("DELETE FROM t WHERE n=3", 1))
				run(referenceSelect("SELECT c,s FROM t", []string{"c", "s"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "one"}}))
				run(referenceExec("ALTER TABLE t ALTER c TYPE TEXT USING c::text", 0))
				run(referenceSelect("SELECT c FROM t", []string{"c"}, []string{"TEXT"}, [][]any{{"1"}}))
			})
		})
	}
}

func TestReferenceIndexClausesOperatorClasses(t *testing.T) {
	indexClausesScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, run func(referenceStep)) {
		run(referenceExec("CREATE DOMAIN dint AS integer", 0))
		// Direct REGCLASS storage remains excluded. This existing domain storage
		// path is independently covered by TestReferenceDomainBaseValues.
		run(referenceExec("CREATE DOMAIN dreg AS regclass", 0))
		run(referenceExec("CREATE TABLE t(n INT,s TEXT,v VARCHAR,c CHAR(4),d dint,r dreg)", 0))
		for i, elem := range []string{"n int4_ops", "n pg_catalog.int4_ops", "n oid_ops", "s text_ops", "s varchar_ops", "s bpchar_ops", "s text_pattern_ops", "s varchar_pattern_ops", "s bpchar_pattern_ops", "v text_ops", "v varchar_ops", "v bpchar_ops", "v text_pattern_ops", "v varchar_pattern_ops", "v bpchar_pattern_ops", "c bpchar_ops", "c bpchar_pattern_ops", "d int4_ops", "d oid_ops", "r oid_ops"} {
			run(referencePrepared(referenceExec(fmt.Sprintf("CREATE INDEX op%d ON t(%s) INCLUDE(c)", i, elem), 0)))
		}
		run(referenceExec("INSERT INTO t(n,s,v,c,d,r) VALUES(-1,'a ','a ','a ',-1,'t'),(1,'a','a','a',1,'t')", 2))
		run(referenceSelect("SELECT n FROM t ORDER BY n", []string{"n"}, []string{"INT4"}, [][]any{{int64(-1)}, {int64(1)}}))
		run(referenceSelect("SELECT s FROM t ORDER BY s", []string{"s"}, []string{"TEXT"}, [][]any{{"a"}, {"a "}}))
		run(referenceSelect("SELECT c FROM t WHERE c='a' ORDER BY n", []string{"c"}, []string{"BPCHAR"}, [][]any{{"a   "}, {"a   "}}))
		run(referenceSelect("SELECT r::text AS r FROM t ORDER BY n", []string{"r"}, []string{"TEXT"}, [][]any{{"t"}, {"t"}}))
	})
}
