package reference_test

import (
	"database/sql"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQuerySearchDiagnostics(t *testing.T) {
	searchReference(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		base := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) "
		constant := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM r) "
		for _, mode := range searchModes {
			clause := searchClause(mode, "n")
			for _, tc := range []struct{ name, q, code, message, marker string }{
				{"nonrecursive", "WITH RECURSIVE r(n) AS (SELECT 1)" + clause + "SELECT * FROM r", "42601", "WITH query is not recursive", "r(n)"},
				{"no recursive modifier", "WITH r(n) AS (SELECT 1)" + clause + "SELECT * FROM r", "42601", "WITH query is not recursive", "r(n)"},
				{"unused nonrecursive", "WITH RECURSIVE r(n) AS (SELECT 1)" + clause + "SELECT 7", "42601", "WITH query is not recursive", "r(n)"},
				{"missing key", base + searchClause(mode, "missing") + "SELECT * FROM r", "42601", `search column "missing" not in WITH query column list`, "SEARCH"},
				{"duplicate key", base + searchClause(mode, "n,n") + "SELECT * FROM r", "42701", `search column "n" specified more than once`, "SEARCH"},
				{"SET conflict", constant + "SEARCH " + mode + " FIRST BY n SET n SELECT * FROM r", "42601", `search sequence column name "n" already used in WITH query column list`, "SEARCH"},
				{"missing before conflict", constant + "SEARCH " + mode + " FIRST BY missing SET n SELECT * FROM r", "42601", `search column "missing" not in WITH query column list`, "SEARCH"},
				{"ambiguous generated reference", base + "SEARCH " + mode + " FIRST BY n SET n SELECT * FROM r", "42702", `column reference "n" is ambiguous`, "n FROM"},
				{"source ambiguity before missing", base + "SEARCH " + mode + " FIRST BY missing SET n SELECT * FROM r", "42702", `column reference "n" is ambiguous`, "n FROM"},
				{"alias substitution missing original", "WITH RECURSIVE r(x) AS (SELECT 1 AS n UNION ALL SELECT x FROM r)" + searchClause(mode, "n") + "SELECT * FROM r", "42601", `search column "n" not in WITH query column list`, "SEARCH"},
				{"quoted duplicate", `WITH RECURSIVE r("N") AS (SELECT 1 UNION ALL SELECT "N" FROM r)` + searchClause(mode, `"N","N"`) + "SELECT * FROM r", "42701", `search column "N" specified more than once`, "SEARCH"},
				{"left UNION", "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT n FROM r)" + clause + "SELECT * FROM r", "0A000", "with a SEARCH or CYCLE clause, the left side of the UNION must be a SELECT", ""},
				{"right UNION", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 2))" + clause + "SELECT * FROM r", "42601", "with a SEARCH or CYCLE clause, the right side of the UNION must be a SELECT", ""},
				{"buried self", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM (SELECT n FROM r) s)" + clause + "SELECT * FROM r", "0A000", `with a SEARCH or CYCLE clause, the recursive reference to WITH query "r" must be at the top level of its right-hand SELECT`, ""},
				{"source before key", "WITH RECURSIVE r(n) AS (SELECT missing UNION ALL SELECT n FROM r)" + searchClause(mode, "absent") + "SELECT * FROM r", "42703", `column "missing" does not exist`, "missing UNION"},
				{"width before key", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1,2 FROM r)" + searchClause(mode, "missing") + "SELECT * FROM r", "42601", "each UNION query must have the same number of columns", "1,2"},
				{"union type before key", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 'x'::text FROM r)" + searchClause(mode, "missing") + "SELECT * FROM r", "42804", "UNION types integer and text cannot be matched", "'x'"},
				{"excess aliases", "WITH RECURSIVE r(n,x) AS (SELECT 1 UNION ALL SELECT n FROM r)" + clause + "SELECT * FROM r", "42P10", `WITH query "r" has 1 columns available but 2 columns specified`, "r(n,x)"},
				{"self aliases", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT x FROM r AS t(x,y))" + clause + "SELECT * FROM r", "42P10", `table "t" has 1 columns available but 2 columns specified`, ""},
			} {
				for _, prefix := range []string{"", "/*é*/ "} {
					t.Run(mode+" "+tc.name+prefix, func(t *testing.T) {
						q := prefix + tc.q
						want := cycleDiagnostic(tc.code, tc.message, q, tc.marker)
						searchAssertDiagnostic(t, p, b, c, q, &want)
					})
				}
			}
			for _, tc := range []struct{ name, suffix, token, marker string }{
				{"missing FIRST", "SEARCH " + mode + " BY n SET seq SELECT * FROM r", "BY", "BY n"},
				{"missing BY", "SEARCH " + mode + " FIRST n SET seq SELECT * FROM r", "n", "n SET"},
				{"missing SET", "SEARCH " + mode + " FIRST BY n seq SELECT * FROM r", "seq", "seq SELECT"},
				{"expression key", "SEARCH " + mode + " FIRST BY n+1 SET seq SELECT * FROM r", "+", "+"},
				{"qualified key", "SEARCH " + mode + " FIRST BY r.n SET seq SELECT * FROM r", ".", "."},
				{"descending key", "SEARCH " + mode + " FIRST BY n DESC SET seq SELECT * FROM r", "DESC", "DESC"},
				{"duplicate SEARCH", clause + clause + "SELECT * FROM r", "SEARCH", ""},
				{"trailing junk", clause + "SELECT * FROM r junk junk", "junk", ""},
			} {
				t.Run(mode+" grammar "+tc.name, func(t *testing.T) {
					q := "/*é*/ " + base + tc.suffix
					want := cycleDiagnostic("42601", `syntax error at or near "`+tc.token+`"`, q, tc.marker)
					if tc.marker == "" {
						want.Position = fixture.Position(len([]rune(q[:strings.LastIndex(q, tc.token)])) + 1)
					}
					searchAssertDiagnostic(t, p, b, c, q, &want)
				})
			}
			t.Run(mode+" recursive type mismatch", func(t *testing.T) {
				q := "/*é*/ WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n::bigint FROM r)" + clause + "SELECT * FROM r"
				want := cycleDiagnostic("42804", `recursive query "r" column 1 has type integer in non-recursive term but type bigint overall`, q, "1 UNION")
				want.Hint = "Cast the output of the non-recursive term to the correct type."
				searchAssertDiagnostic(t, p, b, c, q, &want)
			})
		}
	})
}

