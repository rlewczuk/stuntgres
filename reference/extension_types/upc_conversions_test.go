package reference_test

import (
	"database/sql"
	"strconv"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Shared independent conversion corpus, including invalid-check-digit payloads
// and strict NULLs, for baseline and membership-only survivor witnesses.
var upcConversionValues = []struct {
	expression, typ string
	output          any
}{
	{"'0123456789012'::ean13::upc", "UPC", "123456789012"},
	{"'123456789012'::upc::ean13", "EAN13", "012-345678901-2"},
	{"'220356483481'::upc::ean13", "EAN13", "022-035648348-1"},
	{"'0123456789010!'::ean13::upc", "UPC", "123456789012!"},
	{"'0123456789010!'::ean13::upc::ean13", "EAN13", "012-345678901-2!"},
	{"NULL::ean13::upc", "UPC", nil}, {"NULL::upc::ean13", "EAN13", nil},
}

func TestReferenceUpcConversions(t *testing.T) {
	t.Run("values", func(t *testing.T) {
		upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			for _, tc := range upcConversionValues {
				s := upcSelect(b, "SELECT "+tc.expression+" AS v", []string{"v"}, []string{tc.typ}, [][]any{{tc.output}}, nil)
				run(s)
				run(fixture.Prepared(s))
				held := ean13Held(t, p, c, s.Query)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
			for _, q := range []string{"SELECT $1::ean13::upc AS v", "SELECT CAST(CAST($1 AS ean13) AS upc) AS v"} {
				held := ean13Held(t, p, c, q)
				for _, value := range []any{"0123456789012", []byte("0123456789010!"), nil, []byte(nil)} {
					var output any = "123456789012"
					if _, ok := value.([]byte); ok {
						output = "123456789012!"
					}
					if value == nil {
						output = nil
					}
					if bytes, ok := value.([]byte); ok && bytes == nil {
						output = nil
					}
					s := upcSelect(b, q, []string{"v"}, []string{"UPC"}, [][]any{{output}}, nil, value)
					run(s)
					run(fixture.Prepared(s))
					fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
				}
			}
			setup := func() {
				run(fixture.Exec("CREATE TABLE upc_source(id integer NOT NULL,v upc)", 0))
				run(fixture.Exec("CREATE TABLE upc_ean(id integer NOT NULL,v ean13)", 0))
				run(fixture.Exec("INSERT INTO upc_source VALUES(1,'123456789010!'),(2,NULL),(3,'220356483481')", 3))
				run(fixture.Exec("INSERT INTO upc_ean SELECT id,v FROM upc_source", 3))
			}
			setup()
			run(fixture.Ean13Select(b, "SELECT id,v FROM upc_ean ORDER BY id", []string{"id", "v"}, []string{"INT4", "EAN13"}, [][]any{{int64(1), "012-345678901-2!"}, {int64(2), nil}, {int64(3), "022-035648348-1"}}, []bool{false, true}))
			q := "INSERT INTO upc_source SELECT 4,v FROM upc_ean WHERE id=1"
			want := ean13Diagnostic("42804", `column "v" is of type upc but expression is of type ean13`)
			want.Position = strconv.Itoa(strings.Index(q, ",v") + 2)
			want.Hint = "You will need to rewrite or cast the expression."
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic(q, want)
				s.ErrorStage = "execute"
				if prepared {
					s = fixture.Prepared(s)
					s.ErrorStage = "prepare"
				}
				run(s)
			}
			run(fixture.Exec("INSERT INTO upc_source SELECT 4,v::upc FROM upc_ean WHERE id=1", 1))
			run(fixture.Exec("INSERT INTO upc_source SELECT 5,v::upc FROM upc_ean WHERE id=2", 1))
			run(upcSelect(b, "SELECT id,v FROM upc_source WHERE id>3 ORDER BY id", []string{"id", "v"}, []string{"INT4", "UPC"}, [][]any{{int64(4), "123456789012!"}, {int64(5), nil}}, []bool{false, true}))
			// A stable TEXT result exercises both captured conversion descriptors
			// across ordinary catalog generations and extension OID replacement.
			held := ean13Held(t, p, c, "SELECT v::upc::ean13::text AS v FROM upc_ean ORDER BY id")
			s := fixture.Select(held.SQL, []string{"v"}, []string{"TEXT"}, [][]any{{"012-345678901-2!"}, {nil}, {"022-035648348-1"}})
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("CREATE TABLE upc_unrelated(v integer)", 0))
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			run(fixture.Exec("DROP TABLE upc_source,upc_ean", 0))
			run(fixture.Exec("DROP EXTENSION isn", 0))
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			setup()
			fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
		})
	})
	t.Run("typed_failures", func(t *testing.T) {
		upcScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			run(fixture.Exec("CREATE EXTENSION isn", 0))
			run(fixture.Exec("CREATE TABLE upc_conversion_atomic(id integer,v upc)", 0))
			run(fixture.Exec("INSERT INTO upc_conversion_atomic VALUES(1,'123456789012')", 1))
			for _, tc := range []struct{ input, family, number string }{{"9780123456786", "EAN13(ISBN)", "9780123456786"}, {"9790123456785", "EAN13(ISMN)", "9790123456785"}, {"9771234567898", "EAN13(ISSN)", "9771234567898"}, {"1234567890128", "EAN13", "1234567890128"}, {"9780123456780!", "EAN13(ISBN)", "9780123456786"}, {"1234567890120!", "EAN13", "1234567890128"}} {
				message := "cannot cast " + tc.family + " to UPC for number: \"" + tc.number + "\""
				for _, q := range []string{"SELECT " + ean13Literal(tc.input) + "::ean13::upc", "INSERT INTO upc_conversion_atomic VALUES(2,'123456789012'),(3," + ean13Literal(tc.input) + "::ean13::upc)"} {
					want := ean13Diagnostic("22P02", message)
					// A runtime typed cast, unlike literal input, has no cursor position.
					for _, prepared := range []bool{false, true} {
						s := fixture.ErrorDiagnostic(q, want)
						s.ErrorStage = "execute"
						if prepared {
							s = fixture.Prepared(s)
						}
						run(s)
					}
				}
				held := ean13Held(t, p, c, "SELECT $1::ean13::upc AS v")
				want := ean13Diagnostic("22P02", message)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, fixture.ErrorDiagnostic(held.SQL, want, tc.input))
				fixture.RunStep(t, p.Ctx, held, b.Recorder, upcSelect(b, held.SQL, []string{"v"}, []string{"UPC"}, [][]any{{"123456789012!"}}, nil, "0123456789010!"))
				run(upcSelect(b, "SELECT id,v FROM upc_conversion_atomic ORDER BY id", []string{"id", "v"}, []string{"INT4", "UPC"}, [][]any{{int64(1), "123456789012"}}, []bool{true, true}))
			}
			// Input diagnostics remain tied to characters, not UTF-8 byte offsets.
			q := "/*é*/ SELECT 'bad'::upc"
			want := ean13Diagnostic("22P02", `invalid input syntax for UPC number: "bad"`)
			want.Position = strconv.Itoa(utf8.RuneCountInString(q[:strings.Index(q, "'bad'")]) + 1)
			run(fixture.ErrorDiagnostic(q, want))
		})
	})
}
