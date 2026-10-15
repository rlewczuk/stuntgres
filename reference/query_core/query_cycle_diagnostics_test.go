package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceQueryCycleDiagnostics(t *testing.T) {
	p := fixture.NewScenario(t)
	for _, backend := range p.Backends() {
		t.Run(backend.Name, func(t *testing.T) {
			c := cycleConn(t, p, backend)
			base := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM r) "
			constantBase := "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1 FROM r) "
			for _, tc := range []struct{ name, q, code, message, marker string }{
				{"nonrecursive", "WITH RECURSIVE r(n) AS (SELECT 1) CYCLE n SET cyc USING path SELECT * FROM r", "42601", "WITH query is not recursive", "r(n)"},
				{"without recursive modifier", "WITH r(n) AS (SELECT 1) CYCLE n SET cyc USING path SELECT * FROM r", "42601", "WITH query is not recursive", "r(n)"},
				{"unused nonrecursive", "WITH RECURSIVE r(n) AS (SELECT 1) CYCLE n SET cyc USING path SELECT 7", "42601", "WITH query is not recursive", "r(n)"},
				{"missing key", base + "CYCLE x SET cyc USING path SELECT * FROM r", "42601", `cycle column "x" not in WITH query column list`, "CYCLE"},
				{"duplicate key", base + "CYCLE n,n SET cyc USING path SELECT * FROM r", "42701", `cycle column "n" specified more than once`, "CYCLE"},
				{"ambiguous mark reference", base + "CYCLE n SET n USING path SELECT * FROM r", "42702", `column reference "n" is ambiguous`, "n FROM"},
				{"ambiguous path reference", base + "CYCLE n SET cyc USING n SELECT * FROM r", "42702", `column reference "n" is ambiguous`, "n FROM"},
				{"mark conflict", constantBase + "CYCLE n SET n USING path SELECT * FROM r", "42601", `cycle mark column name "n" already used in WITH query column list`, "CYCLE"},
				{"path conflict", constantBase + "CYCLE n SET cyc USING n SELECT * FROM r", "42601", `cycle path column name "n" already used in WITH query column list`, "CYCLE"},
				{"generated names equal", base + "CYCLE n SET cyc USING cyc SELECT * FROM r", "42601", "cycle mark column name and cycle path column name are the same", "CYCLE"},
				{"ambiguous alias mark reference", "WITH RECURSIVE r(x) AS (SELECT 1 AS n UNION ALL SELECT x FROM r) CYCLE x SET x USING path SELECT * FROM r", "42702", `column reference "x" is ambiguous`, "x FROM"},
				{"ambiguous alias path reference", "WITH RECURSIVE r(x) AS (SELECT 1 AS n UNION ALL SELECT x FROM r) CYCLE x SET cyc USING x SELECT * FROM r", "42702", `column reference "x" is ambiguous`, "x FROM"},
				{"alias mark conflict", "WITH RECURSIVE r(x) AS (SELECT 1 AS n UNION ALL SELECT 1 FROM r) CYCLE x SET x USING path SELECT * FROM r", "42601", `cycle mark column name "x" already used in WITH query column list`, "CYCLE"},
				{"alias path conflict", "WITH RECURSIVE r(x) AS (SELECT 1 AS n UNION ALL SELECT 1 FROM r) CYCLE x SET cyc USING x SELECT * FROM r", "42601", `cycle path column name "x" already used in WITH query column list`, "CYCLE"},
				{"ambiguous reference before missing key", base + "CYCLE x SET n USING n SELECT * FROM r", "42702", `column reference "n" is ambiguous`, "n FROM"},
				{"missing key beats name conflict", constantBase + "CYCLE x SET n USING n SELECT * FROM r", "42601", `cycle column "x" not in WITH query column list`, "CYCLE"},
				{"unused missing key", base + "CYCLE x SET cyc USING path SELECT 7", "42601", `cycle column "x" not in WITH query column list`, "CYCLE"},
				{"quoted duplicate", `WITH RECURSIVE r("N") AS (SELECT 1 UNION ALL SELECT "N" FROM r) CYCLE "N","N" SET cyc USING path SELECT * FROM r`, "42701", `cycle column "N" specified more than once`, "CYCLE"},
				{"boolean integer marks", base + "CYCLE n SET cyc TO true DEFAULT 0 USING path SELECT * FROM r", "42804", "CYCLE types boolean and integer cannot be matched", "0 USING"},
				{"integer boolean marks", base + "CYCLE n SET cyc TO 0 DEFAULT true USING path SELECT * FROM r", "42804", "CYCLE types integer and boolean cannot be matched", "true USING"},
				{"mark error before invalid body", "WITH RECURSIVE r(n) AS (SELECT missing UNION ALL SELECT n FROM r) CYCLE n SET cyc TO true DEFAULT 0 USING path SELECT * FROM r", "42804", "CYCLE types boolean and integer cannot be matched", "0 USING"},
				{"mark no equality", base + `CYCLE n SET cyc TO json '1' DEFAULT json '0' USING path SELECT * FROM r`, "42883", "could not identify an equality operator for type json", ""},
				{"left nested union", "WITH RECURSIVE r(n) AS ((SELECT 1 UNION ALL SELECT 2) UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT * FROM r", "0A000", "with a SEARCH or CYCLE clause, the left side of the UNION must be a SELECT", ""},
				{"right nested union", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL (SELECT n FROM r UNION ALL SELECT 2)) CYCLE n SET cyc USING path SELECT * FROM r", "42601", "with a SEARCH or CYCLE clause, the right side of the UNION must be a SELECT", ""},
				{"buried self", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n FROM (SELECT n FROM r) s) CYCLE n SET cyc USING path SELECT * FROM r", "0A000", `with a SEARCH or CYCLE clause, the recursive reference to WITH query "r" must be at the top level of its right-hand SELECT`, ""},
				{"ambiguous reference before width", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n,2 FROM r) CYCLE n SET n USING path SELECT * FROM r", "42702", `column reference "n" is ambiguous`, "n,2"},
				{"width before generated conflict", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 1,2 FROM r) CYCLE n SET n USING path SELECT * FROM r", "42601", "each UNION query must have the same number of columns", "1,2"},
				{"type before generated conflict", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT 'x'::text FROM r) CYCLE n SET n USING path SELECT * FROM r", "42804", "UNION types integer and text cannot be matched", "'x'"},
				{"excess aliases cannot consume generated", "WITH RECURSIVE r(n,x) AS (SELECT 1 UNION ALL SELECT n FROM r) CYCLE n SET cyc USING path SELECT * FROM r", "42P10", `WITH query "r" has 1 columns available but 2 columns specified`, "r(n,x)"},
				{"self aliases cannot consume generated", "WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT x FROM r AS t(x,y)) CYCLE n SET cyc USING path SELECT * FROM r", "42P10", `table "t" has 1 columns available but 2 columns specified`, ""},
			} {
				t.Run(tc.name, func(t *testing.T) {
					q := "/*é*/ " + tc.q
					cycleAssertDiagnostic(t, p, backend, c, q, cycleDiagnostic(tc.code, tc.message, q, tc.marker))
				})
			}
			for _, tc := range []struct{ name, value, token string }{
				{"parameter", "$1", "$1"},
				{"function", "abs(1)", "DEFAULT"},
				{"postfix cast", "1::integer", "::"},
				{"unary minus", "-1", "-"},
				{"unary plus", "+1", "+"},
				{"parenthesized expression", "(1+1)", "("},
				{"collate expression", "'Y' COLLATE \"C\"", "COLLATE"},
			} {
				t.Run("grammar "+tc.name, func(t *testing.T) {
					q := "/*é*/ " + base + "CYCLE n SET cyc TO " + tc.value + " DEFAULT 0 USING path SELECT * FROM r"
					marker := tc.token
					// The opening parenthesis of the expression is not the CTE's.
					if marker == "(" {
						marker = "(1+1)"
					}
					want := cycleDiagnostic("42601", `syntax error at or near "`+tc.token+`"`, q, marker)
					cycleAssertDiagnostic(t, p, backend, c, q, want)
				})
			}
			// Static rejection has no physical effects.
			subqueryBoth(subqueryRun(t, p, backend, c), cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='cycle_diagnostic_side_effect'", []string{"relname"}, []string{"NAME"}, nil))
			q := "/*é*/ CREATE TABLE cycle_diagnostic_side_effect AS " + base + "CYCLE n SET cyc TO true DEFAULT 0 USING path SELECT n FROM r"
			cycleAssertDiagnostic(t, p, backend, c, q, cycleDiagnostic("42804", "CYCLE types boolean and integer cannot be matched", q, "0 USING"))
			subqueryBoth(subqueryRun(t, p, backend, c), cteBag("SELECT relname FROM pg_catalog.pg_class WHERE relname='cycle_diagnostic_side_effect'", []string{"relname"}, []string{"NAME"}, nil))
		})
	}
}
