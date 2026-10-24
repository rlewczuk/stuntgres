package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
	"unicode/utf8"
)

// Row expressions reach analysis without admitting the INSERT destination as input.
var querySetOpsRestrictedCells = []struct {
	cell, token, code, message string
}{
	{"abs(1)", ")", "42601", `syntax error at or near ")"`},
	{"TRUE AND FALSE", "AND", "42601", `syntax error at or near "AND"`},
}

var querySetOpsValuesShapes = []string{
	"VALUES(%s)", "VALUES(%s) LIMIT 1", "VALUES(%s) ORDER BY 1",
	"(VALUES(%s) LIMIT 1)", "((VALUES(%s) ORDER BY 1 LIMIT 1))",
}

func TestReferenceQuerySetOpsParserBoundaries(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE boundary_dst(v INT DEFAULT 7)", 0))
		for _, cell := range []string{"v+1", "CAST(v AS INT)", "COALESCE(NULL,v)", "CASE WHEN TRUE THEN 1 ELSE v END", "length(v)", "boundary_dst.v"} {
			for _, shape := range querySetOpsValuesShapes {
				q := "/*é*/ INSERT INTO boundary_dst " + fmt.Sprintf(shape, cell)
				code, message, token := "42703", `column "v" does not exist`, "v"
				detail := `There is a column named "v" in table "boundary_dst", but it cannot be referenced from this part of the query.`
				if cell == "boundary_dst.v" {
					code, message, token = "42P01", `invalid reference to FROM-clause entry for table "boundary_dst"`, cell
					detail = `There is an entry for table "boundary_dst", but it cannot be referenced from this part of the query.`
				}
				offset := strings.Index(q, cell) + strings.Index(cell, token)
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail, Position: fixture.Position(utf8.RuneCountInString(q[:offset]) + 1)})
				querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
			}
		}
		for _, cell := range querySetOpsRestrictedCells {
			for _, shape := range querySetOpsValuesShapes {
				q := "/*é*/ INSERT INTO boundary_dst " + fmt.Sprintf(shape, cell.cell)
				t.Run(q, func(t *testing.T) {
					if b.PG {
						if cell.cell == "TRUE AND FALSE" {
							position := ""
							if shape == "VALUES(%s)" {
								position = "39"
							}
							s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42804", Message: `column "v" is of type integer but expression is of type boolean`, Hint: "You will need to rewrite or cast the expression.", Position: position})
							querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
						} else {
							querySetOpsRoutes(t, p, b, fixture.Exec(q, 1))
						}
						return
					}
					offset := strings.Index(q, cell.cell) + strings.Index(cell.cell, cell.token)
					if cell.cell == "abs(1)" {
						// The existing restricted path reports the outer row close,
						// not the call opener (root artifact83). Unit witnesses pin
						// that same diagnostic for bare and decorated VALUES.
						offset = strings.Index(q, cell.cell) + len(cell.cell)
					}
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: cell.code, Message: cell.message, Position: fixture.Position(utf8.RuneCountInString(q[:offset]) + 1)})
					querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
				})
			}
		}
		// Literal arithmetic was already scalar grammar; retain its admission.
		// The restricted arithmetic witness above contains a column reference.
		for _, shape := range querySetOpsValuesShapes {
			querySetOpsRoutes(t, p, b, fixture.Exec("/*é*/ INSERT INTO boundary_dst "+fmt.Sprintf(shape, "1+2"), 1))
		}
		// Genuine sets keep query-cell expressions, including parenthesized operands.
		for _, q := range []string{
			"INSERT INTO boundary_dst VALUES(1+2) UNION ALL SELECT 4",
			"INSERT INTO boundary_dst (VALUES(abs(1)) LIMIT 1) UNION ALL SELECT 2",
			"INSERT INTO boundary_dst ((VALUES(CASE WHEN TRUE THEN 1 ELSE 2 END)) UNION ALL SELECT 3)",
			"INSERT INTO boundary_dst (VALUES(1),(2) ORDER BY 1 DESC LIMIT 1)",
			"INSERT INTO boundary_dst VALUES(DEFAULT) LIMIT 1",
			"INSERT INTO boundary_dst ((VALUES(DEFAULT) ORDER BY 1 LIMIT 1))",
		} {
			if strings.Contains(q, "DEFAULT") {
				offset := strings.Index(q, "DEFAULT")
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42601", Message: "DEFAULT is not allowed in this context", Position: fixture.Position(utf8.RuneCountInString(q[:offset]) + 1)})
				querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
				continue
			}
			count := int64(1)
			if strings.Contains(q, "UNION ALL") {
				count = 2
			}
			querySetOpsRoutes(t, p, b, fixture.Exec(q, count))
		}
		// Every successful source ran through four public routes. Independently
		// assert the published bag, not just affected counts or backend parity.
		var written [][]any
		for range 20 {
			written = append(written, []any{int64(3)})
			if b.PG {
				written = append(written, []any{int64(1)})
			}
		}
		for range 4 {
			for _, value := range []int64{3, 4, 1, 2, 1, 3, 2} {
				written = append(written, []any{value})
			}
		}
		querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT v FROM boundary_dst", "v", "INT4", written))
	})
}

