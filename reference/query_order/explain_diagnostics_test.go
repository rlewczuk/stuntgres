package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExplainDiagnostics(t *testing.T) {
	// Option errors generally happen in ExplainQuery (execute), whereas the
	// wrapped child's parse analysis and FORMAT's missing parameter can fail
	// during Prepare/Describe. These source-derived stages are oracle assertions,
	// not observations learned from the native implementation.
	cases := []struct {
		name, query, code, message, token, preparedStage, hint, detail string
	}{
		{"missing-child", "EXPLAIN", "42601", "syntax error at end of input", "<end>", "prepare", "", ""},
		{"missing-filter", "EXPLAIN SELECT 1 WHERE", "42601", "syntax error at end of input", "<end>", "prepare", "", ""},
		{"trailing-junk", "EXPLAIN SELECT 1 WHERE TRUE junk", "42601", `syntax error at or near "junk"`, "junk", "prepare", "", ""},
		{"empty-options", "EXPLAIN () SELECT 1", "42601", `syntax error at or near ")"`, ")", "prepare", "", ""},
		{"trailing-comma", "EXPLAIN (COSTS false,) SELECT 1", "42601", `syntax error at or near ")"`, ")", "prepare", "", ""},
		{"missing-value", "EXPLAIN (FORMAT = TEXT) SELECT 1", "42601", `syntax error at or near "="`, "=", "prepare", "", ""},
		{"illegal-child", "EXPLAIN CREATE TABLE explain_bad(id integer)", "42601", `syntax error at or near "integer"`, "integer", "prepare", "", ""},
		{"nested", "EXPLAIN EXPLAIN SELECT 1", "42601", `syntax error at or near "EXPLAIN"`, "EXPLAIN SELECT", "prepare", "", ""},
		{"sql-prepare", "PREPARE explain_bad AS EXPLAIN SELECT 1", "42601", `syntax error at or near "EXPLAIN"`, "EXPLAIN", "prepare", "", ""},
		{"unknown", "EXPLAIN (bogus) SELECT 1", "42601", `unrecognized EXPLAIN option "bogus"`, "bogus", "execute", "", ""},
		{"unknown-quoted", "EXPLAIN (\"Odd\") SELECT 1", "42601", `unrecognized EXPLAIN option "Odd"`, "\"Odd\"", "execute", "", ""},
		{"unknown-quoted-known", "EXPLAIN (\"COSTS\" false) SELECT 1", "42601", `unrecognized EXPLAIN option "COSTS"`, "\"COSTS\"", "execute", "", ""},
		{"format-invalid", "EXPLAIN (FORMAT bad) SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "bad"`, "FORMAT", "execute", "", ""},
		{"format-absent", "EXPLAIN (FORMAT) SELECT 1", "42601", "format requires a parameter", "", "prepare", "", ""},
		{"serialize-invalid", "EXPLAIN (SERIALIZE bad) SELECT 1", "22023", `unrecognized value for EXPLAIN option "serialize": "bad"`, "SERIALIZE", "execute", "", ""},
		{"wal-dependency", "EXPLAIN (WAL true) SELECT 1", "22023", "EXPLAIN option WAL requires ANALYZE", "", "execute", "", ""},
		{"timing-dependency", "EXPLAIN (TIMING true) SELECT 1", "22023", "EXPLAIN option TIMING requires ANALYZE", "", "execute", "", ""},
		{"serialize-text", "EXPLAIN (SERIALIZE TEXT) SELECT 1", "22023", "EXPLAIN option SERIALIZE requires ANALYZE", "", "execute", "", ""},
		{"serialize-binary", "EXPLAIN (SERIALIZE BINARY) SELECT 1", "22023", "EXPLAIN option SERIALIZE requires ANALYZE", "", "execute", "", ""},
		{"serialize-default", "EXPLAIN (SERIALIZE) SELECT 1", "22023", "EXPLAIN option SERIALIZE requires ANALYZE", "", "execute", "", ""},
		{"analyze-generic", "EXPLAIN (ANALYZE, GENERIC_PLAN) SELECT 1", "22023", "EXPLAIN options ANALYZE and GENERIC_PLAN cannot be used together", "", "execute", "", ""},
		{"earlier-invalid-format", "EXPLAIN (FORMAT bad, FORMAT TEXT) SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "bad"`, "FORMAT", "execute", "", ""},
		{"earlier-invalid-boolean", "EXPLAIN (COSTS 'bad', COSTS false) SELECT 1", "42601", "costs requires a Boolean value", "", "execute", "", ""},
		{"earlier-invalid-analyze", "EXPLAIN (ANALYZE 'bad', ANALYZE FALSE) SELECT 1", "42601", "analyze requires a Boolean value", "", "execute", "", ""},
		{"final-false-timing", "EXPLAIN (ANALYZE TRUE, ANALYZE FALSE, TIMING TRUE) SELECT 1", "22023", "EXPLAIN option TIMING requires ANALYZE", "", "execute", "", ""},
		{"final-false-wal", "EXPLAIN (ANALYZE TRUE, ANALYZE FALSE, WAL TRUE) SELECT 1", "22023", "EXPLAIN option WAL requires ANALYZE", "", "execute", "", ""},
		{"final-false-serialize", "EXPLAIN (ANALYZE TRUE, ANALYZE FALSE, SERIALIZE TEXT) SELECT 1", "22023", "EXPLAIN option SERIALIZE requires ANALYZE", "", "execute", "", ""},
		{"option-order", "EXPLAIN (bogus, COSTS 'bad') SELECT 1", "42601", `unrecognized EXPLAIN option "bogus"`, "bogus", "execute", "", ""},
		{"missing-relation", "EXPLAIN SELECT * FROM explain_absent", "42P01", `relation "explain_absent" does not exist`, "explain_absent", "prepare", "", ""},
		{"missing-column", "EXPLAIN SELECT absent FROM explain_diag", "42703", `column "absent" does not exist`, "absent", "prepare", "", ""},
		{"where-type", "EXPLAIN SELECT id FROM explain_diag WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "1", "prepare", "", ""},
		{"operator-type", "EXPLAIN SELECT id + true FROM explain_diag", "42883", "operator does not exist: integer + boolean", "+", "prepare", "No operator matches the given name and argument types. You might need to add explicit type casts.", ""},
		{"inconsistent-parameter", "EXPLAIN SELECT $1, $1::integer", "42P08", "inconsistent types deduced for parameter $1", "$1", "prepare", "", "integer versus text"},
		{"child-before-option", "EXPLAIN (bogus) SELECT absent FROM explain_diag", "42703", `column "absent" does not exist`, "absent", "prepare", "", ""},
		{"child-before-dependency", "EXPLAIN (WAL true) SELECT * FROM explain_absent", "42P01", `relation "explain_absent" does not exist`, "explain_absent", "prepare", "", ""},
		{"syntax-before-child", "EXPLAIN (bogus) SELECT * FROM explain_absent WHERE", "42601", "syntax error at end of input", "<end>", "prepare", "", ""},
		{"json-format-quoted", "EXPLAIN (FORMAT 'JSON') SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "JSON"`, "FORMAT", "execute", "", ""},
		{"json-earlier-format", "EXPLAIN (FORMAT 'JSON', FORMAT JSON) SELECT 1", "22023", `unrecognized value for EXPLAIN option "format": "JSON"`, "FORMAT", "execute", "", ""},
		{"json-absent-format", "EXPLAIN (FORMAT, FORMAT JSON) SELECT 1", "42601", "format requires a parameter", "", "prepare", "", ""},
		{"json-costs-string-one", "EXPLAIN (FORMAT JSON, COSTS '1') SELECT 1", "42601", "costs requires a Boolean value", "", "execute", "", ""},
		{"json-costs-string-zero", "EXPLAIN (FORMAT JSON, COSTS '0') SELECT 1", "42601", "costs requires a Boolean value", "", "execute", "", ""},
		{"json-costs-decimal", "EXPLAIN (FORMAT JSON, COSTS 1.0) SELECT 1", "42601", "costs requires a Boolean value", "", "execute", "", ""},
		{"json-costs-integer", "EXPLAIN (FORMAT JSON, COSTS 2) SELECT 1", "42601", "costs requires a Boolean value", "", "execute", "", ""},
		{"json-boolean-order", "EXPLAIN (FORMAT JSON, COSTS '1', COSTS false) SELECT 1", "42601", "costs requires a Boolean value", "", "execute", "", ""},
		{"json-wal-dependency", "EXPLAIN (FORMAT JSON, WAL true) SELECT 1", "22023", "EXPLAIN option WAL requires ANALYZE", "", "execute", "", ""},
		{"json-dependency-order", "EXPLAIN (FORMAT JSON, TIMING true, WAL true) SELECT 1", "22023", "EXPLAIN option WAL requires ANALYZE", "", "execute", "", ""},
		{"json-final-dependency", "EXPLAIN (FORMAT JSON, ANALYZE true, ANALYZE false, WAL true) SELECT 1", "22023", "EXPLAIN option WAL requires ANALYZE", "", "execute", "", ""},
		{"json-child-option", "EXPLAIN (FORMAT JSON, bogus) SELECT absent FROM explain_diag", "42703", `column "absent" does not exist`, "absent", "prepare", "", ""},
		{"json-child-format", "EXPLAIN (FORMAT 'JSON') SELECT * FROM explain_absent", "42P01", `relation "explain_absent" does not exist`, "explain_absent", "prepare", "", ""},
		{"json-child-type", "EXPLAIN (FORMAT JSON, WAL) SELECT id FROM explain_diag WHERE 1", "42804", "argument of WHERE must be type boolean, not type integer", "1", "prepare", "", ""},
		{"json-parse-first", "EXPLAIN (FORMAT JSON, bogus) SELECT * FROM explain_absent WHERE", "42601", "syntax error at end of input", "<end>", "prepare", "", ""},
	}
	for _, option := range []string{"ANALYZE", "VERBOSE", "COSTS", "SETTINGS", "GENERIC_PLAN", "BUFFERS", "WAL", "TIMING", "SUMMARY", "MEMORY"} {
		cases = append(cases, struct {
			name, query, code, message, token, preparedStage, hint, detail string
		}{"boolean-" + option, "EXPLAIN (" + option + " 'bad') SELECT 1", "42601", strings.ToLower(option) + " requires a Boolean value", "", "execute", "", ""})
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := fixture.NewScenario(t)
			for _, b := range p.Backends() {
				t.Run(b.Name, func(t *testing.T) {
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE explain_diag(id integer)", 0))
					for _, prefix := range []string{"", "/*é*/ "} {
						for _, prepared := range []bool{false, true} {
							t.Run(fmt.Sprintf("prefix=%q/prepared=%t", prefix, prepared), func(t *testing.T) {
								query := prefix + tc.query
								position := 0
								if tc.token == "<end>" {
									position = len([]rune(query)) + 1
								} else if tc.token != "" {
									position = len([]rune(prefix+tc.query[:strings.Index(tc.query, tc.token)])) + 1
								}
								s := fixture.ImplicitError(query, fixture.Diagnostic{Code: tc.code, Message: tc.message, Position: fixture.Position(position), Hint: tc.hint, Detail: tc.detail})
								if tc.name == "inconsistent-parameter" {
									s.Args = []any{int64(1)}
								}
								s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
								if prepared {
									s.ErrorStage = tc.preparedStage
								}
								fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
							})
						}
					}
					fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Select("SELECT id FROM explain_diag ORDER BY id", []string{"id"}, []string{"INT4"}, nil))
				})
			}
		})
	}
}
