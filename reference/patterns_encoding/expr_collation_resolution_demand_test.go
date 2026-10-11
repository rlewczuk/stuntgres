package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func exprCollationDemand(q string, args ...any) fixture.Step {
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P22", Message: "could not determine which collation to use for string comparison", Hint: "Use the COLLATE clause to set the collation explicitly."}, args...)
	s.SelectRows = true
	s.ErrorStage = "execute"
	return s
}

func exprCollationExplicit(q, left, right string) fixture.Step {
	pos := strings.LastIndex(q, "COLLATE")
	s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P21", Message: `collation mismatch between explicit collations "` + left + `" and "` + right + `"`, Position: strconv.Itoa(utf8.RuneCountInString(q[:pos]) + 1)})
	s.SelectRows = true
	return s
}

func exprCollationCases(t *testing.T, p *fixture.Pair, b fixture.Backend, cases []fixture.Step) {
	t.Helper()
	for _, s := range cases {
		t.Run(s.Query, func(t *testing.T) { fixture.CollationRoutes(t, p, b, s) })
	}
}

func exprCollationTable(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	fixture.CollationExec(t, p, b, `CREATE COLLATION expr_coll_copy FROM pg_catalog."C"`, `CREATE TABLE expr_coll_t(id INT,a TEXT COLLATE "C",b TEXT COLLATE "POSIX")`)
	fixture.CollationStep(t, p, b, fixture.Exec(`INSERT INTO expr_coll_t VALUES(1,'A','A'),(2,'a','A'),(3,NULL,'A'),(4,'A',NULL),(5,NULL,NULL)`, 5))
}

func TestReferenceExprCollationResolution(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		exprCollationCases(t, p, b, []fixture.Step{
			fixture.Select(`SELECT a='A' AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT a COLLATE "C"=b AS v,b=a COLLATE "POSIX" AS w FROM expr_coll_t WHERE id=1`, []string{"v", "w"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}),
			fixture.Select(`SELECT (a COLLATE "C") COLLATE "POSIX"=b AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			fixture.Select(`SELECT b COLLATE "C"=a AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			exprCollationExplicit(`SELECT a COLLATE "C"=b COLLATE "POSIX" FROM expr_coll_t WHERE FALSE`, "C", "POSIX"),
			exprCollationExplicit(`SELECT NULL::text COLLATE "C"=NULL::text COLLATE "POSIX"`, "C", "POSIX"),
			exprCollationExplicit(`SELECT CASE WHEN FALSE THEN a COLLATE "C"=b COLLATE "POSIX" ELSE TRUE END FROM expr_coll_t`, "C", "POSIX"),
			exprCollationExplicit(`SELECT a COLLATE "C"=b COLLATE expr_coll_copy FROM expr_coll_t`, "C", "expr_coll_copy"),
		})
		for _, q := range []string{`SELECT 1 COLLATE "C"`, `SELECT 1 COLLATE expr_coll_missing`, `SELECT TRUE COLLATE "C"`, `SELECT (a=b) COLLATE "C" FROM expr_coll_t`} {
			typ := "integer"
			if strings.Contains(q, "TRUE") || strings.Contains(q, "a=b") {
				typ = "boolean"
			}
			d := fixture.Diagnostic{Code: "42804", Message: "collations are not supported by type " + typ, Position: strconv.Itoa(strings.Index(q, "COLLATE") + 1)}
			t.Run(q, func(t *testing.T) { fixture.CollationError(t, p, b, q, d) })
		}
		// Operator admission precedes collation assignment.
		q := `SELECT a COLLATE "C"=1 FROM expr_coll_t`
		fixture.CollationError(t, p, b, q, fixture.Diagnostic{Code: "42883", Message: "operator does not exist: text = integer", Hint: "No operator matches the given name and argument types. You might need to add explicit type casts.", Position: strconv.Itoa(strings.Index(q, "=") + 1)})
	})
}

func TestReferenceExprCollationDemand(t *testing.T) {
	fixture.CollationScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		exprCollationTable(t, p, b)
		var cases []fixture.Step
		for _, op := range []string{"=", "<>", "<", "<=", ">", ">="} {
			cases = append(cases, exprCollationDemand(`SELECT a`+op+`b AS v FROM expr_coll_t WHERE id=1`),
				fixture.Select(`SELECT a`+op+`b AS v FROM expr_coll_t WHERE FALSE`, []string{"v"}, []string{"BOOL"}, nil),
				fixture.Select(`SELECT a`+op+`b AS v FROM expr_coll_t WHERE id>=3 ORDER BY id`, []string{"v"}, []string{"BOOL"}, [][]any{{nil}, {nil}, {nil}}))
		}
		cases = append(cases,
			fixture.Select(`SELECT a||b AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"TEXT"}, [][]any{{"AA"}}),
			exprCollationDemand(`SELECT (a||b)='AA' AS v FROM expr_coll_t WHERE id=1`),
			fixture.Select(`SELECT (a||b) COLLATE "C"='AA' AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{true}}),
			exprCollationDemand(`SELECT (a||b)=$1::text AS v FROM expr_coll_t WHERE id=1`, "AA"),
			fixture.Select(`SELECT (a||b)=$1::text AS v FROM expr_coll_t WHERE id=1`, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}, nil),
			fixture.Select(`SELECT CASE WHEN id=0 THEN a=b ELSE TRUE END AS v FROM expr_coll_t ORDER BY id`, []string{"v"}, []string{"BOOL"}, [][]any{{true}, {true}, {true}, {true}, {true}}),
			fixture.Select(`SELECT a=b AS v FROM expr_coll_t WHERE id=0`, []string{"v"}, []string{"BOOL"}, nil))
		exprCollationCases(t, p, b, cases)
		// Execute both values under one retained prepared signature.
		for _, nonContext := range []bool{false, true} {
			t.Run("retained-parameter-signature-"+strconv.FormatBool(nonContext), func(t *testing.T) {
				q := `SELECT (a||b)=$1::text AS v FROM expr_coll_t WHERE id=1`
				var stmt *sql.Stmt
				var err error
				if nonContext {
					stmt, err = b.DB.Prepare(q)
				} else {
					stmt, err = b.DB.PrepareContext(p.Ctx, q)
				}
				if err != nil {
					t.Fatal(err)
				}
				defer stmt.Close()
				var rows *sql.Rows
				if nonContext {
					rows, err = stmt.Query("AA")
				} else {
					rows, err = stmt.QueryContext(p.Ctx, "AA")
				}
				if rows != nil {
					rows.Close()
				}
				fixture.AssertControlDiagnostic(t, err, *exprCollationDemand(q).Diagnostic)
				if nonContext {
					rows, err = stmt.Query(nil)
				} else {
					rows, err = stmt.QueryContext(p.Ctx, nil)
				}
				fixture.DeleteReferenceConsume(t, b.PG, rows, err, fixture.Select(q, []string{"v"}, []string{"BOOL"}, [][]any{{nil}}))
			})
		}
	})
}
