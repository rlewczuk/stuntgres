package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceQueryCTEDiagnostics(t *testing.T) {
	cteReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := subqueryRun(t, p, b, c)
		forwardHint := "Use WITH RECURSIVE, or re-order the WITH items to remove forward references."
		for _, tc := range []struct{ q, code, message, position, detail, hint string }{
			{"WITH c AS (SELECT 1), c AS (SELECT 2) SELECT * FROM c", "42712", `WITH query name "c" specified more than once`, "23", "", ""},
			{"WITH c AS (SELECT missing), c AS (SELECT 2) SELECT 7", "42712", `WITH query name "c" specified more than once`, "29", "", ""},
			{"WITH c(a,b) AS (SELECT 1) SELECT * FROM c", "42P10", `WITH query "c" has 1 columns available but 2 columns specified`, "6", "", ""},
			{"WITH a AS (SELECT * FROM b), b AS (SELECT 1) SELECT * FROM a", "42P01", `relation "b" does not exist`, "26", `There is a WITH item named "b", but it cannot be referenced from this part of the query.`, forwardHint},
			{"WITH c AS (SELECT * FROM c) SELECT * FROM c", "42P01", `relation "c" does not exist`, "26", `There is a WITH item named "c", but it cannot be referenced from this part of the query.`, forwardHint},
			{"WITH c AS (SELECT 1 AS x) SELECT missing FROM c", "42703", `column "missing" does not exist`, "34", "", ""},
			{"WITH c(x,x) AS (SELECT 1,2) SELECT x FROM c", "42702", `column reference "x" is ambiguous`, "36", "", ""},
			{"WITH c AS (SELECT 1 AS x) SELECT c.x FROM c AS z", "42P01", `invalid reference to FROM-clause entry for table "c"`, "34", "", `Perhaps you meant to reference the table alias "z".`},
			{"WITH c AS (SELECT 1 AS x) SELECT * FROM c,c", "42712", `table name "c" specified more than once`, "", "", ""},
			{"WITH c AS SELECT 1 SELECT * FROM c", "42601", `syntax error at or near "SELECT"`, "11", "", ""},
			{"WITH c AS (SELECT 1) SELECT * FROM", "42601", "syntax error at end of input", "35", "", ""},
			{"WITH c AS MATERIALIZED SELECT 1 SELECT * FROM c", "42601", `syntax error at or near "SELECT"`, "24", "", ""},
			{"WITH c AS (SELECT * FROM absent) SELECT 7", "42P01", `relation "absent" does not exist`, "26", "", ""},
			{"WITH c AS (SELECT 1 AS x) SELECT x", "42703", `column "x" does not exist`, "34", "", ""},
			{"WITH c AS (SELECT 1 AS x) SELECT * FROM c z(a,b)", "42P10", `table "z" has 1 columns available but 2 columns specified`, "", "", ""},
			{"/* é */ WITH c AS (SELECT 1 AS x) SELECT missing FROM c", "42703", `column "missing" does not exist`, "42", "", ""},
			{"WITH \"C\" AS (SELECT 1), \"C\" AS (SELECT 2) SELECT 7", "42712", `WITH query name "C" specified more than once`, "25", "", ""},
			{"WITH c AS (SELECT missing) SELECT 7", "42703", `column "missing" does not exist`, "19", "", ""},
		} {
			t.Run(tc.q, func(t *testing.T) {
				childRun := subqueryRun(t, p, b, c)
				want := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				want.Position, want.Detail, want.Hint = tc.position, tc.detail, tc.hint
				subqueryAnalysisFailure(childRun, tc.q, want)
			})
		}
		name := strings.Repeat("a", 63)
		q := "WITH \"" + name + "x\" AS (SELECT 1), \"" + name + "y\" AS (SELECT 2) SELECT 7"
		want := fixture.ControlsReferenceDiagnostic("42712", `WITH query name "`+name+`" specified more than once`)
		want.Position = "88"
		subqueryAnalysisFailure(run, q, want)
		// Full-source parsing must not execute an otherwise valid volatile prefix.
		run(fixture.Exec("CREATE SEQUENCE s", 0))
		q = "WITH c AS (SELECT nextval('s')) SELECT * FROM c nonsense nonsense"
		want = fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "nonsense"`)
		want.Position = "58"
		subqueryAnalysisFailure(run, q, want)
		run(cteBag("SELECT nextval('s') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
	})
}
