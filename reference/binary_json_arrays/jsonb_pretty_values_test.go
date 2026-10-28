package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

const jsonbPrettyNestedInput = `{"long":[1,{"x":true},[]],"a":{"zz":null,"b":"x\n"}}`
const jsonbPrettyNested = "{\n    \"a\": {\n        \"b\": \"x\\n\",\n        \"zz\": null\n    },\n    \"long\": [\n        1,\n        {\n            \"x\": true\n        },\n        [\n        ]\n    ]\n}"
const jsonbPrettyOrdered = "{\n    \"a\": 2,\n    \"b\": 4,\n    \"aa\": 3,\n    \"zz\": 0,\n    \"é\": 5,\n    \"aaa\": 6\n}"
const jsonbPrettyEmptyNested = "[\n    [\n    ],\n    {\n    }\n]"
const jsonbPrettyEscapeInput = `{"s":"\"\\\/\b\f\n\r\t\u0001\u001f"}`
const jsonbPrettyEscaped = "{\n    \"s\": \"\\\"\\\\/\\b\\f\\n\\r\\t\\u0001\\u001f\"\n}"

func jsonbPrettyOne(q string, want any, args ...any) fixture.Step {
	return fixture.Select(q, []string{"pretty"}, []string{"TEXT"}, [][]any{{want}}, args...)
}

func jsonbPrettyBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, c fixture.Executor, s fixture.Step) {
	t.Helper()
	jsonTypeBoth(t, p, b, c, fixture.JSONTypeWithMetadata(s, b.PG))
}

func TestReferenceJSONBPrettyValues(t *testing.T) {
	jsonTypeScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		cases := []struct {
			name        string
			input, want any
		}{
			{"empty-array", "[]", "[\n]"}, {"empty-object", "{ }", "{\n}"},
			{"json-null", "null", "null"}, {"true", "true", "true"}, {"false", "false", "false"},
			{"empty-string", `""`, `""`}, {"scale", "1.230e-5", "0.00001230"},
			{"large-number", "123456789012345678901234567890.1200", "123456789012345678901234567890.1200"},
			{"unicode", `"é\uD83D\uDE00<>&\/"`, `"é😀<>&/"`}, {"sql-null", nil, nil},
			{"nested-empty", "[[],{}]", jsonbPrettyEmptyNested},
			{"nested", jsonbPrettyNestedInput, jsonbPrettyNested},
			{"canonical", jsonTypeOrderedObject, jsonbPrettyOrdered},
			{"reordered", ` { "aaa":6,"é":5,"zz":0,"aa":3,"b":4,"a":2 } `, jsonbPrettyOrdered},
			{"escaping", jsonbPrettyEscapeInput, jsonbPrettyEscaped},
			{"key-escaping", `{"a\n\"\\":1}`, "{\n    \"a\\n\\\"\\\\\": 1\n}"},
		}
		// An independent closed form covers stack growth, not a second formatter.
		const depth = 40
		var deep strings.Builder
		for i := range depth {
			deep.WriteString(strings.Repeat(" ", 4*i))
			deep.WriteString("[\n")
		}
		deep.WriteString(strings.Repeat(" ", 4*depth))
		deep.WriteString("0")
		for i := depth - 1; i >= 0; i-- {
			deep.WriteByte('\n')
			deep.WriteString(strings.Repeat(" ", 4*i))
			deep.WriteByte(']')
		}
		cases = append(cases, struct {
			name        string
			input, want any
		}{"deep", strings.Repeat("[", depth) + "0" + strings.Repeat("]", depth), deep.String()})
		cases = append(cases, struct {
			name        string
			input, want any
		}{"long-string", `"` + strings.Repeat(`\n`, 2048) + `"`, `"` + strings.Repeat(`\n`, 2048) + `"`})
		cases = append(cases, struct {
			name        string
			input, want any
		}{"numeric-zero-span", "1e4096", "1" + strings.Repeat("0", 4096)})
		cases = append(cases, struct {
			name        string
			input, want any
		}{"wide", "[" + strings.Repeat("1,", 299) + "1]", "[\n" + strings.Repeat("    1,\n", 299) + "    1\n]"})
		longPrefix := strings.Repeat("k", 2048)
		cases = append(cases, struct {
			name        string
			input, want any
		}{"long-key-prefix", `{"` + longPrefix + `b":2,"` + longPrefix + `a":1}`, "{\n    \"" + longPrefix + "a\": 1,\n    \"" + longPrefix + "b\": 2\n}"})
		cases = append(cases, struct {
			name        string
			input, want any
		}{"max-numeric-digits", "1e131071", "1" + strings.Repeat("0", 131071)})
		cases = append(cases, struct {
			name        string
			input, want any
		}{"max-numeric-scale", "1e-16383", "0." + strings.Repeat("0", 16382) + "1"})
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne("SELECT jsonb_pretty($1) AS pretty", tc.want, tc.input))
				if text, ok := tc.input.(string); ok {
					jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne("SELECT jsonb_pretty("+jsonTypeQuote(text)+") AS pretty", tc.want))
				}
			})
		}
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE jbp_values(j jsonb)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("INSERT INTO jbp_values VALUES($1)", 1, jsonbPrettyNestedInput))
		jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne("SELECT jsonb_pretty(j) AS pretty FROM jbp_values", jsonbPrettyNested))
		jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne("SELECT jsonb_pretty(j->'a') AS pretty FROM jbp_values", "{\n    \"b\": \"x\\n\",\n    \"zz\": null\n}"))
		jsonbPrettyBoth(t, p, b, c, jsonbPrettyOne("SELECT jsonb_pretty(jsonb_build_array(1,true,NULL)) AS pretty", "[\n    1,\n    true,\n    null\n]"))
	})
}
