package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONTableDiagnostics(t *testing.T) {
	for _, tc := range []struct{ name, q, token, code, message, detail string }{
		{"nonconstant null", `SELECT * FROM JSON_TABLE('{}',NULL COLUMNS(a int))`, `NULL`, "0A000", `only string constants are supported in JSON_TABLE path specification`, ""},
		{"nonconstant parameter", `SELECT * FROM JSON_TABLE('{}',$1 COLUMNS(a int))`, `$1`, "0A000", `only string constants are supported in JSON_TABLE path specification`, ""},
		{"nonconstant concatenate", `SELECT * FROM JSON_TABLE('{}',('$'||'.x') COLUMNS(a int))`, `('$'`, "0A000", `only string constants are supported in JSON_TABLE path specification`, ""},
		{"duplicate column", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a int,a int))`, `a int`, "42712", `duplicate JSON_TABLE column or path name: a`, ""},
		{"duplicate nested global", `SELECT * FROM JSON_TABLE('{}','$' AS root COLUMNS(a int,NESTED '$.x' AS root COLUMNS(b int)))`, `root COLUMNS(b`, "42712", `duplicate JSON_TABLE column or path name: root`, ""},
		{"duplicate ordinal", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a FOR ORDINALITY,b FOR ORDINALITY))`, `b FOR`, "42601", `only one FOR ORDINALITY column is allowed`, ""},
		{"table policy", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a int) NULL ON ERROR)`, `NULL`, "42601", `invalid ON ERROR behavior`, `Only EMPTY [ ARRAY ] or ERROR is allowed in the top-level ON ERROR clause.`},
		{"scalar policy", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a int EMPTY ARRAY ON EMPTY))`, `EMPTY ARRAY`, "42601", `invalid ON EMPTY behavior for column "a"`, `Only ERROR, NULL, or DEFAULT expression is allowed in ON EMPTY for scalar columns.`},
		{"quotes wrapper", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a text FORMAT JSON WITH WRAPPER OMIT QUOTES))`, `a text`, "42601", `SQL/JSON QUOTES behavior must not be specified when WITH WRAPPER is used`, ""},
		{"alias width", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a int)) AS t(x,y)`, ``, "42P10", `JSON_TABLE function has 1 columns available but 2 columns specified`, ""},
		{"empty columns", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS())`, `)`, "42601", `syntax error at or near ")"`, ""},
		{"scalar placement", `SELECT JSON_TABLE('{}','$')`, `(`, "42601", `syntax error at or near "("`, ""},
		{"exists empty", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a bool EXISTS PATH '$' NULL ON EMPTY))`, `EMPTY`, "42601", `syntax error at or near "EMPTY"`, ""},
		{"trailing garbage", `SELECT * FROM JSON_TABLE('{}','$' COLUMNS(a int)); garbage`, `garbage`, "42601", `syntax error at or near "garbage"`, ""},
	} {
		for _, prefix := range []string{"", "/*é🙂*/ "} {
			t.Run(tc.name+prefix, func(t *testing.T) {
				jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					q := prefix + tc.q
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Detail = tc.detail
					if tc.token != "" {
						idx := strings.LastIndex(q, tc.token)
						if tc.name == "empty columns" {
							idx = strings.Index(q, "COLUMNS(") + len("COLUMNS(")
						}
						d.Position = fixture.Position(len([]rune(q[:idx])) + 1)
					}
					var args []any
					if tc.name == "nonconstant parameter" {
						args = []any{"$"}
					}
					sqljsonDiagnostic(t, p, b, c, q, d, true, args...)
				})
			})
		}
	}
	for _, tc := range []struct{ name, doc, column, code, message string }{
		{"multiple", `[1,2]`, `a int PATH '$[*]' ERROR ON ERROR`, "22034", `JSON path expression for column "a" must return single scalar item`},
		{"object", `{}`, `a int PATH '$' ERROR ON ERROR`, "2203F", `JSON path expression for column "a" must return single scalar item`},
		{"conversion", `"bad"`, `a int PATH '$' ERROR ON ERROR`, "22P02", `invalid input syntax for type integer: "bad"`},
		{"typmod", `"abcd"`, `a varchar(3) PATH '$' ERROR ON ERROR`, "22001", `value too long for type character varying(3)`},
		{"strict member", `1`, `a int PATH 'strict $.x' ERROR ON ERROR`, "2203A", `jsonpath member accessor can only be applied to an object`},
		{"strict array", `1`, `a int PATH 'strict $[1]' ERROR ON ERROR`, "22039", `jsonpath array accessor can only be applied to an array`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				sqljsonDiagnostic(t, p, b, c, `SELECT * FROM JSON_TABLE($1::jsonb,'$' COLUMNS(`+tc.column+`))`, fixture.ControlsReferenceDiagnostic(tc.code, tc.message), false, tc.doc)
			})
		})
	}
	for _, tc := range []struct {
		name, q  string
		analysis bool
		d        fixture.Diagnostic
		args     []any
	}{
		{"malformed path", `SELECT * FROM JSON_TABLE($1::jsonb,'$.[' COLUMNS(a int))`, true, fixture.ControlsReferenceDiagnostic("42601", `syntax error at or near "[" of jsonpath input`), []any{`{}`}},
		{"bad document limit zero", `SELECT * FROM JSON_TABLE('bad','$' COLUMNS(a int)) LIMIT 0`, false, fixture.ControlsReferenceDiagnostic("22P02", `invalid input syntax for type json`), nil},
		{"null document unused default", `SELECT * FROM JSON_TABLE(NULL::jsonb,'$' COLUMNS(a int DEFAULT no_such_jt_routine() ON EMPTY))`, true, fixture.ControlsReferenceDiagnostic("42883", `function no_such_jt_routine() does not exist`), nil},
		{"bare default parameter", `SELECT * FROM JSON_TABLE($1::jsonb,'$[*]?(@ < $x)' PASSING $2::int AS x COLUMNS(r FOR ORDINALITY,v int PATH '$' ERROR ON ERROR,d int PATH '$.missing' DEFAULT $3::int ON EMPTY))`, true, fixture.ControlsReferenceDiagnostic("42804", `can only specify a constant, non-aggregate function, or operator expression for DEFAULT`), []any{`[1]`, 3, 7}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			jsonTableScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				d := tc.d
				switch tc.name {
				case "malformed path":
					d.Position = fixture.Position(36)
				case "bad document limit zero":
					d.Detail = `Token "bad" is invalid.`
					d.Context = `JSON data, line 1: bad`
				case "null document unused default":
					d.Position = fixture.Position(64)
					d.Hint = `No function matches the given name and argument types. You might need to add explicit type casts.`
				case "bare default parameter":
					d.Position = fixture.Position(159)
				}
				sqljsonDiagnostic(t, p, b, c, tc.q, d, tc.analysis, tc.args...)
			})
		})
	}
}
