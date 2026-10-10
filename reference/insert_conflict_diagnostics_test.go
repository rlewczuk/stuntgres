package reference_test

import (
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"
)

func TestReferenceInsertConflictDiagnostics(t *testing.T) {
	cases := []struct{ name, query, code, message, token, stage string }{
		{"no_match", "INSERT INTO t VALUES(1) ON CONFLICT(v) DO NOTHING", "42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification", "", "execute"},
		{"missing_constraint", "INSERT INTO t VALUES(1) ON CONFLICT ON CONSTRAINT absent DO NOTHING", "42704", "constraint \"absent\" for table \"t\" does not exist", "", "prepare"},
		{"index_not_constraint", "INSERT INTO t VALUES(1) ON CONFLICT ON CONSTRAINT ui DO NOTHING", "42704", "constraint \"ui\" for table \"t\" does not exist", "", "prepare"},
		{"check", "INSERT INTO t VALUES(1) ON CONFLICT ON CONSTRAINT positive DO NOTHING", "42809", "constraint in ON CONFLICT clause has no associated index", "", "execute"},
		{"missing_column", "INSERT INTO t VALUES(1) ON CONFLICT (missing) DO NOTHING", "42703", "column \"missing\" does not exist", "(missing)", "prepare"},
		{"descending", "INSERT INTO t VALUES(1) ON CONFLICT (k DESC) DO NOTHING", "42P10", "ASC/DESC is not allowed in ON CONFLICT clause", "(k DESC)", "prepare"},
		{"null_order", "INSERT INTO t VALUES(1) ON CONFLICT (k NULLS FIRST) DO NOTHING", "42P10", "NULLS FIRST/LAST is not allowed in ON CONFLICT clause", "(k NULLS FIRST)", "prepare"},
		{"utf8", "/*é*/ INSERT INTO t VALUES(1) ON CONFLICT (missing) DO NOTHING", "42703", "column \"missing\" does not exist", "(missing)", "prepare"},
		{"unknown_class", "INSERT INTO t VALUES(1) ON CONFLICT(k absent_ops) DO NOTHING", "42704", "operator class \"absent_ops\" does not exist for access method \"btree\"", "", "prepare"},
		{"wrong_class", "INSERT INTO t VALUES(1) ON CONFLICT(k text_ops) DO NOTHING", "42P10", "there is no unique or exclusion constraint matching the ON CONFLICT specification", "", "execute"},
		{"collation", "INSERT INTO t VALUES(1) ON CONFLICT(v COLLATE absent) DO NOTHING", "42704", "collation \"absent\" for encoding \"UTF8\" does not exist", "(v COLLATE", "prepare"},
		{"subquery_key_syntax", "INSERT INTO t VALUES(1) ON CONFLICT((SELECT 1)) DO NOTHING", "42601", "syntax error at or near \"SELECT\"", "SELECT", "prepare"},
		{"subquery_key", "INSERT INTO t VALUES(1) ON CONFLICT(((SELECT 1))) DO NOTHING", "0A000", "cannot use subquery in index expression", "(SELECT", "prepare"},
		{"subquery_predicate", "INSERT INTO t VALUES(1) ON CONFLICT(k) WHERE (SELECT TRUE) DO NOTHING", "0A000", "cannot use subquery in index predicate", "(SELECT", "prepare"},
		{"aggregate_key", "INSERT INTO t VALUES(1) ON CONFLICT(sum(k)) DO NOTHING", "42803", "aggregate functions are not allowed in index expressions", "sum(", "prepare"},
		{"aggregate_predicate", "INSERT INTO t VALUES(1) ON CONFLICT(k) WHERE sum(k)>0 DO NOTHING", "42803", "aggregate functions are not allowed in index predicates", "sum(", "prepare"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			d := controlsReferenceDiagnostic(c.code, c.message)
			if c.token != "" {
				d.Position = strconv.Itoa(utf8.RuneCountInString(c.query[:strings.Index(c.query, c.token)]) + 1)
			}
			for _, prepared := range []bool{false, true} {
				t.Run(strconv.FormatBool(prepared), func(t *testing.T) {
					insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
						run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
						run(referenceExec("CREATE TABLE t(k INT,v TEXT,CONSTRAINT positive CHECK(k>0))", 0))
						run(referenceExec("CREATE UNIQUE INDEX ui ON t(k)", 0))
						s := referenceErrorDiagnostic(c.query, d)
						s.prepared = prepared
						s.errorStage = "execute"
						if prepared {
							s.errorStage = c.stage
						}
						run(s)
						run(referenceSelect("SELECT k FROM t", []string{"k"}, []string{"INT4"}, nil))
					})
				})
			}
		})
	}
}
func TestReferenceInsertConflictProviderBoundaries(t *testing.T) {
	for _, mode := range []string{"DEFERRABLE", "DEFERRABLE INITIALLY IMMEDIATE", "DEFERRABLE INITIALLY DEFERRED"} {
		for _, target := range []string{"", "(k)", "ON CONSTRAINT u"} {
			t.Run(mode+target, func(t *testing.T) {
				insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
					q := "CREATE TABLE t(k INT CONSTRAINT u UNIQUE " + mode + ")"
					run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
					if !b.pg {
						run(referenceImplicitError(q, referenceDiagnostic{Code: "0A000", Message: "UNIQUE deferrability is unsupported", Position: referencePosition(strings.Index(q, "DEFERRABLE") + 1)}))
						run(referenceSelect("SELECT relname FROM pg_class WHERE relname='t'", []string{"relname"}, []string{"NAME"}, nil))
						return
					}
					run(referenceExec(q, 0))
					d := controlsReferenceDiagnostic("55000", "ON CONFLICT does not support deferrable unique constraints/exclusion constraints as arbiters")
					d.Schema, d.Table, d.Constraint = "public", "t", "u"
					for _, prepared := range []bool{false, true} {
						s := referenceErrorDiagnostic("INSERT INTO t VALUES(1) ON CONFLICT "+target+" DO NOTHING", d)
						s.prepared = prepared
						s.errorStage = "execute"
						run(s)
					}
					run(referenceExec("INSERT INTO t SELECT 1 WHERE FALSE ON CONFLICT "+target+" DO NOTHING", 0))
					run(insertConflictRows("SELECT k FROM t"))
				})
			})
		}
	}
	t.Run("exclusion", func(t *testing.T) {
		insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
			run := func(s referenceStep) { referenceRunStep(t, p.ctx, b.db, b.recorder, s) }
			q := "CREATE TABLE t(k INT4RANGE,CONSTRAINT ex EXCLUDE USING gist(k WITH &&))"
			if !b.pg {
				run(referenceImplicitError(q, referenceDiagnostic{Code: "42601", Message: "syntax error at or near \"EXCLUDE\"", Position: referencePosition(strings.Index(q, "EXCLUDE") + 1)}))
				run(referenceSelect("SELECT relname FROM pg_class WHERE relname='t'", []string{"relname"}, []string{"NAME"}, nil))
				return
			}
			run(referenceExec(q, 0))
			run(referenceExec("INSERT INTO t VALUES('[1,3)')", 1))
			run(referenceExec("INSERT INTO t VALUES('[2,4)'),('[5,7)') ON CONFLICT DO NOTHING", 1))
			run(referencePrepared(referenceExec("INSERT INTO t VALUES('[2,4)') ON CONFLICT ON CONSTRAINT ex DO NOTHING", 0)))
			run(insertConflictNoMatch("INSERT INTO t VALUES('[2,4)') ON CONFLICT(k) DO NOTHING"))
			run(referenceSelect("SELECT count(*) AS n FROM t", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		})
	})
}
func TestReferenceInsertConflictOracleQuestions(t *testing.T) {
	insertConflictScenario(t, func(t *testing.T, p *referencePair, b referenceBackend) {
		c := concurrentIndexConn(t, p, b)
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(k INT,n INT)", 0))
		run(referenceExec("CREATE UNIQUE INDEX ui ON t(k) WHERE n>0", 0))
		query := "INSERT INTO t VALUES($1,20) ON CONFLICT(k) WHERE n>$2 DO NOTHING"
		stmt, err := c.PrepareContext(p.ctx, query)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := referenceHeldStatement{referenceExecutor: c, statement: stmt, query: query}
		successfulCustom := 0
		for i, threshold := range []int64{10, 0, -1, 20, 10, 0, 10, 0, -1, 10, 0, 10} {
			t.Run("execution_"+strconv.Itoa(i+1)+"_threshold_"+strconv.FormatInt(threshold, 10), func(t *testing.T) {
				s := referenceExec(query, 1, int64(i+1), threshold)
				// Failed inference does not create a custom plan. PostgreSQL considers
				// an unspecialized generic plan after five completed custom plans; that
				// plan cannot prove n>$2 implies n>0. Native reproves current parameters
				// without a general PostgreSQL plan-cache implementation.
				if threshold < 0 || b.pg && successfulCustom >= 5 {
					s = insertConflictNoMatch(query)
					s.args = []any{int64(i + 1), threshold}
				}
				referenceRunStep(t, p.ctx, held, b.recorder, s)
				if s.state == "" {
					successfulCustom++
				}
			})
		}
		q := "INSERT INTO t VALUES(99,20) ON CONFLICT(k int4_ops(foo=1)) WHERE n>0 DO NOTHING"
		run(referenceExec(q, 1))
		run(referencePrepared(referenceExec(q, 0)))
		run(referenceSelect("SELECT k,n FROM t WHERE k=99", []string{"k", "n"}, []string{"INT4", "INT4"}, [][]any{{int64(99), int64(20)}}))
		want := [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(99)}}
		if !b.pg {
			want = [][]any{{int64(1)}, {int64(2)}, {int64(4)}, {int64(5)}, {int64(6)}, {int64(7)}, {int64(8)}, {int64(10)}, {int64(11)}, {int64(12)}, {int64(99)}}
		}
		run(referenceSelect("SELECT k FROM t ORDER BY k", []string{"k"}, []string{"INT4"}, want))
	})
}
