package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordDiagnostics(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_diag AS (a int,b text)`, `CREATE TYPE jbr_diag_array AS (a int[])`, `CREATE TYPE jbr_diag_nested AS (r jbr_diag)`)
		jsonbRecordSetup(t, p, b, c, `CREATE TYPE jbr_diag_hint AS ("q""\key" int[])`)
		for _, f := range []string{"jsonb_populate_record", "jsonb_populate_recordset", "jsonb_to_record", "jsonb_to_recordset"} {
			set := strings.HasSuffix(f, "recordset")
			for _, input := range []string{`null`, `true`, `1`, `"x"`, `[]`, `{}`, `[{},null]`, `[{},[]]`, `[{},1]`, `[{"a":1},{"a":"bad"}]`} {
				if (!set && input == `{}`) || (set && input == `[]`) {
					continue
				}
				code, message := "22023", "cannot call populate_composite on a scalar"
				if set {
					message = "cannot call " + f + " on a non-array"
					if strings.HasPrefix(input, "[") {
						message = "argument of " + f + " must be an array of objects"
					}
					if input == `[{"a":1},{"a":"bad"}]` {
						code, message = "22P02", `invalid input syntax for type integer: "bad"`
					}
				} else if strings.HasPrefix(input, "[") {
					message = "cannot call populate_composite on an array"
				}
				for _, prefix := range []string{"", "/*é🙂*/ "} {
					for _, bound := range []bool{false, true} {
						argument, args := "'"+input+"'::jsonb", []any(nil)
						if bound {
							argument, args = "$1::jsonb", []any{input}
						}
						call := f + "("
						if strings.HasPrefix(f, "jsonb_populate") {
							call += "NULL::jbr_diag,"
						}
						call += argument + ")"
						q := prefix + "SELECT * FROM " + call
						if strings.HasPrefix(f, "jsonb_to") {
							q += " AS r(a int,b text)"
						}
						t.Run(q+input, func(t *testing.T) {
							fixture.CompositeReferenceError(t, p, c, q, "execute", fixture.ControlsReferenceDiagnostic(code, message), args...)
						})
					}
				}
			}
		}
		// Exact complete records include absent optional fields. SQL positions
		// count characters; runtime conversion diagnostics have no SQL position.
		for _, tc := range []struct {
			q, code, message, hint string
			position               int
			stage, detail, context string
		}{
			{`SELECT jsonb_to_record('{}'::jsonb)`, "0A000", "could not determine row type for result of jsonb_to_record", jsonbRecordRowHint, 0, "execute", "", ""},
			{`SELECT jsonb_to_recordset('[]'::jsonb)`, "0A000", "could not determine row type for result of jsonb_to_recordset", jsonbRecordRowHint, 0, "execute", "", ""},
			{`SELECT jsonb_populate_record(NULL::record,'{}'::jsonb)`, "0A000", "could not determine row type for result of jsonb_populate_record", jsonbRecordRowHint, 0, "execute", "", ""},
			{`SELECT jsonb_populate_record(1,'{}'::jsonb)`, "42804", "first argument of jsonb_populate_record must be a row type", "", 0, "execute", "", ""},
			{`SELECT jsonb_populate_recordset(1,'[]'::jsonb)`, "42804", "first argument of jsonb_populate_recordset must be a row type", "", 0, "execute", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb)`, "42601", `a column definition list is required for functions returning "record"`, "", 15, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_recordset('[]'::jsonb)`, "42601", `a column definition list is required for functions returning "record"`, "", 15, "prepare", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag,'{}'::jsonb) AS r(a int,b text)`, "42601", "a column definition list is redundant for a function returning a named composite type", "", 70, "prepare", "", ""},
			{`SELECT * FROM jsonb_each('{}'::jsonb) AS r(k text,v jsonb)`, "42601", "a column definition list is redundant for a function with OUT parameters", "", 44, "prepare", "", ""},
			{`SELECT * FROM generate_series(1,2) AS r(a int)`, "42601", `a column definition list is only allowed for functions returning "record"`, "", 41, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(a int,a text)`, "42701", `column name "a" specified more than once`, "", 0, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(a jbr_missing_type)`, "42704", `type "jbr_missing_type" does not exist`, "", 51, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(a varchar(-1))`, "42601", `syntax error at or near "-"`, "", 59, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(a int COLLATE "C")`, "42804", "collations are not supported by type integer", "", 55, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(a text COLLATE jbr_missing_collation)`, "42704", `collation "jbr_missing_collation" for encoding "UTF8" does not exist`, "", 56, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(a)`, "42601", `a column definition list is required for functions returning "record"`, "", 15, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record('{}'::jsonb) WITH ORDINALITY AS r(a int)`, "42601", "WITH ORDINALITY cannot be used with a column definition list", "Put the column definition list inside ROWS FROM().", 65, "prepare", "", ""},
			{`SELECT * FROM ROWS FROM(jsonb_to_record('{}'::jsonb),generate_series(1,2)) WITH ORDINALITY AS r(a int)`, "42601", "ROWS FROM() with multiple functions cannot have a column definition list", "Put a separate column definition list for each function inside ROWS FROM().", 97, "prepare", "", ""},
			{`SELECT * FROM ROWS FROM(jsonb_to_record('{}'::jsonb) AS (a int)) AS r(a int)`, "42601", "multiple column definition lists are not allowed for the same function", "", 71, "prepare", "", ""},
			{`SELECT * FROM ROWS FROM(jbr_missing_function(1),generate_series(1,2)) AS r(a int)`, "42883", "function jbr_missing_function(integer) does not exist", "No function matches the given name and argument types. You might need to add explicit type casts.", 25, "prepare", "", ""},
			{`SELECT * FROM jsonb_to_record(generate_series(1,2)::text::jsonb) AS r(a int)`, "0A000", "set-returning functions must appear at top level of FROM", "", 31, "prepare", "", ""},
			{`SELECT 1 WHERE jsonb_to_recordset('[]'::jsonb) IS NULL`, "0A000", "set-returning functions are not allowed in WHERE", "", 16, "prepare", "", ""},
			{`SELECT coalesce(jsonb_to_recordset('[]'::jsonb),NULL)`, "0A000", "set-returning functions are not allowed in COALESCE", "You might be able to move the set-returning function into a LATERAL FROM item.", 17, "prepare", "", ""},
			{`SELECT jsonb_populate_record(NULL::jbr_diag,'{}'::jsonb) FILTER(WHERE true)`, "42809", "FILTER specified, but jsonb_populate_record is not an aggregate function", "", 8, "prepare", "", ""},
			{`SELECT jsonb_populate_record(NULL::jbr_diag,'{}'::jsonb) OVER()`, "42809", "OVER specified, but jsonb_populate_record is not a window function nor an aggregate function", "", 8, "prepare", "", ""},
			{`SELECT jsonb_populate_record(DISTINCT NULL::jbr_diag,'{}'::jsonb)`, "42809", "DISTINCT specified, but jsonb_populate_record is not an aggregate function", "", 8, "prepare", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_array,'{"a":[[1],2]}'::jsonb)`, "22P02", "expected JSON array", `See the array element [1] of key "a".`, 0, "execute", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_array,'{"a":[[1],[2,3]]}'::jsonb)`, "22P02", "malformed JSON array", "", 0, "execute", "Multidimensional arrays must have sub-arrays with matching dimensions.", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_array,'{"a":[[[1]],[[2],3]]}'::jsonb)`, "22P02", "expected JSON array", `See the array element [1][1] of key "a".`, 0, "execute", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_array,'{"a":[[1],["bad",3]]}'::jsonb)`, "22P02", `invalid input syntax for type integer: "bad"`, "", 0, "execute", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_hint,'{"q\"\\key":1}'::jsonb)`, "22P02", "expected JSON array", `See the value of key "q"\key".`, 0, "execute", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_nested,'{"r":[]}'::jsonb)`, "22023", "cannot call populate_composite on an array", "", 0, "execute", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag_nested,'{"r":1}'::jsonb)`, "22023", "cannot call populate_composite on a scalar", "", 0, "execute", "", ""},
			{`SELECT * FROM jsonb_populate_record(NULL::jbr_diag,'{"a":}'::jsonb)`, "22P02", "invalid input syntax for type json", "", 52, "prepare", `Expected JSON value, but found "}".`, `JSON data, line 1: {"a":}`},
		} {
			for _, prefix := range []string{"", "/*é🙂*/ "} {
				t.Run(prefix+tc.q, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					d.Hint, d.Detail, d.Context = tc.hint, tc.detail, tc.context
					if tc.position != 0 {
						d.Position = fixture.Position(tc.position + len([]rune(prefix)))
					}
					fixture.CompositeReferenceError(t, p, c, prefix+tc.q, tc.stage, d)
				})
			}
		}
		var columns []string
		for i := range 1601 {
			columns = append(columns, "c"+fixture.Position(i+1)+" int")
		}
		t.Run("1601 definitions", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("54011", "column definition lists can have at most 1600 entries")
			d.Position = "49"
			fixture.CompositeReferenceError(t, p, c, `SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(`+strings.Join(columns, ",")+")", "prepare", d)
		})
		name := strings.Repeat("a", 63)
		t.Run("clipped duplicate", func(t *testing.T) {
			fixture.CompositeReferenceError(t, p, c, `SELECT * FROM jsonb_to_record('{}'::jsonb) AS r(`+name+`x int,`+name+`y text)`, "prepare", fixture.ControlsReferenceDiagnostic("42701", `column name "`+name+`" specified more than once`))
		})
	})
}

const jsonbRecordRowHint = "Provide a non-null record argument, or call the function in the FROM clause using a column definition list."
