package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSQLJSONFromClauseBoundaries(t *testing.T) {
	sqljsonScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, prefix := range []string{"SELECT * FROM ", "/*é😀*/ SELECT * FROM "} {
			for _, head := range []string{
				`JSON_OBJECT`,
				`pg_catalog.json_object`,
				`"json_object"`,
				`pg_catalog."json_object"`,
			} {
				for _, args := range []string{
					`ARRAY['a','1'] FORMAT JSON`,
					`ARRAY['a'] FORMAT JSON, ARRAY['1']`,
					`ARRAY['a'], ARRAY['1'] FORMAT JSON`,
				} {
					call := head + "(" + args + ")"
					for _, source := range []string{call, "ROWS FROM (" + call + ")"} {
						q := prefix + source + ";"
						t.Run(q, func(t *testing.T) {
							// All these are ordinary legacy calls: FORMAT must fail
							// in syntax analysis, before scalar-FROM provider binding.
							d := fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "FORMAT"`)
							d.Position = fixture.Position(len([]rune(q[:strings.Index(q, "FORMAT")])) + 1)
							sqljsonDiagnostic(t, p, b, c, q, d, true)
						})
					}
				}
			}
		}
	})
}
