package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceIsnComparisons(t *testing.T) {
	for _, pair := range isnConversionPairs() {
		left, right := isnConversionRoles[pair[0]], isnConversionRoles[pair[1]]
		t.Run(left.name+"_"+right.name, func(t *testing.T) {
			isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := isnConversionRun(t, p, b, c)
				run(fixture.Exec("CREATE EXTENSION isn", 0))
				family := pair[0]
				if family == 0 {
					family = pair[1]
				}
				role := isnConversionRoles[family]
				lhs, rhs := "$1::"+left.name, "$2::"+right.name
				prototype := isnConversionComparison(lhs, rhs, 0, false, role.compact, role.compact)
				held := fixture.IdentityHeld(t, p.Ctx, c, prototype.Query)
				defer held.Statement.Close()
				for _, tc := range []struct {
					left, right any
					sign        int
					null        bool
				}{
					{role.compact, role.compact, 0, false},
					{[]byte(role.compact), []byte(role.compact), 0, false},
					{role.low, role.high, -1, false}, {role.high, role.low, 1, false},
					{role.compact, role.compact + "!", -1, false},
					{role.compact + "!", role.compact, 1, false},
					{role.compact[:12] + "0!", role.compact[:12] + "1!", 0, false},
					{role.compact[:12] + "0!", role.compact + "!", 0, false},
					{role.compact[:12] + "?!", role.compact, 0, false},
					{nil, role.compact, 0, true}, {role.compact, nil, 0, true}, {nil, nil, 0, true},
				} {
					s := isnConversionComparison(lhs, rhs, tc.sign, tc.null, tc.left, tc.right)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
				// Literal typed NULLs cannot become an untyped-parameter shortcut.
				for _, operands := range [][2]string{
					{"NULL::" + left.name, isnConversionLiteral(role.compact) + "::" + right.name},
					{isnConversionLiteral(role.compact) + "::" + left.name, "NULL::" + right.name},
					{"NULL::" + left.name, "NULL::" + right.name},
				} {
					s := isnConversionComparison(operands[0], operands[1], 0, true)
					run(s)
					run(fixture.Prepared(s))
				}
				// The same captured pair remains usable after parameter-input failure.
				want := isnConversionDiagnostic("22P02", "invalid input syntax for "+isnConversionInputFamily(left.name)+` number: "bad"`)
				want.Context = "unnamed portal parameter $1 = '...'"
				fixture.RunStep(t, p.Ctx, held, b.Recorder, isnConversionRuntimeError(held.SQL, want, "bad", role.compact))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, prototype)
			})
		})
	}
	t.Run("presentation_and_stored_predicates", func(t *testing.T) {
		isnConversionScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := isnConversionRun(t, p, b, c)
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			for _, operands := range [][2]string{
				{"'1-234-56789-X'::isbn", "'9781234567897'::isbn13"},
				{"'M-1234-5678-5'::ismn", "'9790123456785'::ismn13"},
				{"'1234-5679'::issn", "'9771234567003'::issn13"},
				{"'123456789012'::upc", "'012-345678901-2'::ean13"},
			} {
				isnConversionRoutes(t, p, b, c, isnConversionComparison(operands[0], operands[1], 0, false))
			}
			isnConversionRoutes(t, p, b, c, isnConversionComparison("'9771234567003'::issn", "'9771234567898'::issn", -1, false))
			run(fixture.Exec("CREATE TABLE isn_predicates(id integer NOT NULL,l isbn,r isbn13)", 0))
			run(fixture.Exec("INSERT INTO isn_predicates VALUES(1,'123456789X','9781234567897'),(2,'123456789X!','9781234567897'),(3,NULL,'9781234567897')", 3))
			isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM isn_predicates WHERE l=r ORDER BY id", 1))
			isnConversionRoutes(t, p, b, c, isnConversionIDs("SELECT id FROM isn_predicates WHERE l>r ORDER BY id", 2))
			q := "SELECT id,l=r AS equal,isneq(l,r) AS called FROM isn_predicates ORDER BY id"
			isnConversionRoutes(t, p, b, c, fixture.Select(q, []string{"id", "equal", "called"}, []string{"INT4", "BOOL", "BOOL"}, [][]any{{int64(1), true, true}, {int64(2), false, false}, {int64(3), nil, nil}}))
		})
	})
}

func isnConversionInputFamily(role string) string {
	switch role {
	case "isbn", "isbn13":
		return "ISBN"
	case "ismn", "ismn13":
		return "ISMN"
	case "issn", "issn13":
		return "ISSN"
	case "upc":
		return "UPC"
	default:
		return "EAN13"
	}
}
