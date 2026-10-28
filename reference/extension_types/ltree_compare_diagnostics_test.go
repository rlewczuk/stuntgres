package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func ltreeComparisonDiagnosticChecks(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	// Complete records are release-source inferences pending pinned oracle proof.
	for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
		for _, operands := range []struct{ left,right,leftType,rightType string }{
			{"'a'::ltree", "'a'::text", "ltree", "text"},
			{"'a'::text", "'a'::ltree", "text", "ltree"},
			{"NULL::ltree", "'a'::text", "ltree", "text"},
		} {
			q := "SELECT " + operands.left + " " + op + " " + operands.right
			want := fixture.ControlsReferenceDiagnostic("42883", "operator does not exist: "+operands.leftType+" "+op+" "+operands.rightType)
			want.Hint = "No operator matches the given name and argument types. You might need to add explicit type casts."
			want.Position = strconv.Itoa(len("SELECT ")+len(operands.left)+2)
			for _, prepared := range []bool{false,true} {
				ltreeError(t, p, b, c, q, prepared, true, true, want)
			}
		}
	}
	want := fixture.ControlsReferenceDiagnostic("42601", "ltree syntax error at character 3")
	want.Position = "8"
	for _, q := range []string{"SELECT 'a..b'::ltree = 'a'::ltree", "SELECT 'a..b' = 'a'::ltree"} {
		for _, prepared := range []bool{false,true} {
			ltreeError(t, p, b, c, q, prepared, true, true, want)
		}
	}
	q := "SELECT $1::ltree = $2::ltree AS eq"
	stmt, err := c.PrepareContext(p.Ctx, q)
	if err != nil { t.Fatal(err) }
	defer stmt.Close()
	held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
	for i, args := range [][]any{{"a..b","a"},{"a","a..b"}} {
		want.Position = ""
		want.Context = "unnamed portal parameter $"+strconv.Itoa(i+1)+" = '...'"
		s := fixture.ErrorDiagnostic(q, want, args...)
		s.SelectRows, s.ErrorStage = true, "execute"
		ltreeComparisonRun(t, p, b, held, s)
		ltreeComparisonRun(t, p, b, held, fixture.Select(q, []string{"eq"}, []string{"BOOL"}, [][]any{{true}}, "a.b", []byte("a.b")))
	}
	// Failed row conversion cannot publish a preceding valid key or move old keys.
	ltreeComparisonRun(t, p, b, c, fixture.Exec("CREATE TABLE lt_cmp_diag(id integer,v ltree)", 0))
	ltreeComparisonRun(t, p, b, c, fixture.Exec("CREATE INDEX lt_cmp_diag_key ON lt_cmp_diag(v)", 0))
	ltreeComparisonRun(t, p, b, c, fixture.Exec("INSERT INTO lt_cmp_diag VALUES (1,'a.b'),(2,'a-b')", 2))
	for _, query := range []string{"INSERT INTO lt_cmp_diag VALUES (3,'new'),(4,'a..b')", "UPDATE lt_cmp_diag SET v='a..b' WHERE id=1"} {
		want = fixture.ControlsReferenceDiagnostic("42601", "ltree syntax error at character 3")
		want.Position = strconv.Itoa(strings.Index(query, "'a..b'")+1)
		ltreeError(t, p, b, c, query, false, false, false, want)
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_diag WHERE v='a.b'::ltree ORDER BY id", 1))
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_diag WHERE v='new'::ltree ORDER BY id"))
		ltreeComparisonRun(t, p, b, c, ltreeComparisonIDs("SELECT id FROM lt_cmp_diag ORDER BY id", 1,2))
	}
}

func TestReferenceLtreeComparisonDiagnostics(t *testing.T) {
	ltreeComparisonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		ltreeInstall(t, p, b, c)
		ltreeComparisonDiagnosticChecks(t, p, b, c)
	})
}
