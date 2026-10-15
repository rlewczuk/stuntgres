package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func jsonbExtractValues(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	for _, tc := range []struct {
		doc       string
		key       any
		raw, text any
	}{
		{`{"a":1,"a":2}`, "a", "2", "2"},
		{`{"0":"key"}`, "0", `"key"`, "key"},
		{`{"":"empty","A":"upper","a":"lower","雪":"é","a\"b":"quoted","a\\b":"slash"}`, "", `"empty"`, "empty"},
		{`{"A":"upper","a":"lower"}`, "A", `"upper"`, "upper"},
		{`{"A":"upper","a":"lower"}`, "a", `"lower"`, "lower"},
		{`{"雪":"é"}`, "雪", `"é"`, "é"},
		{`{"a\"b":"quoted"}`, `a"b`, `"quoted"`, "quoted"},
		{`{"a\\b":"slash"}`, `a\b`, `"slash"`, "slash"},
		{`{"a":"x\ny\\z\"\uD83D\uDE00"}`, "a", `"x\ny\\z\"😀"`, "x\ny\\z\"😀"},
		{`{"a":{"z":1,"z":2,"aa":1.230e-5}}`, "a", `{"z": 2, "aa": 0.00001230}`, `{"z": 2, "aa": 0.00001230}`},
		{`{"a":null}`, "a", "null", nil},
		{`{"a":true}`, "a", "true", "true"},
		{`{"a":false}`, "a", "false", "false"},
		{`{"a":""}`, "a", `""`, ""},
		{`{"a":[]}`, "a", "[]", "[]"},
		{`{"a":{}}`, "a", "{}", "{}"},
		{`{"a":1.2300}`, "a", "1.2300", "1.2300"},
		{`{}`, "a", nil, nil}, {`[]`, "0", nil, nil}, {`[10]`, "0", nil, nil},
		{`7`, "a", nil, nil}, {`null`, "a", nil, nil}, {`{"a":1}`, nil, nil, nil},
	} {
		t.Run(fmt.Sprintf("key/%s/%v", tc.doc, tc.key), func(t *testing.T) {
			jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::jsonb -> $2 AS v", "JSONB", tc.raw, tc.doc, tc.key))
			jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::jsonb ->> $2 AS v", "TEXT", tc.text, tc.doc, tc.key))
		})
	}
	for _, doc := range []any{nil, "[10,20,30]", "[]", `{"0":"key"}`} {
		for _, i := range []int{-2147483648, -4, -3, -1, 0, 2, 3, 2147483647} {
			var want any
			if doc == "[10,20,30]" {
				switch i {
				case -3, 0:
					want = "10"
				case -1, 2:
					want = "30"
				}
			}
			t.Run(fmt.Sprintf("index/%v/%d", doc, i), func(t *testing.T) {
				for _, tc := range []struct{ op, typ string }{{"->", "JSONB"}, {"->>", "TEXT"}} {
					jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::jsonb "+tc.op+" $2::int4 AS v", tc.typ, want, doc, i))
				}
			})
		}
	}
	for _, tc := range []struct {
		doc       string
		raw, text any
	}{{"7", "7", "7"}, {`"x\ny"`, `"x\ny"`, "x\ny"}, {"true", "true", "true"}, {"null", "null", nil}, {"[null]", "null", nil}} {
		// Integer operators index the physical scalar wrapper;
		// nonempty paths must not inherit this behavior.
		for _, i := range []int{0, -1, 1, -2} {
			t.Run(fmt.Sprintf("scalar/%s/%d", tc.doc, i), func(t *testing.T) {
				raw, text := tc.raw, tc.text
				if i == 1 || i == -2 {
					raw, text = nil, nil
				}
				jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::jsonb -> $2::int AS v", "JSONB", raw, tc.doc, i))
				jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT $1::jsonb ->> $2::int AS v", "TEXT", text, tc.doc, i))
			})
		}
	}
	jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT NULL::jsonb -> 'a' AS v", "JSONB", nil))
	jsonExtractBoth(t, p, b, c, jsonExtractOne("SELECT '[1]'::jsonb -> NULL::int AS v", "JSONB", nil))
}

func TestReferenceJSONBExtractValues(t *testing.T) { jsonbExtractScenario(t, jsonbExtractValues) }
