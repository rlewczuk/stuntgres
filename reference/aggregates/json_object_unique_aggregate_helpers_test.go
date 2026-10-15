package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independently specified expectations confirmed against pinned PostgreSQL 18.0.
var jsonObjectUniqueAggregateFamilies = []struct {
	name, typ, singleton, nullValue, empty string
	strict                                 bool
}{
	{"json_object_agg_unique", "JSON", `{ "a" : 1 }`, `{ "a" : null }`, `{  }`, false},
	{"jsonb_object_agg_unique", "JSONB", `{"a": 1}`, `{"a": null}`, `{}`, false},
	{"json_object_agg_unique_strict", "JSON", `{ "a" : 1 }`, `{  }`, `{  }`, true},
	{"jsonb_object_agg_unique_strict", "JSONB", `{"a": 1}`, `{}`, `{}`, true},
}

func jsonObjectUniqueAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func jsonObjectUniqueAggregateSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE jou_input(id int,g text,k text,v int,j json)", 0),
		fixture.Exec(`INSERT INTO jou_input VALUES(1,'a','b',1,'null'),(2,'a','a',2,'{"x":1,"x":2}'),(3,'b','c',NULL,NULL)`, 3),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func jsonObjectUniqueAggregateCore(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, f := range jsonObjectUniqueAggregateFamilies {
		t.Run(f.name, func(t *testing.T) {
			for _, tc := range []struct {
				args, suffix string
				text, binary any
			}{
				{"k,v ORDER BY id", " FROM jou_input WHERE id<3", `{ "b" : 1, "a" : 2 }`, `{"a": 2, "b": 1}`},
				{"k,v ORDER BY id DESC", " FROM jou_input WHERE id<3", `{ "a" : 2, "b" : 1 }`, `{"a": 2, "b": 1}`},
				{"'a',1", "", f.singleton, f.singleton},
				{"'a',NULL", "", f.nullValue, f.nullValue},
				{"k,v", " FROM jou_input WHERE FALSE", nil, nil},
			} {
				want := tc.text
				if f.typ == "JSONB" {
					want = tc.binary
				}
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"("+tc.args+") AS v"+tc.suffix, f.typ, want))
			}
			for _, filter := range []string{"FALSE", "NULL::boolean"} {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(NULL,1/(id-id)) FILTER(WHERE "+filter+") AS v FROM jou_input", f.typ, nil))
			}
			s := fixture.Select("SELECT "+f.name+"(k,v) AS v FROM jou_input WHERE FALSE GROUP BY g", []string{"v"}, []string{f.typ}, nil)
			jsonArrayAggregateRoutes(t, p, b, s)
			jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(NULL,NULL)", jsonObjectAggregateNullKey(f.typ), false)
		})
	}
}
