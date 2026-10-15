package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceSQLJSONObjectAggregateConversion(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE DOMAIN sjo_input_jsonb AS jsonb", 0))
		for _, tc := range []struct{ pair, typ, want string }{
			{"1:TRUE", "JSON", `{ "1" : true }`},
			{"TRUE:12.340::numeric", "JSON", `{ "true" : 12.340 }`},
			{"'':1", "JSON", `{ "" : 1 }`},
			{"'a':ARRAY[1,NULL,2]", "JSON", `{ "a" : [1,null,2] }`},
			{"'a':ROW(1,'x'::text)", "JSON", `{ "a" : {"f1":1,"f2":"x"} }`},
			{"'a':'[1]'::text", "JSON", `{ "a" : "[1]" }`},
			{"'a':'[1]'::text FORMAT JSON", "JSON", `{ "a" : [1] }`},
			{"'a':'[1]'::bytea FORMAT JSON", "JSON", `{ "a" : [1] }`},
			{"'a':'[1]'::bytea FORMAT JSON ENCODING UTF8", "JSON", `{ "a" : [1] }`},
			{"'a':'[1]'::json FORMAT JSON", "JSON", `{ "a" : [1] }`},
			{"'a':'[1]'::jsonb FORMAT JSON", "JSONB", `{"a": [1]}`},
			{"'a':'{\"b\":2,\"a\":1}'::jsonb", "JSONB", `{"a": {"a": 1, "b": 2}}`},
			{"'a':'{\"b\":2,\"a\":1}'::sjo_input_jsonb", "JSON", `{ "a" : {"a": 1, "b": 2} }`},
		} {
			t.Run(tc.pair, func(t *testing.T) {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg("+tc.pair+") AS v", tc.typ, tc.want))
			})
		}
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg($1::text:$2::text) AS v", "JSON", `{ "a\"\\\n" : "v\"\\\n" }`, "a\"\\\n", "v\"\\\n"))
		for _, key := range []string{"ARRAY[1,2]", "ROW(1,2)", "'{}'::json", "'{}'::jsonb"} {
			for _, unique := range []string{"", " WITH UNIQUE"} {
				for _, value := range []string{"1", "NULL::int"} {
					t.Run(key+unique+value, func(t *testing.T) {
						q := "SELECT json_objectagg(" + key + ":" + value + " ABSENT ON NULL" + unique + ") AS v"
						if unique == "" && value == "NULL::int" {
							typ, want := "JSON", `{  }`
							if key == "'{}'::jsonb" {
								typ, want = "JSONB", `{}`
							}
							jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q, typ, want))
						} else {
							d := fixture.ControlsReferenceDiagnostic("22023", "key value must be scalar, not array, composite, or json")
							jsonArrayAggregateError(t, p, b, q, d, false)
						}
					})
				}
			}
		}
		for _, suffix := range []string{"", " FILTER(WHERE FALSE)"} {
			t.Run("null-encoding/"+suffix, func(t *testing.T) {
				want := any(`{ "a" : null }`)
				if suffix != "" {
					want = nil
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('a':NULL::bytea FORMAT JSON ENCODING UTF16)"+suffix+" AS v", "JSON", want))
			})
		}
		// Keep malformed JSON row-dependent: a literal FORMAT conversion can
		// fail during PostgreSQL constant folding even with FILTER FALSE.
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE TABLE sjo_format_filter_input(v text)", 0))
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("INSERT INTO sjo_format_filter_input VALUES('no')", 1))
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg(NULL:v FORMAT JSON) FILTER(WHERE FALSE) AS v FROM sjo_format_filter_input", "JSON", nil))
	})
}

func TestReferenceSQLJSONObjectAggregateReturning(t *testing.T) {
	sqlJSONObjectAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		for _, q := range []string{
			"CREATE DOMAIN sjo_return_json AS json", "CREATE DOMAIN sjo_return_jsonb AS jsonb",
			"CREATE DOMAIN sjo_return_text AS text", "CREATE DOMAIN sjo_return_varchar AS varchar(11)",
			"CREATE DOMAIN sjo_return_empty AS text CHECK(VALUE='{  }')",
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec(q, 0))
		}
		for _, tc := range []struct{ clause, typ, want string }{
			{"json", "JSON", `{ "a" : 1 }`}, {"jsonb", "JSONB", `{"a": 1}`},
			{"jsonb FORMAT JSON", "JSONB", `{"a": 1}`}, {"text", "TEXT", `{ "a" : 1 }`},
			{"bytea", "BYTEA", `{ "a" : 1 }`}, {"bytea FORMAT JSON ENCODING UTF8", "BYTEA", `{ "a" : 1 }`},
			{"varchar(11)", "VARCHAR", `{ "a" : 1 }`}, {"char(13)", "BPCHAR", `{ "a" : 1 }  `},
			{"name", "NAME", `{ "a" : 1 }`}, {"sjo_return_json", "JSON", `{ "a" : 1 }`},
			{"sjo_return_jsonb", "JSONB", `{"a": 1}`}, {"sjo_return_text", "TEXT", `{ "a" : 1 }`},
			{"sjo_return_varchar", "VARCHAR", `{ "a" : 1 }`},
		} {
			t.Run(tc.clause, func(t *testing.T) {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('a':1 RETURNING "+tc.clause+") AS v", tc.typ, tc.want))
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('a':1 RETURNING "+tc.clause+") AS v WHERE FALSE", tc.typ, nil))
			})
		}
		for _, tc := range []struct{ target, typ, message string }{
			{"varchar(10)", "VARCHAR", "value too long for type character varying(10)"},
			{"char(10)", "BPCHAR", "value too long for type character(10)"},
		} {
			t.Run(tc.target, func(t *testing.T) {
				q := "SELECT json_objectagg('a':1 RETURNING " + tc.target + ") AS v"
				jsonArrayAggregateError(t, p, b, q, fixture.ControlsReferenceDiagnostic("22001", tc.message), false)
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect(q+" WHERE FALSE", tc.typ, nil))
			})
		}
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('a':NULL ABSENT ON NULL RETURNING sjo_return_empty) AS v", "TEXT", `{  }`))
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('a':1 RETURNING sjo_return_empty) AS v WHERE FALSE", "TEXT", nil))
		d := fixture.ControlsReferenceDiagnostic("23514", `value for domain sjo_return_empty violates check constraint "sjo_return_empty_check"`)
		d.Schema, d.DataType, d.Constraint = "public", "sjo_return_empty", "sjo_return_empty_check"
		jsonArrayAggregateError(t, p, b, "SELECT json_objectagg('a':1 RETURNING sjo_return_empty) AS v", d, false)
		jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT json_objectagg('é':'😀' RETURNING bytea FORMAT JSON ENCODING UTF8) AS v", "BYTEA", `{ "é" : "😀" }`))
	})
}
