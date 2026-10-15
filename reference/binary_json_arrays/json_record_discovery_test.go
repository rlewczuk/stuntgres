package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONRecordDiscoveryDomains(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE DOMAIN jr_pair_domain AS jr_pair CHECK ((VALUE).a > 0)",
			"CREATE DOMAIN jr_positive AS integer CHECK (VALUE > 0)",
			"CREATE DOMAIN jr_checked_required AS integer CHECK (VALUE IS NOT NULL)",
			"CREATE TYPE jr_domain_fields AS (a jr_positive, b jr_checked_required)",
			"CREATE DOMAIN jr_plain_domain AS jr_pair",
			"CREATE DOMAIN jr_array_domain AS integer[] CHECK (array_length(VALUE, 1) = 2)",
			"CREATE TYPE jr_domain_array AS (a jr_array_domain)",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		// Native CHECK enforcement is real and shared below. NOT NULL DDL is
		// an existing, explicitly asserted boundary, not a substitute domain.
		for _, q := range []string{"CREATE DOMAIN jr_pair_nn AS jr_pair NOT NULL", "CREATE DOMAIN jr_required AS integer NOT NULL"} {
			if b.PG {
				fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
			} else {
				want := jsonRecordDiagnostic("0A000", "domain constraints and defaults are unsupported", strings.Index(q, "NOT NULL")+1)
				jsonRecordErrorBoth(t, p, b, c, q, want, false)
			}
		}
		if b.PG {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TYPE jr_nn_fields AS (a jr_positive,b jr_required)", 0))
			for _, tc := range []struct {
				q    string
				want fixture.Diagnostic
			}{
				{`SELECT json_populate_record(NULL::jr_pair_nn, '{}')`, jsonRecordDomain("23502", "jr_pair_nn", "")},
				{`SELECT * FROM json_populate_record(NULL::jr_nn_fields, '{"a":1,"b":null}')`, jsonRecordDomain("23502", "jr_required", "")},
				{`SELECT * FROM json_populate_record(NULL::jr_nn_fields, '{"a":1}')`, jsonRecordDomain("23502", "jr_required", "")},
			} {
				jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, false)
			}
		} else {
			for _, name := range []string{"jr_pair_nn", "jr_required"} {
				jsonRecordErrorBoth(t, p, b, c, "SELECT NULL::"+name, jsonRecordDiagnostic("42704", `type "`+name+`" does not exist`, 14), true)
			}
		}
		// pg_typeof is not an executable native provider. Keep the PostgreSQL
		// logical identity witnesses, with an explicit native inquiry boundary;
		// shared raw results and metadata below still exercise each domain.
		for _, tc := range []struct{ q, domain string }{
			{`SELECT pg_typeof(json_populate_record(NULL::jr_pair_domain, '{"a":2}'))::text`, "jr_pair_domain"},
			{`SELECT pg_typeof(json_populate_record(NULL::jr_plain_domain, '{"a":2}'))::text`, "jr_plain_domain"},
			{`SELECT pg_typeof(json_populate_recordset(NULL::jr_pair_domain, '[{"a":2}]'))::text`, "jr_pair_domain"},
		} {
			if b.PG {
				jsonRecordBoth(t, p, b, c, fixture.Select(tc.q, []string{"pg_typeof"}, []string{"TEXT"}, [][]any{{tc.domain}}))
			} else {
				jsonRecordErrorBoth(t, p, b, c, tc.q, jsonRecordSignature("pg_typeof("+tc.domain+")", 8), true)
			}
		}
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"identity", fixture.Select(`SELECT json_populate_record(NULL::jr_pair_domain, '{"a":2}') AS r`, []string{"r"}, []string{""}, [][]any{{"(2,)"}})},
			{"plain-domain", fixture.Select(`SELECT json_populate_record(NULL::jr_plain_domain, '{"a":2}') AS r`, []string{"r"}, []string{""}, [][]any{{"(2,)"}})},
			{"base", jsonRecordPair(`SELECT * FROM json_populate_record('(7,keep)'::jr_pair_domain, '{"a":2}')`, [][]any{{int64(2), "keep"}})},
			{"sql-null", fixture.Select(`SELECT json_populate_record(NULL::jr_pair_domain, NULL)`, []string{"json_populate_record"}, []string{""}, [][]any{{nil}})},
			{"preserved-sql-null", fixture.Select(`SELECT json_populate_record('(7,keep)'::jr_pair_domain, NULL)::text`, []string{"json_populate_record"}, []string{"TEXT"}, [][]any{{"(7,keep)"}})},
			{"preserved-field", fixture.Select(`SELECT * FROM json_populate_record('(1,2)'::jr_domain_fields, '{"a":3}')`, []string{"a", "b"}, []string{"INT4", "INT4"}, [][]any{{int64(3), int64(2)}})},
			{"array-success", fixture.Select(`SELECT * FROM json_populate_record(NULL::jr_domain_array, '{"a":[1,2]}')`, []string{"a"}, []string{"_INT4"}, [][]any{{"{1,2}"}})},
			{"set-identity", fixture.Select(`SELECT json_populate_recordset(NULL::jr_pair_domain, '[{"a":2}]') AS r`, []string{"r"}, []string{""}, [][]any{{"(2,)"}})},
			{"set-null", jsonRecordPair(`SELECT * FROM json_populate_recordset(NULL::jr_pair_domain, NULL)`, nil)},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordBoth(t, p, b, c, jsonRecordMetadata(tc.step, b.PG)) })
		}
		for _, tc := range []struct {
			name, q string
			want    fixture.Diagnostic
		}{
			{"outer-check", `SELECT * FROM json_populate_record(NULL::jr_pair_domain, '{"a":-1}')`, jsonRecordDomain("23514", "jr_pair_domain", "jr_pair_domain_check")},
			{"null-field", `SELECT * FROM json_populate_record(NULL::jr_domain_fields, '{"a":1,"b":null}')`, jsonRecordDomain("23514", "jr_checked_required", "jr_checked_required_check")},
			{"missing-field", `SELECT * FROM json_populate_record(NULL::jr_domain_fields, '{"a":1}')`, jsonRecordDomain("23514", "jr_checked_required", "jr_checked_required_check")},
			{"field-check", `SELECT * FROM json_to_record('{"a":-1}') AS r(a jr_positive)`, jsonRecordDomain("23514", "jr_positive", "jr_positive_check")},
			{"array-check", `SELECT * FROM json_populate_record(NULL::jr_domain_array, '{"a":[1]}')`, jsonRecordDomain("23514", "jr_array_domain", "jr_array_domain_check")},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, false) })
		}
	})
}

