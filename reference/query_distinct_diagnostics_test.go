package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"
)

func queryDistinctPosition(q, token string) string {
	return referencePosition(utf8.RuneCountInString(q[:strings.LastIndex(q, token)]) + 1)
}

func TestReferenceQueryDistinctDiagnostics(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(a INT,b INT)", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO t VALUES(1,2),(1,2)", 2))
			}
			for _, tc := range []struct{ q, code, message, token string }{
				{"SELECT DISTINCT a FROM t ORDER BY b", "42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", "b"},
				{"SELECT /*é*/ DISTINCT a FROM t ORDER BY b", "42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", "b"},
				{"SELECT DISTINCT a FROM t ORDER BY a+0", "42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", "a+0"},
				{"SELECT DISTINCT a AS b FROM t ORDER BY t.b", "42P10", "for SELECT DISTINCT, ORDER BY expressions must appear in select list", "t.b"},
				{"SELECT DISTINCT setseed(0) FROM t", "42883", "could not identify an equality operator for type void", "setseed"},
				{"SELECT DISTINCT FROM t", "42601", `syntax error at or near "FROM"`, "FROM"},
				{"SELECT DISTINCT DISTINCT a FROM t", "42601", `syntax error at or near "DISTINCT"`, "DISTINCT a"},
				{"SELECT DISTINCT absent FROM t", "42703", `column "absent" does not exist`, "absent"},
				{"SELECT DISTINCT a AS x FROM t ORDER BY absent", "42703", `column "absent" does not exist`, "absent"},
				{"SELECT DISTINCT a AS x,b AS x FROM t ORDER BY x", "42702", `ORDER BY "x" is ambiguous`, "x"},
				{"SELECT DISTINCT a FROM t ORDER BY 0", "42P10", "ORDER BY position 0 is not in select list", "0"},
				{"SELECT DISTINCT a FROM t ORDER BY 2", "42P10", "ORDER BY position 2 is not in select list", "2"},
			} {
				t.Run(tc.q, func(t *testing.T) {
					want := referenceDiagnostic{Code: tc.code, Message: tc.message}
					if tc.token != "" {
						want.Position = queryDistinctPosition(tc.q, tc.token)
					}
					queryDistinctAnalysis(t, p, b, c, tc.q, want)
				})
			}
			for _, mode := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
				q := "SELECT DISTINCT a FROM t FOR " + mode
				queryDistinctAnalysis(t, p, b, c, q, referenceDiagnostic{Code: "0A000", Message: "FOR " + mode + " is not allowed with DISTINCT clause"})
				q = "SELECT DISTINCT a FROM t ORDER BY b FOR " + mode
				queryDistinctAnalysis(t, p, b, c, q, referenceDiagnostic{Code: "42P10", Message: "for SELECT DISTINCT, ORDER BY expressions must appear in select list", Position: queryDistinctPosition(q, "b FOR")})
			}
		}
		run(referenceExec("CREATE VIEW dv AS SELECT DISTINCT a FROM t", 0))
		s := referenceImplicitError("SELECT a FROM dv FOR UPDATE", referenceDiagnostic{Code: "0A000", Message: "FOR UPDATE is not allowed with DISTINCT clause"})
		s.selectRows, s.errorStage = true, "execute"
		queryDistinctBoth(run, s)
	})
}

func TestReferenceQueryDistinctDiagnosticsZeroColumns(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE zero_columns ()", 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO zero_columns DEFAULT VALUES", 1))
				run(referenceExec("INSERT INTO zero_columns DEFAULT VALUES", 1))
			}
			queryDistinctAnalysis(t, p, b, c, "SELECT DISTINCT * FROM zero_columns", referenceDiagnostic{Code: "42601", Message: "SELECT DISTINCT must have at least one column"})
		}
	})
}

func TestReferenceQueryDistinctDiagnosticsNoEffects(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec("CREATE TABLE t(id INT)", 0))
		run(referenceExec("CREATE SEQUENCE s START WITH 1", 0))
		seed := func() {
			var v any
			if err := c.QueryRowContext(p.ctx, "SELECT setseed(.25)").Scan(&v); err != nil {
				t.Fatal(err)
			}
		}
		draw := func() float64 {
			var v float64
			if err := c.QueryRowContext(p.ctx, "SELECT random()").Scan(&v); err != nil {
				t.Fatal(err)
			}
			return v
		}
		seed()
		draw()
		continuation := draw()
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO t VALUES(1),(2),(3)", 3))
			}
			for _, q := range []string{"SELECT DISTINCT setseed(0) FROM t", "SELECT DISTINCT nextval('s'),setseed(0) FROM t"} {
				for _, prepared := range []bool{false, true} {
					run(referenceExec("ALTER SEQUENCE s RESTART WITH 1", 0))
					seed()
					draw()
					s := referenceImplicitError(q, referenceDiagnostic{Code: "42883", Message: "could not identify an equality operator for type void", Position: queryDistinctPosition(q, "setseed")})
					s.selectRows = true
					s.errorStage = "execute"
					if prepared {
						s = referencePrepared(s)
						s.errorStage = "prepare"
					}
					run(s)
					if got := draw(); got != continuation {
						t.Fatal("analysis evaluated seed", got, continuation)
					}
					var next int64
					if err := c.QueryRowContext(p.ctx, "SELECT nextval('s')").Scan(&next); err != nil || next != 1 {
						t.Fatal("analysis evaluated sequence", next, err)
					}
				}
			}
		}
	})
}

func TestReferenceQueryDistinctDiagnosticsCollation(t *testing.T) {
	queryDistinctScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, c, b.recorder, s) }
		run(referenceExec(`CREATE TABLE coll(a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`, 0))
		for _, populated := range []bool{false, true} {
			if populated {
				run(referenceExec("INSERT INTO coll VALUES('a','b'),('a','b')", 2))
			}
			q := `SELECT DISTINCT COALESCE(a COLLATE "C",b COLLATE "POSIX") FROM coll`
			queryDistinctAnalysis(t, p, b, c, q, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "C" and "POSIX"`, Position: queryDistinctPosition(q, `COLLATE "POSIX"`)})
			q = "SELECT DISTINCT COALESCE(a,b) AS v FROM coll"
			queryDistinctAnalysis(t, p, b, c, q, referenceDiagnostic{Code: "42P21", Message: `collation mismatch between implicit collations "C" and "POSIX"`, Hint: "You can choose the collation by applying the COLLATE clause to one or both expressions.", Position: "28"})
		}
	})
}
