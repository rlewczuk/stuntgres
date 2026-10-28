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
			{"text relabel", "($1::text)::varchar", "abc"},
			{"text IO", "($1::int)::text", 7},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.name, func(t *testing.T) {
					q := prefix + `SELECT JSON_VALUE('{}', '$.missing' DEFAULT ` + tc.value + ` ON EMPTY)`
					want := fixture.ControlsReferenceDiagnostic("42804", "can only specify a constant, non-aggregate function, or operator expression for DEFAULT")
					want.Position = fixture.Position(utf8.RuneCountInString(q[:strings.Index(q, "$1")]) + 1)
					fixture.SQLJSONDiagnostic(t, p, b, c, q, want, true, tc.arg)
				})
			}
		}
		// The checked reverse direction really is a cast function, even when
		// text I/O wraps its result. This independently guards the other side
		// of the classification instead of rejecting every family conversion.
		q := `SELECT JSON_VALUE('{}', '$.missing' DEFAULT ($1::ean13)::upc::text ON EMPTY) AS v`
		fixture.SQLJSONBoth(t, p, b, c, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{"123456789012"}}, "0123456789012"))
	})
}