// Analysis failures reject Prepare; pseudo-output and reached JSON comparison
// failures prepare successfully and reject execution without publishing results.
func searchAssertDiagnostic(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q string, want *fixture.Diagnostic) {
	t.Helper()
	if want == nil {
		t.Fatal("missing expected diagnostic")
	}
	executionFailure := want.Code == "42P16" || want.Code == "42883"
	for _, route := range []string{"QueryContext", "ExecContext", "PrepareContext/Query", "PrepareContext/Exec", "Query", "Exec", "Prepare/Query", "Prepare/Exec"} {
		t.Run(route, func(t *testing.T) {
			phase := "execute"
			var err error
			var rows *sql.Rows
			var result sql.Result
			var stmt *sql.Stmt
			switch route {
			case "QueryContext":
				rows, err = c.QueryContext(p.Ctx, q)
			case "ExecContext":
				result, err = c.ExecContext(p.Ctx, q)
			case "Query":
				rows, err = b.DB.Query(q)
			case "Exec":
				result, err = b.DB.Exec(q)
			default:
				phase = "prepare"
				if route == "Prepare/Query" || route == "Prepare/Exec" {
					stmt, err = b.DB.Prepare(q)
				} else {
					stmt, err = c.PrepareContext(p.Ctx, q)
				}
				if executionFailure && err != nil {
					t.Fatalf("expected successful Prepare before execution failure: %v", err)
				}
				if !executionFailure && err == nil {
					if stmt != nil {
						stmt.Close()
					}
					t.Fatal("expected analysis failure at Prepare")
				}
				if stmt != nil {
					defer stmt.Close()
					if err != nil {
						t.Error("failed Prepare published statement")
					}
				}
				if err == nil && stmt != nil {
					phase = "execute held"
					switch route {
					case "PrepareContext/Query":
						rows, err = stmt.QueryContext(p.Ctx)
					case "PrepareContext/Exec":
						result, err = stmt.ExecContext(p.Ctx)
					case "Prepare/Query":
						rows, err = stmt.Query()
					case "Prepare/Exec":
						result, err = stmt.Exec()
					}
				}
			}
			if result != nil {
				t.Error("failed Exec published result")
			}
			if rows != nil {
				t.Error("failed Query published Rows")
			}
			if rows != nil {
				defer rows.Close()
				if err != nil {
					t.Error("failed Query published Rows")
				}
				if err == nil {
					for rows.Next() {
					}
					err = rows.Err()
					phase += " stream"
				}
			}
			if err == nil {
				t.Fatal("expected diagnostic, got success")
			}
			got := fixture.ExtractDiagnostic(t, err)
			expectedPhase := "execute"
			if strings.HasPrefix(route, "Prepare") {
				expectedPhase = "prepare"
				if executionFailure {
					expectedPhase = "execute held"
				}
			}
			if phase != expectedPhase {
				t.Errorf("failure phase=%s want=%s", phase, expectedPhase)
			}
			if !reflect.DeepEqual(got, *want) {
				t.Errorf("diagnostic=%+v want=%+v", got, *want)
			}
		})
	}
}