func TestReferenceJSONRecordDiscoveryDescriptors(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"scalar-to-null", fixture.Select(`SELECT json_to_record(NULL::json)`, []string{"json_to_record"}, []string{"RECORD"}, [][]any{{nil}})},
			{"row-base-scalar", fixture.Select(`SELECT json_populate_record(ROW(7,'keep'::text), '{"f1":2}')`, []string{"json_populate_record"}, []string{"RECORD"}, [][]any{{"(2,keep)"}})},
			{"row-base-definitions", jsonRecordPair(`SELECT * FROM json_populate_record(ROW(7,'keep'::text), '{"f1":2}') AS r(a int,b text)`, [][]any{{int64(2), "keep"}})},
			{"known-null", fixture.Select(`SELECT json_populate_record(NULL::jr_pair, NULL::json)`, []string{"json_populate_record"}, []string{""}, [][]any{{nil}})},
			{"known-null-from", jsonRecordPair(`SELECT * FROM json_populate_record(NULL::jr_pair, NULL::json)`, [][]any{{nil, nil}})},
			{"to-null-from", jsonRecordPair(`SELECT * FROM json_to_record(NULL::json) AS r(a int,b text)`, [][]any{{nil, nil}})},
			{"zero-width", fixture.Select(`SELECT count(*) FROM json_populate_record(NULL::jr_empty, '{}')`, []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}})},
			{"zero-width-set", fixture.Select(`SELECT count(*) FROM json_populate_recordset(NULL::jr_empty, '[{},{}]')`, []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}})},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordBoth(t, p, b, c, jsonRecordMetadata(tc.step, b.PG)) })
		}
		missingList := jsonRecordDiagnostic("42601", `a column definition list is required for functions returning "record"`, 15)
		unknown := jsonRecordDiagnostic("42804", "could not determine polymorphic type because input has type unknown", 0)
		for _, tc := range []struct {
			name, q  string
			want     fixture.Diagnostic
			analysis bool
		}{
			{"scalar-to", `SELECT json_to_record('{"a":1}')`, jsonRecordMissingShape("json_to_record"), false},
			{"scalar-set", `SELECT json_to_recordset('[{"a":1}]')`, jsonRecordMissingShape("json_to_recordset"), false},
			{"from-to", `SELECT * FROM json_to_record('{"a":1}')`, missingList, true},
			{"from-set", `SELECT * FROM json_to_recordset('[{"a":1}]')`, missingList, true},
			{"unknown-base-scalar", `SELECT json_populate_record(NULL, '{}')`, unknown, true},
			{"unknown-base-from", `SELECT * FROM json_populate_record(NULL, '{}')`, unknown, true},
			{"record-base", `SELECT json_populate_record(NULL::record, '{}')`, jsonRecordMissingShape("json_populate_record"), false},
			{"row-base-from", `SELECT * FROM json_populate_record(ROW(7,'keep'::text), '{"f1":2}')`, missingList, true},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, tc.analysis) })
		}
		for _, tc := range []struct{ columns, detail string }{
			{"a text,b int", "Returned type integer at ordinal position 1, but query expects text."},
			{"a int", "Returned row contains 2 attributes, but query expects 1."},
			{"a int,b text,c int", "Returned row contains 2 attributes, but query expects 3."},
		} {
			want := jsonRecordDiagnostic("42804", "function return row and query-specified return row do not match", 0)
			want.Detail = tc.detail
			jsonRecordErrorBoth(t, p, b, c, `SELECT * FROM json_populate_record(ROW(7,'keep'::text),'{}') AS r(`+tc.columns+`)`, want, false)
		}
		jsonRecordBoth(t, p, b, c, jsonRecordMetadata(fixture.Select(`SELECT * FROM json_populate_record(NULL::record,'{"a":2}') AS r(a int)`, []string{"a"}, []string{"INT4"}, [][]any{{int64(2)}}), b.PG))
		t.Run("output-layout", func(t *testing.T) {
			for _, function := range []string{"json_populate_record", "json_populate_recordset"} {
				for _, base := range []struct {
					name, expression string
					value            any
				}{
					{"nonnull", "'x'::varchar(3)", "x"},
					{"null", "NULL::varchar(3)", nil},
				} {
					t.Run(function+"/"+base.name, func(t *testing.T) {
						for _, input := range []struct {
							text  string
							value any
						}{
							{`{}`, base.value},
							{`{"f1":"xy","a":"ignored"}`, "xy"},
							{`{"f1":null}`, nil},
						} {
							document := input.text
							if function == "json_populate_recordset" {
								document = "[" + document + "]"
							}
							q := "SELECT * FROM " + function + "(ROW(" + base.expression + "),'" + document + "') AS r(a text)"
							step := fixture.Select(q, []string{"a"}, []string{"TEXT"}, [][]any{{input.value}})
							jsonRecordBoth(t, p, b, c, jsonRecordMetadata(step, b.PG))
						}
						if function == "json_populate_recordset" {
							q := "SELECT * FROM ROWS FROM(" + function + "(ROW(" + base.expression + `),'[{},{"f1":"xy","a":"ignored"},{"f1":null},{}]') AS (a text)) WITH ORDINALITY AS r(a,n) ORDER BY n`
							step := fixture.Select(q, []string{"a", "n"}, []string{"TEXT", "INT8"}, [][]any{{base.value, int64(1)}, {"xy", int64(2)}, {nil, int64(3)}, {base.value, int64(4)}})
							jsonRecordBoth(t, p, b, c, jsonRecordMetadata(step, b.PG))
						}
					})
				}
				for _, tc := range []struct {
					name, base, declaration string
					value                   any
					length                  int64
				}{
					{"reverse-unconstrained", "'x'::text", "varchar", "x", -1},
					{"reverse-typmod", "'x'::text", "varchar(3)", "x", 3},
					{"reverse-typmod-null", "NULL::text", "varchar(3)", nil, 3},
					{"varchar-typmod", "'x'::varchar(3)", "varchar(2)", "x", 2},
					{"text-payload-not-sized", "'long'::text", "varchar(2)", "long", 2},
					{"varchar-payload-not-sized", "'xyz'::varchar(3)", "varchar(2)", "xyz", 2},
				} {
					document := `{}`
					if function == "json_populate_recordset" {
						document = "[" + document + "]"
					}
					q := "SELECT * FROM " + function + "(ROW(" + tc.base + "),'" + document + "') AS r(a " + tc.declaration + ")"
					step := fixture.Select(q, []string{"a"}, []string{"VARCHAR"}, [][]any{{tc.value}})
					t.Run(function+"/"+tc.name, func(t *testing.T) { jsonRecordBoth(t, p, b, c, jsonRecordMetadata(step, b.PG, tc.length)) })
				}
				for _, tc := range []struct{ name, base, columns, document, code, message, detail string }{
					{"padded-character", "'x'::char(3)", "a text", `{}`, "42804", "function return row and query-specified return row do not match", "Returned type character at ordinal position 1, but query expects text."},
					{"numeric-assignment-not-binary", "7", "a bigint", `{}`, "42804", "function return row and query-specified return row do not match", "Returned type integer at ordinal position 1, but query expects bigint."},
					{"singular-width", "7", "a int,b text", `{}`, "42804", "function return row and query-specified return row do not match", "Returned row contains 1 attribute, but query expects 2."},
					{"original-conversion-typmod", "'x'::varchar(3)", "a text", `{"f1":"long"}`, "22001", "value too long for type character varying(3)", ""},
				} {
					document := tc.document
					if function == "json_populate_recordset" {
						document = "[" + document + "]"
					}
					q := "SELECT * FROM " + function + "(ROW(" + tc.base + "),'" + document + "') AS r(" + tc.columns + ")"
					want := jsonRecordDiagnostic(tc.code, tc.message, 0)
					want.Detail = tc.detail
					t.Run(function+"/"+tc.name, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, q, want, false) })
				}
			}
		})
		// Pinned PostgreSQL observations: decode all strings in the demanded
		// object before field conversion, but do not decode later rows early.
		for _, function := range []string{"json_to_recordset", "json_populate_recordset"} {
			for _, tc := range []struct{ input, code, message, detail, context string }{
				{`[{"a":"bad"},{"ignored":"\u0000"}]`, "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
				{`[{"ignored":"\u0000"},{"a":"bad"}]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: [{"ignored":"\u0000...`},
				{`[{"a":"bad"},{"ignored":"\ud800"}]`, "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
				{`[{"ignored":"\ud800"},{"a":"bad"}]`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: [{"ignored":"\ud800"...`},
				{`[{"a":"bad","ignored":"\u0000"}]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: [{"a":"bad","ignored":"\u0000...`},
				{`[{"a":"bad","ignored":"\ud800"}]`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: [{"a":"bad","ignored":"\ud800"...`},
				{`["\u0000"]`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: ["\u0000...`},
				{`["\ud800"]`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: ["\ud800"...`},
				{`"\u0000"`, "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 1: "\u0000...`},
				{`"\ud800"`, "22P02", "invalid input syntax for type json", "Unicode low surrogate must follow a high surrogate.", `JSON data, line 1: "\ud800"`},
				{`[1,{"ignored":"\u0000"}]`, "22023", "argument of " + function + " must be an array of objects", "", ""},
				{`[{"a":"bad"},null]`, "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
				{`[null,{"a":"bad"}]`, "22023", "argument of " + function + " must be an array of objects", "", ""},
				{"[\n {\"a\":1},\n {\"ignored\":\"\\u0000\"}\n]", "22P05", "unsupported Unicode escape sequence", `\u0000 cannot be converted to text.`, `JSON data, line 3:  {"ignored":"\u0000...`},
			} {
				q := "SELECT * FROM " + function + "('" + tc.input + "') AS r(a int)"
				if function == "json_populate_recordset" {
					q = "SELECT * FROM " + function + "(NULL::jr_pair,'" + tc.input + "')"
				}
				want := jsonRecordDiagnostic(tc.code, tc.message, 0)
				want.Detail, want.Context = tc.detail, tc.context
				t.Run(q, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, q, want, false) })
			}
		}
		for _, function := range []string{"json_to_record", "json_populate_record"} {
			q := "SELECT * FROM " + function + `('{"a":"bad","ignored":"\u0000"}') AS r(a int)`
			if function == "json_populate_record" {
				q = "SELECT * FROM " + function + `(NULL::jr_pair,'{"a":"bad","ignored":"\u0000"}')`
			}
			want := jsonRecordDiagnostic("22P05", "unsupported Unicode escape sequence", 0)
			want.Detail = `\u0000 cannot be converted to text.`
			want.Context = `JSON data, line 1: {"a":"bad","ignored":"\u0000...`
			jsonRecordErrorBoth(t, p, b, c, q, want, false)
		}
	})
}

func TestReferenceJSONRecordDiscoveryDefinitions(t *testing.T) {
	jsonRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name string
			step fixture.Step
		}{
			{"per-member", fixture.Select(`SELECT * FROM ROWS FROM(json_to_record('{"a":1}') AS (a int,b text), json_to_recordset('[{"c":"x"}]') AS (c varchar(2)))`, []string{"a", "b", "c"}, []string{"INT4", "TEXT", "VARCHAR"}, [][]any{{int64(1), nil, "x"}})},
			{"pseudo", fixture.Select(`SELECT * FROM json_to_record('{}') AS r(a record)`, []string{"a"}, []string{"RECORD"}, [][]any{{nil}})},
			{"ordinality-member", fixture.Select(`SELECT * FROM ROWS FROM(json_to_record('{}') AS (a int,b text)) WITH ORDINALITY AS r(a,b,n)`, []string{"a", "b", "n"}, []string{"INT4", "TEXT", "INT8"}, [][]any{{nil, nil, int64(1)}})},
			{"named-arguments", jsonRecordPair(`SELECT * FROM json_populate_record(base => NULL::jr_pair, from_json => '{"a":2}')`, [][]any{{int64(2), nil}})},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordBoth(t, p, b, c, tc.step) })
		}
		ordinality := jsonRecordDiagnostic("42601", "WITH ORDINALITY cannot be used with a column definition list", 57)
		ordinality.Hint = "Put the column definition list inside ROWS FROM()."
		multiple := jsonRecordDiagnostic("42601", "ROWS FROM() with multiple functions cannot have a column definition list", 74)
		multiple.Hint = "Put a separate column definition list for each function inside ROWS FROM()."
		nested := jsonRecordDiagnostic("0A000", "set-returning functions must appear at top level of FROM", 30)
		for _, tc := range []struct {
			name, q string
			want    fixture.Diagnostic
		}{
			{"missing-before-bad-arity", `SELECT * FROM json_to_record()`, jsonRecordSignature("json_to_record()", 15)},
			{"missing-before-bad-type", `SELECT * FROM json_to_record(1)`, jsonRecordSignature("json_to_record(integer)", 15)},
			{"typed-bad-type", `SELECT * FROM json_to_record(1) AS r(a int)`, jsonRecordSignature("json_to_record(integer)", 15)},
			{"typed-nested-srf", `SELECT * FROM json_to_record(generate_series(1,2)::text::json) AS r(a int)`, nested},
			{"missing-nested-srf", `SELECT * FROM json_to_record(generate_series(1,2)::text::json)`, nested},
			{"duplicate", `SELECT * FROM json_to_record('{}') AS r(a int,a text)`, jsonRecordDiagnostic("42701", `column name "a" specified more than once`, 0)},
			{"unknown-type", `SELECT * FROM json_to_record('{}') AS r(a jr_missing_type)`, jsonRecordDiagnostic("42704", `type "jr_missing_type" does not exist`, 43)},
			{"bad-typmod", `SELECT * FROM json_to_record('{}') AS r(a varchar(0))`, jsonRecordDiagnostic("22023", "length for type varchar must be at least 1", 43)},
			{"bad-collation", `SELECT * FROM json_to_record('{}') AS r(a int COLLATE "C")`, jsonRecordDiagnostic("42804", "collations are not supported by type integer", 47)},
			{"named-redundant", `SELECT * FROM json_populate_record(NULL::jr_pair, '{}') AS r(a int,b text)`, jsonRecordDiagnostic("42601", "a column definition list is redundant for a function returning a named composite type", 62)},
			{"out-redundant", `SELECT * FROM json_each('{}') AS r(a text,b json)`, jsonRecordDiagnostic("42601", "a column definition list is redundant for a function with OUT parameters", 36)},
			{"scalar-redundant", `SELECT * FROM generate_series(1,2) AS r(a int)`, jsonRecordDiagnostic("42601", `a column definition list is only allowed for functions returning "record"`, 41)},
			{"ordinality-top", `SELECT * FROM json_to_record('{}') WITH ORDINALITY AS r(a int,b text)`, ordinality},
			{"top-multiple", `SELECT * FROM ROWS FROM(json_to_record('{}'), generate_series(1,2)) AS r(a int,b text)`, multiple},
			{"member-and-top", `SELECT * FROM ROWS FROM(json_to_record('{}') AS (a int)) AS r(b int)`, jsonRecordDiagnostic("42601", "multiple column definition lists are not allowed for the same function", 63)},
			{"bad-signature-and-ordinality", `SELECT * FROM json_to_record(1) WITH ORDINALITY AS r(a int)`, jsonRecordSignature("json_to_record(integer)", 15)},
			{"bad-signature-and-count", `SELECT * FROM ROWS FROM(json_to_record(1), generate_series(1,2)) AS r(a int)`, jsonRecordSignature("json_to_record(integer)", 25)},
			{"nested-and-ordinality", `SELECT * FROM json_to_record(generate_series(1,2)::text::json) WITH ORDINALITY AS r(a int)`, nested},
			{"too-many-aliases", `SELECT * FROM json_populate_record(NULL::jr_pair, '{}') AS r(a,b,c)`, jsonRecordDiagnostic("42P10", `table "r" has 2 columns available but 3 columns specified`, 0)},
			{"to-named", `SELECT * FROM json_to_record(from_json => '{"a":2}') AS r(a int)`, jsonRecordSignature("json_to_record(from_json => unknown)", 15)},
			{"unicode-position", `SELECT 'é', * FROM json_to_record('{}') AS r(a int,a text)`, jsonRecordDiagnostic("42701", `column name "a" specified more than once`, 0)},
			{"full-input", `SELECT * FROM json_to_record('{}') AS r(a int) invalid suffix`, jsonRecordDiagnostic("42601", `syntax error at or near "invalid"`, 48)},
		} {
			t.Run(tc.name, func(t *testing.T) { jsonRecordErrorBoth(t, p, b, c, tc.q, tc.want, true) })
		}
	})
}
