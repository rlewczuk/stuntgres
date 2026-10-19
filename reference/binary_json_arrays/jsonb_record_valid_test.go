package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBRecordValid(t *testing.T) {
	jsonbRecordScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonbRecordSetup(t, p, b, c,
			`CREATE TYPE jbr_valid_inner AS (a int,b text)`,
			`CREATE TYPE jbr_valid_mood AS ENUM ('ok')`,
			`CREATE DOMAIN jbr_valid_positive AS int CONSTRAINT jbr_positive_check CHECK(VALUE>0)`,
			`CREATE DOMAIN jbr_valid_required AS int CONSTRAINT jbr_required_check CHECK(VALUE IS NOT NULL)`,
			`CREATE DOMAIN jbr_valid_child AS jbr_valid_positive CONSTRAINT jbr_child_check CHECK(VALUE<100)`,
			`CREATE DOMAIN jbr_valid_cast_check AS text CONSTRAINT jbr_cast_check CHECK(VALUE::int>0)`,
			`CREATE DOMAIN jbr_valid_nested_check AS text CONSTRAINT jbr_nested_check CHECK(VALUE::jbr_valid_positive>0)`,
			`CREATE TYPE jbr_child_row AS (d jbr_valid_child)`,
			`CREATE TYPE jbr_check_cast_row AS (d jbr_valid_cast_check)`,
			`CREATE TYPE jbr_check_cast_array_row AS (d jbr_valid_cast_check[])`,
			`CREATE TYPE jbr_check_outer_row AS (d jbr_check_cast_row)`,
			`CREATE TYPE jbr_check_nested_row AS (d jbr_valid_nested_check)`,
			`CREATE TYPE jbr_valid AS (a int,c char(2),n numeric(3,1),r jbr_valid_inner,ar int[],m jbr_valid_mood,d jbr_valid_positive)`,
			`CREATE TYPE jbr_required_row AS (d jbr_valid_required)`,
			`CREATE DOMAIN jbr_checked_row AS jbr_valid_inner CONSTRAINT jbr_row_check CHECK((VALUE).a>0)`)
		for _, tc := range []struct {
			document      string
			valid         bool
			code, message string
		}{
			{`{}`, true, "", ""},
			{`{"a":1,"c":"aa","n":12.3,"r":{"a":2},"ar":[1,"2",null],"m":"ok","d":1}`, true, "", ""},
			{`{"ignored":{"a":"bad"}}`, true, "", ""},
			{`{"a":"bad"}`, false, "22P02", `invalid input syntax for type integer: "bad"`},
			{`{"a":1.5}`, false, "22P02", `invalid input syntax for type integer: "1.5"`},
			{`{"a":"1.5"}`, false, "22P02", `invalid input syntax for type integer: "1.5"`},
			{`{"a":2147483648}`, false, "22003", `value "2147483648" is out of range for type integer`},
			{`{"c":"aaa"}`, false, "22001", `value too long for type character(2)`},
			{`{"n":100}`, false, "22003", `numeric field overflow`},
			{`{"r":{"a":"bad"}}`, false, "22P02", `invalid input syntax for type integer: "bad"`},
			{`{"ar":[[1],[2,3]]}`, false, "22P02", `malformed JSON array`},
			{`{"ar":[[1],2]}`, false, "22P02", `expected JSON array`},
			{`{"m":"bad"}`, false, "22P02", `invalid input value for enum jbr_valid_mood: "bad"`},
			{`{"d":0}`, false, "23514", `value for domain jbr_valid_positive violates check constraint "jbr_positive_check"`},
			{`null`, false, "22023", `cannot call populate_composite on a scalar`},
			{`[]`, false, "22023", `cannot call populate_composite on an array`},
		} {
			t.Run(tc.document, func(t *testing.T) {
				jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(NULL::jbr_valid,$1::jsonb) AS valid`, []string{"valid"}, []string{"BOOL"}, [][]any{{tc.valid}}, tc.document))
				if tc.valid {
					return
				}
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				switch tc.message {
				case "numeric field overflow":
					d.Detail = "A field with precision 3, scale 1 must round to an absolute value less than 10^2."
				case "malformed JSON array":
					d.Detail = "Multidimensional arrays must have sub-arrays with matching dimensions."
				case "expected JSON array":
					d.Hint = `See the array element [1] of key "ar".`
				}
				if tc.code == "23514" {
					d.Schema, d.DataType, d.Constraint = "public", "jbr_valid_positive", "jbr_positive_check"
				}
				fixture.CompositeReferenceError(t, p, c, `SELECT jsonb_populate_record(NULL::jbr_valid,$1::jsonb)`, "execute", d, tc.document)
			})
		}
		for _, tc := range []struct {
			base, input string
			want        bool
		}{
			{"NULL", `{}`, false}, {"NULL", `{"d":null}`, false}, {"NULL", `{"d":1}`, true},
			{"'(7)'", `{}`, true}, {"'(7)'", `{"d":null}`, false},
		} {
			jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(`+tc.base+`::jbr_required_row,$1::jsonb) AS valid`, []string{"valid"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.input))
			if !tc.want {
				t.Run("required"+tc.base+tc.input, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("23514", `value for domain jbr_valid_required violates check constraint "jbr_required_check"`)
					d.Schema, d.DataType, d.Constraint = "public", "jbr_valid_required", "jbr_required_check"
					fixture.CompositeReferenceError(t, p, c, `SELECT jsonb_populate_record(`+tc.base+`::jbr_required_row,$1::jsonb)`, "execute", d, tc.input)
				})
			}
		}
		for _, tc := range []struct {
			input string
			want  bool
		}{{`{"a":1}`, true}, {`{"a":0}`, false}} {
			jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(NULL::jbr_checked_row,$1::jsonb) AS valid`, []string{"valid"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.input))
			if !tc.want {
				t.Run("whole domain", func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic("23514", `value for domain jbr_checked_row violates check constraint "jbr_row_check"`)
					d.Schema, d.DataType, d.Constraint = "public", "jbr_checked_row", "jbr_row_check"
					fixture.CompositeReferenceError(t, p, c, `SELECT jsonb_populate_record(NULL::jbr_checked_row,$1::jsonb)`, "execute", d, tc.input)
				})
			}
		}
		// Source-grounded paired observations: only the domain's own false CHECK
		// is soft; an exception thrown by its expression is not a saved error.
		for _, tc := range []struct{ typ, document, code, message, dataType, constraint string }{
			{"jbr_check_cast_row", `{"d":"bad"}`, "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
			{"jbr_check_cast_array_row", `{"d":"{bad}"}`, "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
			{"jbr_check_outer_row", `{"d":"(bad)"}`, "22P02", `invalid input syntax for type integer: "bad"`, "", ""},
			{"jbr_check_nested_row", `{"d":"-1"}`, "23514", `value for domain jbr_valid_positive violates check constraint "jbr_positive_check"`, "jbr_valid_positive", "jbr_positive_check"},
		} {
			for _, name := range []string{"jsonb_populate_record", "jsonb_populate_record_valid"} {
				t.Run(tc.typ+"/"+name, func(t *testing.T) {
					d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
					if tc.dataType != "" {
						d.Schema, d.DataType, d.Constraint = "public", tc.dataType, tc.constraint
					}
					fixture.CompositeReferenceError(t, p, c, `SELECT `+name+`(NULL::`+tc.typ+`,$1::jsonb)`, "execute", d, tc.document)
				})
			}
		}
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(NULL::jbr_child_row,'{"d":-1}'::jsonb) AS valid`, []string{"valid"}, []string{"BOOL"}, [][]any{{false}}))
		t.Run("inherited CHECK reports requested domain", func(t *testing.T) {
			d := fixture.ControlsReferenceDiagnostic("23514", `value for domain jbr_valid_child violates check constraint "jbr_positive_check"`)
			d.Schema, d.DataType, d.Constraint = "public", "jbr_valid_child", "jbr_positive_check"
			fixture.CompositeReferenceError(t, p, c, `SELECT jsonb_populate_record(NULL::jbr_child_row,$1::jsonb)`, "execute", d, `{"d":-1}`)
		})
		t.Run("domain NOT NULL conversion", func(t *testing.T) {
			q := `CREATE DOMAIN jbr_notnull_required AS int NOT NULL`
			jsonbRecordSetup(t, p, b, c, q, `CREATE TYPE jbr_notnull_row AS (d jbr_notnull_required)`)
			for _, tc := range []struct {
				base, input string
				want        bool
			}{
				{"NULL", `{}`, false}, {"NULL", `{"d":null}`, false}, {"NULL", `{"d":1}`, true}, {"'(7)'", `{}`, true}, {"'(7)'", `{"d":null}`, false},
			} {
				jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(`+tc.base+`::jbr_notnull_row,$1::jsonb) AS valid`, []string{"valid"}, []string{"BOOL"}, [][]any{{tc.want}}, tc.input))
				if !tc.want {
					d := fixture.ControlsReferenceDiagnostic("23502", "domain jbr_notnull_required does not allow null values")
					d.Schema, d.DataType = "public", "jbr_notnull_required"
					fixture.CompositeReferenceError(t, p, c, `SELECT jsonb_populate_record(`+tc.base+`::jbr_notnull_row,$1::jsonb)`, "execute", d, tc.input)
				}
			}
			for _, tc := range []struct {
				base, input string
				want        int64
			}{
				{"NULL", `{"d":1}`, 1},
				{"'(7)'", `{}`, 7},
			} {
				jsonEachBoth(t, p, b, c, fixture.Select(`SELECT * FROM jsonb_populate_record(`+tc.base+`::jbr_notnull_row,$1::jsonb)`, []string{"d"}, []string{"INT4"}, [][]any{{tc.want}}, tc.input))
			}
		})
		// Pinned PostgreSQL18.0: absent base plus SQL NULL propagates SQL NULL.
		jsonEachBoth(t, p, b, c, fixture.Select(`SELECT jsonb_populate_record_valid(NULL::jbr_valid_inner,NULL::jsonb) AS null_base,jsonb_populate_record_valid('(9,keep)'::jbr_valid_inner,NULL::jsonb) AS base`, []string{"null_base", "base"}, []string{"BOOL", "BOOL"}, [][]any{{nil, true}}))
		for _, tc := range []struct {
			q, code, message, hint, stage string
			args                          []any
		}{
			{`SELECT jsonb_populate_record_valid(1,'{}'::jsonb)`, "42804", "first argument of jsonb_populate_record must be a row type", "", "execute", nil},
			{`SELECT jsonb_populate_record_valid(NULL,'{}'::jsonb)`, "42804", "could not determine polymorphic type because input has type unknown", "", "prepare", nil},
			{`SELECT jsonb_populate_record_valid(NULL::record,'{}'::jsonb)`, "0A000", "could not determine row type for result of jsonb_populate_record", jsonbRecordRowHint, "execute", nil},
			{`SELECT jsonb_populate_record_valid(NULL::jbr_valid,$1::jsonb)`, "22P02", "invalid input syntax for type json", "", "execute", []any{`{"a":}`}},
		} {
			t.Run(tc.q, func(t *testing.T) {
				d := fixture.ControlsReferenceDiagnostic(tc.code, tc.message)
				d.Hint = tc.hint
				if tc.args != nil {
					d.Detail = `Expected JSON value, but found "}".`
					d.Context = "JSON data, line 1: {\"a\":}\nunnamed portal parameter $1 = '...'"
				}
				fixture.CompositeReferenceError(t, p, c, tc.q, tc.stage, d, tc.args...)
			})
		}
	})
}
