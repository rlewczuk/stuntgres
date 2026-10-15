package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBContainmentFeatureSmoke(t *testing.T) {
	jsonbContainmentScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, tc := range []struct {
			expr string
			want any
		}{
			{`'{"a":{"b":1,"c":2}}'::jsonb @> '{"a":{"b":1}}'::jsonb`, true},
			{`'{"a":{"b":1}}'::jsonb <@ '{"a":{"b":1,"c":2}}'::jsonb`, true},
			{`'[1,2,3]'::jsonb @> '[3,1,1]'::jsonb`, true},
			{`'[[1]]'::jsonb @> '1'::jsonb`, false},
			{`'["foo","bar"]'::jsonb @> '"bar"'::jsonb`, true},
			{`'{"a":[1]}'::jsonb @> '{"a":1}'::jsonb`, false},
			{`NULL::jsonb @> '{}'::jsonb`, nil},
		} {
			s := jsonbExistsSelect("SELECT "+tc.expr+" AS v", tc.want)
			s.CheckMetadata = jsonbExistsMetadata
			jsonbExistsBoth(t, p, b, c, s)
		}
		jsonbContainmentHeld(t, p, b, c)
		s := jsonbExistsSignature("SELECT '{}'::jsonb @> 1", "jsonb @> integer", 20)
		s.ErrorStage = "execute"
		jsonTypeAnalysisError(t, p, b, c, s)
		jsonbContainmentRows(t, p, b, c)
		check := jsonbContainmentStored(t, p, b, c)
		jsonbContainmentControls(t, p, b, c, check)
	})
}
