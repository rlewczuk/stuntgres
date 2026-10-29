package reference_test

import (
	"fmt"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceFTSDebugDiagnostics(t *testing.T) {
	for i, tc := range []struct {
		query, code, message string
		position             int
		eager                bool
		context              string
		args                 []any
	}{
		{"SELECT * FROM ts_debug(0::regconfig,'word')", "", "", 0, false, "", nil},
		{"SELECT * FROM ts_debug(0::regconfig,NULL::text)", "", "", 0, false, "", nil},
		{"SELECT * FROM ts_debug('debug_absent','word')", "42704", `text search configuration "debug_absent" does not exist`, 24, true, "", nil},
		{"SELECT * FROM ts_debug($1::regconfig,'word')", "42704", `text search configuration "debug_absent" does not exist`, 0, false, "unnamed portal parameter $1 = '...'", []any{"debug_absent"}},
		{"SELECT * FROM ts_debug($1::text::regconfig,'word')", "42704", `text search configuration "debug_absent" does not exist`, 0, false, "", []any{"debug_absent"}},
		{"SELECT * FROM ts_parse('debug_absent','word')", "42704", `text search parser "debug_absent" does not exist`, 0, false, "", nil},
		{"SELECT * FROM ts_parse(0::oid,'word')", "XX000", "cache lookup failed for text search parser 0", 0, false, "", nil},
		{"SELECT ts_lexize('debug_absent','word')", "42704", `text search dictionary "debug_absent" does not exist`, 18, true, "", nil},
		{"SELECT ts_lexize(0::regdictionary,'word')", "XX000", "cache lookup failed for text search dictionary 0", 0, false, "", nil},
		{"SELECT * FROM ts_debug()", "42883", "function ts_debug() does not exist", 15, true, "", nil},
		{"SELECT * FROM ts_debug(true)", "42883", "function ts_debug(boolean) does not exist", 15, true, "", nil},
		{"SELECT * FROM ts_stat()", "42883", "function ts_stat() does not exist", 15, true, "", nil},
		{"SELECT * FROM ts_stat(1)", "42883", "function ts_stat(integer) does not exist", 15, true, "", nil},
		{"SELECT * FROM ts_debug(document => 'word',bad => 'simple')", "42883", "function ts_debug(document => unknown, bad => unknown) does not exist", 15, true, "", nil},
		{"SELECT * FROM ts_debug(NULL::text,missing)", "42703", `column "missing" does not exist`, 35, true, "", nil},
		{"SELECT * FROM ts_stat(NULL::text,missing)", "42703", `column "missing" does not exist`, 34, true, "", nil},
		{"SELECT * FROM ts_debug('word') AS d(a text)", "42601", "a column definition list is redundant for a function with OUT parameters", 35, true, "", nil},
		{"SELECT * FROM ts_stat('SELECT NULL::tsvector') AS s(a text)", "42601", "a column definition list is redundant for a function with OUT parameters", 50, true, "", nil},
		{"SELECT ts_debug(DISTINCT 'word')", "42809", "DISTINCT specified, but ts_debug is not an aggregate function", 8, true, "", nil},
		{"SELECT ts_stat(DISTINCT 'SELECT NULL::tsvector')", "42809", "DISTINCT specified, but ts_stat is not an aggregate function", 8, true, "", nil},
		{"SELECT 1 WHERE ts_debug('word') IS NULL", "0A000", "set-returning functions are not allowed in WHERE", 16, true, "", nil},
		{"SELECT 1 WHERE ts_stat('SELECT NULL::tsvector') IS NULL", "0A000", "set-returning functions are not allowed in WHERE", 16, true, "", nil},
	} {
		t.Run(fmt.Sprint(i), func(t *testing.T) {
			ftsParserScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
				c := ftsInspectionConnection(t, p, b)
				if tc.code == "" {
					ftsInspectionBoth(t, p, b, c, fixture.Select(tc.query, ftsDebugColumns, ftsDebugTypes, nil))
					return
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Position = fixture.Position(tc.position)
				if strings.HasPrefix(tc.message, "a column definition list") {
					d.Position = fixture.Position(strings.Index(tc.query, "a text") + 1)
				}
				d.Context = tc.context
				if tc.code == "42883" {
					d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
				}
				ftsInspectionDiagnostic(t, p, b, c, tc.query, d, tc.eager, tc.args...)
			})
		})
	}
}
