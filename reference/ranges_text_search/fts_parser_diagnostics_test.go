package reference_test

import (
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"stuntgres/reference/fixture"
)

// Candidates are release-source expectations, not claimed oracle observations.
// ErrorDiagnostic asserts every optional field, including fields left empty.
func TestReferenceFTSParserDiagnostics(t *testing.T) {
	cases := []struct {
		name, query, code, message, positionAt string
		eager bool
	}{
		{"missing", "SELECT * FROM ts_parse('missing','Word')", "42704", `text search parser "missing" does not exist`, "", false},
		{"missing_empty", "SELECT * FROM ts_parse('missing','')", "42704", `text search parser "missing" does not exist`, "", false},
		{"missing_descriptors", "SELECT * FROM ts_token_type('missing')", "42704", `text search parser "missing" does not exist`, "", false},
		{"text_oid", "SELECT * FROM ts_parse('3722'::text,'Word')", "42704", `text search parser "3722" does not exist`, "", false},
		{"quoted_case", `SELECT * FROM ts_parse('"Default"','Word')`, "42704", `text search parser "Default" does not exist`, "", false},
		{"quoted_dot", `SELECT * FROM ts_parse('"pg_catalog.default"','Word')`, "42704", `text search parser "pg_catalog.default" does not exist`, "", false},
		{"empty_name", "SELECT * FROM ts_parse('','Word')", "42602", "invalid name syntax", "", false},
		{"bad_quote", `SELECT * FROM ts_parse('"default','Word')`, "42602", "invalid name syntax", "", false},
		{"trailing_dot", "SELECT * FROM ts_token_type('pg_catalog.')", "42602", "invalid name syntax", "", false},
		{"trailing_identifier", "SELECT * FROM ts_parse('default garbage','Word')", "42602", "invalid name syntax", "", false},
		{"too_many_parts", "SELECT * FROM ts_parse('a.b.c.d','Word')", "42601", "improper qualified name (too many dotted names): a.b.c.d", "", false},
		{"cross_database", "SELECT * FROM ts_parse('otherdb.pg_catalog.default','Word')", "0A000", "cross-database references are not implemented: otherdb.pg_catalog.default", "", false},
		{"missing_schema", "SELECT * FROM ts_parse('fts_parser_absent.default','Word')", "3F000", `schema "fts_parser_absent" does not exist`, "", false},
		{"zero_oid", "SELECT * FROM ts_parse(0::oid,'Word')", "XX000", "cache lookup failed for text search parser 0", "", false},
		{"unknown_oid", "SELECT * FROM ts_token_type(4294967295::oid)", "XX000", "cache lookup failed for text search parser 4294967295", "", false},
		{"bool_signature", "SELECT * FROM ts_parse(true,'Word'::text)", "42883", "function ts_parse(boolean, text) does not exist", "ts_parse", true},
		{"arity", "SELECT * FROM ts_parse('default'::text)", "42883", "function ts_parse(text) does not exist", "ts_parse", true},
		{"descriptor_arity", "SELECT * FROM ts_token_type()", "42883", "function ts_token_type() does not exist", "ts_token_type", true},
		{"wrong_label", "SELECT * FROM ts_parse(parser_name => 'default'::text, document => 'Word'::text)", "42883", "function ts_parse(parser_name => text, document => text) does not exist", "ts_parse", true},
		{"duplicate_label", "SELECT * FROM ts_parse(parser_name => 'default', parser_name => 'Word')", "42601", `argument name "parser_name" used more than once`, "parser_name => 'Word'", true},
		{"fixed_out", "SELECT * FROM ts_parse('default','Word') AS p(id integer,token text)", "42601", "a column definition list is redundant for a function with OUT parameters", "id integer", true},
		{"signature_before_coldef", "SELECT * FROM ts_parse(true,'Word'::text) AS p(id integer,token text)", "42883", "function ts_parse(boolean, text) does not exist", "ts_parse", true},
		{"distinct", "SELECT ts_parse(DISTINCT 'default','Word')", "42809", "DISTINCT specified, but ts_parse is not an aggregate function", "ts_parse", true},
		{"nested", "SELECT * FROM ts_parse('default',ts_parse('default','Word'))", "42883", "function ts_parse(unknown, record) does not exist", "ts_parse", true},
		{"suffix", "SELECT * FROM ts_parse('default','Word'); garbage", "42601", `syntax error at or near "garbage"`, "garbage", true},
	}
	for _, tc := range cases {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			for _, prepared := range []bool{false,true} {
				for _, selectRows := range []bool{false,true} {
					t.Run(fmt.Sprintf("%s/prefix_%t/prepared_%t/query_%t",tc.name,prefix!="",prepared,selectRows),func(t *testing.T) {
						ftsParserScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend) {
							query := prefix+tc.query
							d := fixture.ControlsReferenceDiagnostic(tc.code,tc.message)
							if tc.positionAt != "" { d.Position = fixture.Position(utf8.RuneCountInString(query[:strings.Index(query,tc.positionAt)])+1) }
							if tc.code == "42883" { d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts." }
							s := fixture.ErrorDiagnostic(query,d)
							s.Prepared, s.SelectRows, s.ErrorStage = prepared,selectRows,"execute"
							if prepared && tc.eager { s.ErrorStage = "prepare" }
							fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)
						})
					})
				}
			}
		}
	}
}
