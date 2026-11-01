package reference_test

import (
	"database/sql"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

func TestReferenceUpcDefaultCastShape(t *testing.T) {
	fixture.JSONTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE EXTENSION isn", 0))
		// REL_18_6 declares UPC -> EAN13 WITHOUT FUNCTION AS ASSIGNMENT.
		// Neither that relabel nor the surrounding text I/O can turn the
		// parameter into an allowed SQL/JSON DEFAULT function expression.
		for _, tc := range []struct {
			name, value string
			arg         any
		}{
			{"UPC relabel", "($1::upc)::ean13::text", "123456789012"},
			{"UPC CAST relabel", "CAST(($1::upc) AS ean13)::text", "123456789012"},
			{"ISBN relabel chain", "($1::isbn)::isbn13::ean13::text", "123456789X"},
			{"declared IO", "($1::json)::jsonb::text", "{}"},
			{"text relabel", "($1::text)::varchar", "abc"},
			{"text IO", "($1::int)::text", 7},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.name, func(t *testing.T) {
					q := prefix + `SELECT JSON_VALUE('{}', '$.missing' DEFAULT ` + tc.value + ` ON EMPTY)`
					want := fixture.ControlsReferenceDiagnostic("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT")
					token := "$1"
					if strings.HasPrefix(tc.value, "CAST(") {
						token = "CAST("
					}
					want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, token)]) + 1)
					fixture.SQLJSONDiagnostic(t, p, b, c, q, want, true, tc.arg)
				})
			}
		}
		// The checked reverse direction really is a cast function, even when
		// text I/O wraps its result. This independently guards the other side
		// of the classification instead of rejecting every family conversion.
		q := `SELECT JSON_VALUE('{}', '$.missing' DEFAULT ($1::ean13)::upc::text ON EMPTY) AS v`
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"123456789012"}}, "0123456789012"))
		for _, tc := range []struct {
			name, value, input, output string
		}{
			{"checked CAST", "CAST(($1::ean13) AS upc)::text", "0123456789012", "123456789012"},
			{"checked then relabel", "($1::ean13)::upc::ean13::text", "0123456789012", "012-345678901-2"},
			{"checked ISBN then relabel", "($1::ean13)::isbn::isbn13::text", "9780123456786", "978-0-12-345678-6"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				q := `SELECT JSON_VALUE('{}', '$.missing' DEFAULT ` + tc.value + ` ON EMPTY) AS v`
				fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{tc.output}}, tc.input))
			})
		}
		// A selected SQL-defined cast is also a real function expression,
		// regardless of its spelling or the transparent binary wrapper.
		for _, ddl := range []string{
			"CREATE TYPE upc_default_enum AS ENUM ('red')",
			"CREATE FUNCTION upc_default_text(upc_default_enum) RETURNS text LANGUAGE SQL IMMUTABLE STRICT AS $$SELECT 'converted'::text$$",
			"CREATE CAST (upc_default_enum AS text) WITH FUNCTION upc_default_text(upc_default_enum)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(ddl, 0))
		}
		q = `SELECT JSON_VALUE('{}', '$.missing' DEFAULT ($1::upc_default_enum)::text::varchar ON EMPTY) AS v`
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"converted"}}, "red"))
	})
}
