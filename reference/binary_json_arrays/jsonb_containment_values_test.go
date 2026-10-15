package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBContainmentValues(t *testing.T) {
	jsonbContainmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			name, a, z string
			want       bool
		}{
			{"object subset", `{"a":1,"b":2}`, `{"b":2}`, true},
			{"missing null key", `{"a":null}`, `{"missing":null}`, false},
			{"present null key", `{"a":null}`, `{"a":null}`, true},
			{"nested subset", `{"a":{"b":1,"c":2}}`, `{"a":{"b":1}}`, true},
			{"detached key", `{"a":{"b":1}}`, `{"b":1}`, false},
			{"nested empty object", `{"a":{"b":1}}`, `{"a":{}}`, true},
			{"order and duplicates", `[1,2,3]`, `[3,1,1]`, true},
			{"longer rhs", `[1]`, `[1,1,1]`, true},
			{"no flatten", `[1,2,[1,3]]`, `[1,3]`, false},
			{"nested array order", `[1,2,[1,3]]`, `[[3,1,1]]`, true},
			{"reusable container", `[{"a":1,"b":2}]`, `[{"a":1},{"b":2}]`, true},
			{"no container merge", `[{"a":1},{"b":2}]`, `[{"a":1,"b":2}]`, false},
			{"primitive membership", `["foo","bar"]`, `"bar"`, true},
			{"nonreciprocal membership", `"bar"`, `["bar"]`, false},
			{"immediate membership only", `[[1]]`, `1`, false},
			{"object value kind gate", `{"a":[1]}`, `{"a":1}`, false},
			{"numeric scale", `1.00`, `1`, true},
			{"scalar kind", `"1"`, `1`, false},
			{"json null", `null`, `null`, true},
			{"null membership", `[null]`, `null`, true},
			{"boolean equal", `true`, `true`, true},
			{"boolean different", `false`, `true`, false},
			{"empty object", `{}`, `{}`, true},
			{"empty array", `[]`, `[]`, true},
			{"array empty subset", `[1]`, `[]`, true},
			{"array not object", `[]`, `{}`, false},
			{"object not array", `{}`, `[]`, false},
			{"scalar not empty array", `1`, `[]`, false},
			{"duplicate key last", `{"a":1,"a":2}`, `{"a":2}`, true},
			{"duplicate key discarded", `{"a":1,"a":2}`, `{"a":1}`, false},
			{"decoded key", `{"\u0061":1}`, `{"a":1}`, true},
			{"exact large mismatch", `9007199254740993`, `9007199254740992`, false},
			{"exact large scale", `9007199254740993.00`, `9007199254740993`, true},
		} {
			t.Run(tc.name, func(t *testing.T) {
				for _, op := range []string{"@>", "<@"} {
					a, z := tc.a, tc.z
					if op == "<@" {
						a, z = z, a
					}
					q := "SELECT " + jsonTypeQuote(a) + "::jsonb " + op + " " + jsonTypeQuote(z) + "::jsonb AS v"
					s := jsonbExistsSelect(q, tc.want)
					s.CheckMetadata = jsonbExistsMetadata
					jsonbExistsBoth(t, p, b, c, s)
					s = jsonbExistsSelect("SELECT $1::jsonb "+op+" $2::jsonb AS v", tc.want, a, z)
					s.CheckMetadata = jsonbExistsMetadata
					jsonbExistsBoth(t, p, b, c, s)
				}
			})
		}
	})
}