func TestReferenceQuerySetOpsValuesLockBoundaries(t *testing.T) {
	querySetOpsScenario(t, func(p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE boundary_dst(v INT)", 0))
		for _, mode := range []string{"UPDATE", "NO KEY UPDATE", "SHARE", "KEY SHARE"} {
			for _, source := range []string{"VALUES(1) FOR %s", "(VALUES(1) FOR %s)", "((VALUES(1) FOR %s))", "(VALUES(1) ORDER BY 1 LIMIT 1 FOR %s)"} {
				values := fmt.Sprintf(source, mode)
				for _, q := range []string{"INSERT INTO boundary_dst " + values, "SELECT * FROM (" + values + ") AS d"} {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "0A000", Message: "FOR " + mode + " cannot be applied to VALUES"})
					querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
					querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT v FROM boundary_dst", "v", "INT4", nil))
				}
			}
		}
		// Competing errors are independently specified, not inferred from parity.
		for _, tc := range []struct {
			query string
			want  fixture.Diagnostic
		}{
			{"INSERT INTO boundary_missing VALUES(1) FOR UPDATE", fixture.Diagnostic{Code: "42P01", Message: `relation "boundary_missing" does not exist`, Position: "13"}},
			{"INSERT INTO boundary_dst VALUES(missing) FOR SHARE", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "33"}},
			{"INSERT INTO boundary_dst VALUES(1,2) FOR KEY SHARE", fixture.Diagnostic{Code: "0A000", Message: "FOR KEY SHARE cannot be applied to VALUES"}},
			{"SELECT missing FROM (VALUES(1) FOR UPDATE) AS d", fixture.Diagnostic{Code: "0A000", Message: "FOR UPDATE cannot be applied to VALUES"}},
			{"SELECT * FROM (VALUES(missing) FOR NO KEY UPDATE) AS d", fixture.Diagnostic{Code: "42703", Message: `column "missing" does not exist`, Position: "23"}},
			{"INSERT INTO boundary_dst VALUES(1) FOR UPDATE; SELECT FROM", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "59"}},
			{"INSERT INTO boundary_dst VALUES(1); SELECT * FROM (VALUES(1) FOR SHARE) AS d; SELECT FROM", fixture.Diagnostic{Code: "42601", Message: "syntax error at end of input", Position: "90"}},
		} {
			want := tc.want
			s := fixture.ImplicitError(tc.query, want)
			querySetOpsDiagnosticRoutes(t, p, b, s, "prepare")
			querySetOpsRoutes(t, p, b, querySetOpsBag("SELECT v FROM boundary_dst", "v", "INT4", nil))
		}
	})
}
