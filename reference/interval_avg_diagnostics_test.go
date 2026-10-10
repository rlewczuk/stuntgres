package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func intervalAvgDiagnostic(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, query string, want referenceDiagnostic, args ...any) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		s := referenceImplicitError(query, want, args...)
		s.selectRows = true
		s.prepared = prepared
		s.errorStage = "execute"
		if prepared {
			s.errorStage = "prepare"
		}
		referenceRunStep(t, p.ctx, conn, recorder, s)
	}
}

func TestReferenceIntervalAvgDiagnostics(t *testing.T) {
	// Schema and role fixtures are cluster-wide/outside public: own their cleanup.
	roleReferenceCleanup(t, []string{"avg_acl_actor"})
	intervalAvgScenario(t, func(t *testing.T, p *referencePair, conn *sql.Conn, recorder *referenceRecorder, pg bool) {
		run := func(s referenceStep) { referenceRunStep(t, p.ctx, conn, recorder, s) }
		run(referenceExec("CREATE TABLE t(v INTERVAL)", 0))
		run(referenceExec("INSERT INTO t VALUES ('1 day')", 1))
		missingHint := "No function matches the given name and argument types. You might need to add explicit type casts."
		ambiguousHint := "Could not choose a best candidate function. You might need to add explicit type casts."
		cases := []struct {
			query, code, message, hint string
			position                   int
			args                       []any
		}{
			{"SELECT avg(NULL) FROM t", "42725", "function avg(unknown) is not unique", ambiguousHint, 8, nil},
			{"SELECT avg('1 day') FROM t", "42725", "function avg(unknown) is not unique", ambiguousHint, 8, nil},
			{"SELECT avg($1) FROM t", "42725", "function avg(unknown) is not unique", ambiguousHint, 8, []any{"1 day"}},
			{"SELECT avg(TRUE) FROM t", "42883", "function avg(boolean) does not exist", missingHint, 8, nil},
			{"SELECT avg('1 day'::text) FROM t", "42883", "function avg(text) does not exist", missingHint, 8, nil},
			{"SELECT avg() FROM t", "42883", "function avg() does not exist", missingHint, 8, nil},
			{"SELECT avg(*) FROM t", "42883", "function avg() does not exist", missingHint, 8, nil},
			{"SELECT avg(v,v) FROM t", "42883", "function avg(interval, interval) does not exist", missingHint, 8, nil},
			{"SELECT pg_catalog.avg(TRUE) FROM t", "42883", "function pg_catalog.avg(boolean) does not exist", missingHint, 8, nil},
			{"SELECT /*é*/ avg(TRUE) FROM t", "42883", "function avg(boolean) does not exist", missingHint, 14, nil},
			{"SELECT avg(avg(v)) FROM t", "42803", "aggregate function calls cannot be nested", "", 12, nil},
			{"SELECT v FROM t WHERE avg(v)=INTERVAL '0'", "42803", "aggregate functions are not allowed in WHERE", "", 23, nil},
			{"SELECT v FROM t GROUP BY avg(v)", "42803", "aggregate functions are not allowed in GROUP BY", "", 26, nil},
			{"SELECT v,avg(v) FROM t", "42803", "column \"t.v\" must appear in the GROUP BY clause or be used in an aggregate function", "", 8, nil},
			{"SELECT avg(absent) FROM t", "42703", "column \"absent\" does not exist", "", 12, nil},
			{"SELECT missing_avg_schema.avg(v) FROM t", "3F000", "schema \"missing_avg_schema\" does not exist", "", 8, nil},
			{"SELECT avg(TRUE) FROM t WHERE FALSE", "42883", "function avg(boolean) does not exist", missingHint, 8, nil},
		}
		for _, c := range cases {
			t.Run(c.query, func(t *testing.T) {
				intervalAvgDiagnostic(t, p, conn, recorder, c.query, referenceDiagnostic{Code: c.code, Message: c.message, Hint: c.hint, Position: referencePosition(c.position)}, c.args...)
			})
		}
		query := "SELECT avg(" + strings.TrimSuffix(strings.Repeat("v,", 101), ",") + ") FROM t"
		intervalAvgDiagnostic(t, p, conn, recorder, query, referenceDiagnostic{Code: "54023", Message: "cannot pass more than 100 arguments to a function", Position: "8"})
		query = "SELECT avg(" + strings.TrimSuffix(strings.Repeat("v,", 100), ",") + ") FROM t"
		intervalAvgDiagnostic(t, p, conn, recorder, query, referenceDiagnostic{Code: "42883", Message: "function avg(" + strings.TrimSuffix(strings.Repeat("interval, ", 100), ", ") + ") does not exist", Hint: missingHint, Position: "8"})
		run(referenceExec("DELETE FROM t", 1))
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT avg(TRUE) FROM t", referenceDiagnostic{Code: "42883", Message: "function avg(boolean) does not exist", Hint: missingHint, Position: "8"})
		run(referenceExec("INSERT INTO t VALUES ('1 day'),('3 days')", 2))
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT avg(INTERVAL 'nonsense') FROM t", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"nonsense\"", Position: "21"})
		for _, prepared := range []bool{false, true} {
			s := referenceImplicitError("SELECT avg($1::interval) FROM t", referenceDiagnostic{Code: "22007", Message: "invalid input syntax for type interval: \"nonsense\"", Context: "unnamed portal parameter $1 = '...'"}, "nonsense")
			s.selectRows = true
			s.prepared = prepared
			s.errorStage = "execute"
			run(s)
		}
		// All six numeric overloads now have one shared successful contract.
		for _, kind := range []string{"smallint", "integer", "bigint", "numeric", "real", "double precision"} {
			q := "SELECT avg(1::" + kind + ") FROM t"
			var value any = "1.00000000000000000000"
			typ := "NUMERIC"
			if kind == "real" || kind == "double precision" {
				value = float64(1)
				typ = "FLOAT8"
			}
			intervalAvgBoth(t, p, conn, recorder, referenceSelect(q, []string{"avg"}, []string{typ}, [][]any{{value}}))
		}
		for _, q := range []string{"SELECT avg(DISTINCT v) FROM t", "SELECT avg(ALL v) FROM t", "SELECT avg(v ORDER BY v) FROM t", "SELECT avg(v) FILTER (WHERE TRUE) FROM t", "SELECT avg(v) OVER () FROM t", "SELECT avg(INTERVAL '2 days')"} {
			if pg || !strings.Contains(q, "OVER") {
				rows := [][]any{{"2 days"}}
				if strings.Contains(q, "OVER") {
					rows = [][]any{{"2 days"}, {"2 days"}}
				}
				intervalAvgBoth(t, p, conn, recorder, referenceSelect(q, []string{"avg"}, []string{"INTERVAL"}, rows))
			} else {
				intervalAvgBoth(t, p, conn, recorder, referenceError(q, "0A000"))
			}
		}
		run(referenceExec("CREATE USER avg_acl_actor", 0))
		t.Cleanup(func() {
			for _, q := range []string{"RESET SESSION AUTHORIZATION", "DROP SCHEMA IF EXISTS avg_acl_owned CASCADE", "DROP SCHEMA IF EXISTS avg_acl_private CASCADE"} {
				if _, err := conn.ExecContext(p.ctx, q); err != nil {
					t.Error(err)
				}
			}
		})
		run(referenceExec("CREATE SCHEMA avg_acl_owned AUTHORIZATION avg_acl_actor", 0))
		run(referenceExec("CREATE SCHEMA avg_acl_private", 0))
		run(referenceExec("SET SESSION AUTHORIZATION avg_acl_actor", 0))
		run(referenceExec("CREATE TABLE avg_acl_owned.t(v INTERVAL)", 0))
		run(referenceExec("INSERT INTO avg_acl_owned.t VALUES ('1 day')", 1))
		intervalAvgDiagnostic(t, p, conn, recorder, "SELECT avg_acl_private.avg(v) FROM avg_acl_owned.t", referenceDiagnostic{Code: "42501", Message: "permission denied for schema avg_acl_private", Position: "8"})
	})
}
