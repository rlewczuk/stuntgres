package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONObjectUniqueAggregateMetadata(t *testing.T) {
	jsonObjectUniqueAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonObjectUniqueAggregateSetup(t, p, b)
		for _, f := range jsonObjectUniqueAggregateFamilies {
			for _, alias := range []bool{false, true} {
				for _, mode := range []string{"populated", "global-empty", "grouped-empty", "filter-empty", "all-omitted"} {
					t.Run(f.name+"/"+mode+map[bool]string{false: "/unaliased", true: "/aliased"}[alias], func(t *testing.T) {
						expr, name, suffix := f.name+"(k,v ORDER BY id)", f.name, " WHERE id<3"
						populated := `{ "b" : 1, "a" : 2 }`
						if f.typ == "JSONB" {
							populated = `{"a": 2, "b": 1}`
						}
						want := [][]any{{populated}}
						switch mode {
						case "global-empty":
							suffix, want = " WHERE FALSE", [][]any{{nil}}
						case "grouped-empty":
							suffix, want = " WHERE FALSE GROUP BY g", nil
						case "filter-empty":
							expr += " FILTER(WHERE FALSE)"
							want = [][]any{{nil}}
						case "all-omitted":
							expr = f.name + "('a',v ORDER BY id)"
							suffix = " WHERE id=3"
							want = [][]any{{f.nullValue}}
						}
						if alias {
							expr += " AS chosen"
							name = "chosen"
						}
						s := fixture.Select("SELECT "+expr+" FROM jou_input"+suffix, []string{name}, []string{f.typ}, want)
						s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonArrayAggregateMetadata(t, r, b.PG, s.Columns, s.Types, nil) }
						jsonArrayAggregateRoutes(t, p, b, s)
					})
				}
			}
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE TABLE jou_required(k text NOT NULL,v int NOT NULL)", 0),
			fixture.Exec("INSERT INTO jou_required VALUES('a',1)", 1),
			fixture.Exec("CREATE DOMAIN jou_meta_key AS text", 0),
			fixture.Exec("CREATE DOMAIN jou_meta_value AS int", 0),
			fixture.Exec("CREATE VIEW jou_meta AS SELECT json_object_agg_unique(k,v ORDER BY id) AS j,jsonb_object_agg_unique(k,v ORDER BY id) AS b,json_object_agg_unique_strict(k,v ORDER BY id) AS s,jsonb_object_agg_unique_strict(k,v ORDER BY id) AS t FROM jou_input WHERE id<3", 0),
		} {
			fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s)
		}
		for _, f := range jsonObjectUniqueAggregateFamilies {
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"(k,v) AS v FROM jou_required", f.typ, f.singleton))
			jsonArrayAggregateRoutes(t, p, b, jsonArrayAggregateSelect("SELECT "+f.name+"('a'::jou_meta_key,1::jou_meta_value) AS v", f.typ, f.singleton))
		}
		s := fixture.Select("SELECT j,b,s,t FROM jou_meta", []string{"j", "b", "s", "t"}, []string{"JSON", "JSONB", "JSON", "JSONB"}, [][]any{{`{ "b" : 1, "a" : 2 }`, `{"a": 2, "b": 1}`, `{ "b" : 1, "a" : 2 }`, `{"a": 2, "b": 1}`}})
		s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonArrayAggregateViewMetadata(t, r, b.PG, s.Columns, s.Types, nil) }
		jsonArrayAggregateRoutes(t, p, b, s)
	})
}
