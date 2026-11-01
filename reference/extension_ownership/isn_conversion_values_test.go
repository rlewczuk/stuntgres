package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnConversions(t *testing.T) {
	for _, edge := range isnConversionEdges() {
		source, target := isnConversionRoles[edge[0]], isnConversionRoles[edge[1]]
		t.Run(source.name+"_to_"+target.name, func(t *testing.T) {
			isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				family := edge[0]
				if family == 0 {
					family = edge[1]
				}
				input := isnConversionRoles[family].compact
				output := target.output
				if edge[1] == 0 {
					switch family {
					case 1, 2:
						output = "978-1-234-56789-7"
					case 3, 4:
						output = "979-0-1234-5678-5"
					case 5, 6:
						output = "977-1234-567-00-3"
					case 7:
						output = "012-345678901-2"
					}
				}
				expressions := []string{"$1::" + source.name + "::" + target.name, "CAST(CAST($1 AS " + source.name + ") AS " + target.name + ")"}
				if edge[0] == 0 {
					expressions = append(expressions, target.name+"($1::ean13)")
				}
				for _, expression := range expressions {
					q := "SELECT " + expression + " AS v,(" + expression + ")::ean13 = $1::" + source.name + "::ean13 AS preserved"
					held := fixture.IdentityHeld(t, p.Ctx, c, q)
					defer held.Statement.Close()
					for _, tc := range []struct {
						input        any
						marked, null bool
					}{
						{input, false, false}, {[]byte(input + "!"), true, false}, {nil, false, true}, {[]byte(nil), false, true},
					} {
						var formatted any = output
						var preserved any = true
						if tc.marked && !(b.PG && (target.name == "upc" || target.name == "issn")) {
							formatted = output + "!"
						}
						if tc.null {
							formatted, preserved = nil, nil
						}
						s := fixture.Ean13Select(b, q, []string{"v", "preserved"}, []string{strings.ToUpper(target.name), "BOOL"}, [][]any{{formatted, preserved}}, nil, tc.input)
						run(s)
						run(fixture.Prepared(s))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
					}
				}
				// A typed SQL NULL checks binding/admission, independently of a nil
				// parameter's type input. NULL must not erase the destination role.
				q := "SELECT NULL::" + source.name + "::" + target.name + " AS v"
				isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"v"}, []string{strings.ToUpper(target.name)}, [][]any{{nil}}, nil))
				run(fixture.Exec("CREATE TABLE isn_assignment(v "+target.name+")", 0))
				q = "INSERT INTO isn_assignment SELECT $1::" + source.name
				if edge[0] == 0 {
					want := isnConversionDiagnostic("42804", `column "v" is of type `+target.name+` but expression is of type ean13`)
					want.Hint = "You will need to rewrite or cast the expression."
					want.Position = fixture.Position(strings.Index(q, "$1") + 1)
					for _, prepared := range []bool{false, true} {
						s := isnConversionRuntimeError(q, want, input)
						if prepared {
							s = fixture.Prepared(s)
							s.ErrorStage = "prepare"
						}
						run(s)
					}
				} else {
					held := fixture.IdentityHeld(t, p.Ctx, c, q)
					defer held.Statement.Close()
					for _, arg := range []any{input, []byte(input + "!"), nil} {
						run(fixture.Exec(q, 1, arg))
						run(fixture.Prepared(fixture.Exec(q, 1, arg)))
						fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.Exec(q, 1, arg))
					}
					run(fixture.OwnershipCount("SELECT count(*) FROM isn_assignment", 9))
					run(fixture.OwnershipCount("SELECT count(*) FROM isn_assignment WHERE v IS NULL", 3))
					run(fixture.OwnershipCount("SELECT count(*) FROM isn_assignment WHERE v::ean13 = "+isnConversionLiteral(input+"!")+"::ean13", 3))
					marked := output + "!"
					if b.PG && (target.name == "upc" || target.name == "issn") {
						marked = output
					}
					rows := [][]any{}
					for range 3 {
						rows = append(rows, []any{output}, []any{marked}, []any{nil})
					}
					s := fixture.Ean13Select(b, "SELECT v FROM isn_assignment", []string{"v"}, []string{strings.ToUpper(target.name)}, rows, []bool{true})
					s.Bag = true
					run(s)
				}
			})
		})
	}
	t.Run("prefix_and_issue_boundaries", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			for _, tc := range []struct{ input, target, output string }{
				{"9780123456786", "isbn", "0-12-345678-9"},
				{"9780123456786", "isbn13", "978-0-12-345678-6"},
				{"9791234567896", "isbn", "979-123456789-6"},
				{"9791234567896", "isbn13", "979-123456789-6"},
				{"9790123456785", "ismn", "M-1234-5678-5"},
				{"9790123456785", "ismn13", "979-0-1234-5678-5"},
				{"9771234567003", "issn", "1234-5679"},
				{"9771234567898", "issn", "1234-5679"},
				{"9771234567898", "issn13", "977-1234-567-89-8"},
				{"0123456789012", "upc", "123456789012"},
			} {
				output := tc.output
				if !b.PG && tc.target == "issn" && tc.input == "9771234567898" {
					output = "977-1234-567-89-8"
				}
				q := "SELECT " + tc.target + "($1::ean13) AS v,(($1::ean13)::" + tc.target + ")::ean13 = $1::ean13 AS preserved"
				isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"v", "preserved"}, []string{strings.ToUpper(tc.target), "BOOL"}, [][]any{{output, true}}, nil, tc.input))
			}
			// Renaming a callable checked cast must preserve its actual identity;
			// its old type-name spelling is not a retained routine alias.
			id := fixture.OwnershipID(t, p, c, "SELECT 'isbn(ean13)'::regprocedure::bigint")
			run(fixture.Exec("ALTER FUNCTION isbn(ean13) RENAME TO isn_checked_book", 0))
			if fixture.OwnershipID(t, p, c, "SELECT 'isn_checked_book(ean13)'::regprocedure::bigint") != id {
				t.Fatal("checked conversion rename replaced identity")
			}
			q := "SELECT isn_checked_book($1::ean13) AS v,$1::ean13::isbn AS cast_value"
			isnConversionRoutes(t, p, b, c, fixture.Ean13Select(b, q, []string{"v", "cast_value"}, []string{"ISBN", "ISBN"}, [][]any{{"0-12-345678-9", "0-12-345678-9"}}, nil, "9780123456786"))
		})
	})
}
