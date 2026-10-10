package reference_test

import (
	"database/sql"
	"strings"
	"testing"
)

func TestReferenceAggregateModifiersDiagnostics(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_diag(v INT)", 0))
		run(referenceExec("INSERT INTO am_diag VALUES(1),(2)", 2))
		for _, tc := range []struct{ expr, code, msg, token string }{
			{"count(v) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)"},
			{"count(v) FILTER(WHERE $1::integer)", "42804", "argument of FILTER must be type boolean, not type integer", "$1"},
			{"count(missing) FILTER(WHERE 1)", "42703", "column \"missing\" does not exist", "missing"},
			{"absent.count(v) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)"},
			{"count(v,v ORDER BY missing)", "42883", "function count(integer, integer) does not exist", "count"},
			{"count(v ORDER BY missing)", "42703", "column \"missing\" does not exist", "missing"},
			{"count(DISTINCT v ORDER BY v+1)", "42P10", "in an aggregate with DISTINCT, ORDER BY expressions must appear in argument list", "v+1"},
			{"count(count(v))", "42803", "aggregate function calls cannot be nested", "count(v)"},
			{"count(v ORDER BY count(v))", "42803", "aggregate function calls cannot be nested", "count(v))"},
			{"count(v) FILTER(WHERE count(v)>0)", "42803", "aggregate functions are not allowed in FILTER", "count(v)>"},
		} {
			for _, suffix := range []string{"", " WHERE FALSE"} {
				q := "/* é */ SELECT " + tc.expr + " FROM am_diag" + suffix
				s := aggregateModifiersError(q, tc.code, tc.msg, tc.token)
				if tc.code == "42883" {
					s.diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				if strings.Contains(q, "$1") {
					s.args = []any{1}
				}
				aggregateModifiersBoth(run, s)
			}
		}
		q := "SELECT v AS alias,count(v ORDER BY alias) FROM am_diag GROUP BY v"
		aggregateModifiersBoth(run, aggregateModifiersError(q, "42703", "column \"alias\" does not exist", "alias)"))
		for _, tc := range []struct{ q, token string }{
			{"SELECT count(DISTINCT *) FROM am_diag", "*"},
			{"SELECT count(ALL *) FROM am_diag", "*"},
			{"SELECT count(* ORDER BY v) FROM am_diag", "ORDER"},
		} {
			aggregateModifiersBoth(run, aggregateModifiersError(tc.q, "42601", "syntax error at or near \""+tc.token+"\"", tc.token))
		}
		run(referenceSelect("SELECT v FROM am_diag ORDER BY v", []string{"v"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
	})
}

func TestReferenceAggregateModifiersAnalysisPrecedence(t *testing.T) {
	aggregateModifiersScenario(t, func(t *testing.T, p *referencePair, b referenceBackend, c *sql.Conn, run func(referenceStep)) {
		run(referenceExec("CREATE TABLE am_precedence(v INT)", 0))
		args := strings.TrimSuffix(strings.Repeat("v,", 101), ",")
		for _, tc := range []struct{ expr, code, msg, token string }{
			{"count(" + args + ") FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)"},
			{"absent.count(v,v) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)"},
			{"count(v) FILTER(WHERE missing)", "42703", "column \"missing\" does not exist", "missing"},
			{"count(v ORDER BY missing) FILTER(WHERE 1)", "42804", "argument of FILTER must be type boolean, not type integer", "1)"},
		} {
			aggregateModifiersBoth(run, aggregateModifiersError("SELECT "+tc.expr+" FROM am_precedence", tc.code, tc.msg, tc.token))
		}
	})
}
