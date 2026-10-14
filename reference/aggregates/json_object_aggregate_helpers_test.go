package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

// Independent object contracts were confirmed by root's pinned PG18.0 oracle.

var jsonObjectAggregateFamilies = []struct {
	name, typ, singleton, nullValue, empty string
	strict                                 bool
}{
	{"json_object_agg", "JSON", `{ "a" : 1 }`, `{ "a" : null }`, `{  }`, false},
	{"jsonb_object_agg", "JSONB", `{"a": 1}`, `{"a": null}`, `{}`, false},
	{"json_object_agg_strict", "JSON", `{ "a" : 1 }`, `{  }`, `{  }`, true},
	{"jsonb_object_agg_strict", "JSONB", `{"a": 1}`, `{}`, `{}`, true},
}

func jsonObjectAggregateScenario(t *testing.T, body func(*testing.T, *fixture.Pair, fixture.Backend)) {
	t.Helper()
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) { body(t, p, b) })
	}
}

func jsonObjectAggregateSetup(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, s := range []fixture.Step{
		fixture.Exec("CREATE TABLE joa_input(id int,g text,k text,v int,j json)", 0),
		fixture.Exec(`INSERT INTO joa_input VALUES(1,'a','b',1,'null'),(2,'a','a',2,' {"z":1,"a":1,"a":2} '),(3,'a','a',NULL,NULL)`, 3),
	} {
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
	}
}

func jsonObjectAggregateNullKey(typ string) fixture.Diagnostic {
	if typ == "JSONB" {
		return fixture.ControlsReferenceDiagnostic("22023", "field name must not be null")
	}
	return fixture.ControlsReferenceDiagnostic("22004", "null value not allowed for object key")
}

func jsonObjectAggregateCore(t *testing.T, p *fixture.Pair, b fixture.Backend) {
	t.Helper()
	for _, tc := range []struct {
		order string
		row   []any
	}{
		{"id", []any{`{ "b" : 1, "a" : 2, "a" : null }`, `{"a": null, "b": 1}`, `{ "b" : 1, "a" : 2 }`, `{"a": 2, "b": 1}`}},
		{"id DESC", []any{`{ "a" : null, "a" : 2, "b" : 1 }`, `{"a": 2, "b": 1}`, `{ "a" : 2, "b" : 1 }`, `{"a": 2, "b": 1}`}},
	} {
		q := "SELECT json_object_agg(k,v ORDER BY " + tc.order + ") AS j,jsonb_object_agg(k,v ORDER BY " + tc.order + ") AS b,json_object_agg_strict(k,v ORDER BY " + tc.order + ") AS s,jsonb_object_agg_strict(k,v ORDER BY " + tc.order + ") AS t FROM joa_input"
		jsonArrayAggregateRoutes(t, p, b, fixture.Select(q, []string{"j", "b", "s", "t"}, []string{"JSON", "JSONB", "JSON", "JSONB"}, [][]any{tc.row}))
	}
	for _, f := range jsonObjectAggregateFamilies {
		t.Run(f.name, func(t *testing.T) {
			for _, tc := range []struct {
				args, suffix string
				want         any
			}{
				{"'a',1", "", f.singleton},
				{"'a',NULL", "", f.nullValue},
				{"k,v", " FROM joa_input WHERE FALSE", nil},
				{"k,v", " FROM joa_input WHERE FALSE GROUP BY g", nil},
			} {
				s := jsonArrayAggregateSelect("SELECT "+f.name+"("+tc.args+") AS v"+tc.suffix, f.typ, tc.want)
				if tc.suffix == " FROM joa_input WHERE FALSE GROUP BY g" {
					s.Rows = nil
				}
				jsonArrayAggregateRoutes(t, p, b, s)
			}
			for _, filter := range []string{"FALSE", "NULL::boolean"} {
				jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(NULL,1/(id-id)) FILTER(WHERE "+filter+") AS v FROM joa_input", f.typ, nil))
			}
			want := `{ "a" : null }`
			if f.typ == "JSONB" {
				want = `{"a": null}`
			}
			// Root's pinned PG18.0 oracle also omits converted JSON null
			// in the binary strict family; text JSON strict retains it.
			if f.name == "jsonb_object_agg_strict" {
				want = `{}`
			}
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"('a','null'::json) AS v", f.typ, want))
			jsonArrayAggregateError(t, p, b, "SELECT "+f.name+"(NULL,NULL)", jsonObjectAggregateNullKey(f.typ), false)
		})
	}
}
