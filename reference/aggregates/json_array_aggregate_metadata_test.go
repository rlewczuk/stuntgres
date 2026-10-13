package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONArrayAggregateMetadata(t *testing.T) {
	jsonArrayAggregateScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		jsonArrayAggregateSetup(t, p, b)
		for _, tc := range []struct{ expr, name, typ string; width int64; value any }{
			{"json_agg(v ORDER BY id)", "json_agg", "JSON", -1, "[2, null, 1, null]"},
			{"jsonb_agg(v ORDER BY id)", "jsonb_agg", "JSONB", -1, "[2, null, 1, null]"},
			{"json_agg_strict(v ORDER BY id)", "json_agg_strict", "JSON", -1, "[2, 1]"},
			{"jsonb_agg_strict(v ORDER BY id)", "jsonb_agg_strict", "JSONB", -1, "[2, 1]"},
			{"json_arrayagg(v ORDER BY id)", "json_arrayagg", "JSON", -1, "[2, 1]"},
			{"json_arrayagg(v ORDER BY id RETURNING jsonb)", "json_arrayagg", "JSONB", -1, "[2, 1]"},
			{"json_arrayagg(v ORDER BY id RETURNING text)", "json_arrayagg", "TEXT", -1, "[2, 1]"},
			{"json_arrayagg(v ORDER BY id RETURNING bytea)", "json_arrayagg", "BYTEA", -1, "[2, 1]"},
			{"json_arrayagg(v ORDER BY id RETURNING varchar(20))", "json_arrayagg", "VARCHAR", 20, "[2, 1]"},
			{"json_arrayagg(v ORDER BY id RETURNING char(8))", "json_arrayagg", "BPCHAR", 8, "[2, 1]  "},
		} {
			for _, alias := range []bool{false, true} {
				for _, mode := range []string{"populated", "global-empty", "grouped-empty", "filter-empty"} {
					t.Run(tc.expr+"/"+mode+map[bool]string{false:"/unaliased",true:"/aliased"}[alias], func(t *testing.T) {
						expr, name, suffix := tc.expr, tc.name, ""
						want := [][]any{{tc.value}}
						switch mode {
						case "global-empty": suffix, want = " WHERE FALSE", [][]any{{nil}}
						case "grouped-empty": suffix, want = " WHERE FALSE GROUP BY g", nil
						case "filter-empty": expr += " FILTER(WHERE FALSE)"; want = [][]any{{nil}}
						}
						if alias { expr += " AS chosen"; name = "chosen" }
						s := fixture.Select("SELECT "+expr+" FROM ja_input"+suffix, []string{name}, []string{tc.typ}, want)
						s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonArrayAggregateMetadata(t, rows, b.PG, s.Columns, s.Types, []int64{tc.width}) }
						jsonArrayAggregateRoutes(t, p, b, s)
					})
				}
			}
		}
		for _, s := range []fixture.Step{
			fixture.Exec("CREATE DOMAIN ja_meta_jsonb AS jsonb", 0),
			fixture.Exec("CREATE DOMAIN ja_meta_text AS varchar(3)", 0),
			fixture.Exec("CREATE TABLE ja_required(v int NOT NULL)", 0),
			fixture.Exec("INSERT INTO ja_required VALUES(1)", 1),
		} { fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, s) }
		for _, tc := range []struct{ q, typ string; width int64 }{
			{"SELECT json_agg(v) AS v FROM ja_required", "JSON", -1},
			{"SELECT json_arrayagg(v RETURNING ja_meta_jsonb) AS v FROM ja_required", "JSONB", -1},
			{"SELECT json_arrayagg(v RETURNING ja_meta_text) AS v FROM ja_required", "VARCHAR", 3},
		} {
			t.Run(tc.q, func(t *testing.T) {
				s := jsonArrayAggregateSelect(tc.q, tc.typ, "[1]")
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { jsonArrayAggregateMetadata(t, rows, b.PG, s.Columns, s.Types, []int64{tc.width}) }
				jsonArrayAggregateRoutes(t, p, b, s)
			})
		}
		fixture.RunStep(t, p.Ctx, b.DB, b.Recorder, fixture.Exec("CREATE VIEW ja_meta AS SELECT json_agg(v ORDER BY id) AS j,jsonb_agg(v ORDER BY id) AS b,json_agg_strict(v ORDER BY id) AS js,jsonb_agg_strict(v ORDER BY id) AS bs,json_arrayagg(v ORDER BY id) AS a FROM ja_input", 0))
		s:=fixture.Select("SELECT j,b,js,bs,a FROM ja_meta",[]string{"j","b","js","bs","a"},[]string{"JSON","JSONB","JSON","JSONB","JSON"},[][]any{{"[2, null, 1, null]","[2, null, 1, null]","[2, 1]","[2, 1]","[2, 1]"}})
		s.CheckMetadata=func(t *testing.T,rows *sql.Rows){jsonArrayAggregateViewMetadata(t,rows,b.PG,s.Columns,s.Types,nil)}
		jsonArrayAggregateRoutes(t,p,b,s)
		// Stable result OIDs/typmods/noncollation and zero origin are checked by
		// private descriptor units, not by inventing executable catalog relations.
	})
}
