package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONEachFeatureSmoke(t *testing.T) {
	jsonEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// The stored scenario supplies CREATE0, prepared INSERT3, empty/NULL
		// sources, both concrete-member views, CTAS counts and rename/remap.
		checks := jsonEachStored(t, p, b, c)
		for _, f := range []string{"json_each", "json_each_text"} {
			n, x := any("null"), any(`"x"`)
			if f == "json_each_text" {
				n, x = nil, "x"
			}
			q := "SELECT f.key,f.value,n FROM je_documents s," + f + "(s.document) WITH ORDINALITY AS f(key,value,n) WHERE s.id=$1 ORDER BY n"
			fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(q, []string{"key", "value", "n"}, []string{"TEXT", jsonEachType(f), "INT8"}, [][]any{{"a", "1", int64(1)}, {"a", n, int64(2)}, {"b", x, int64(3)}}, 1), b.PG))
			for _, id := range []int{2, 3} {
				fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(q, []string{"key", "value", "n"}, []string{"TEXT", jsonEachType(f), "INT8"}, nil, id), b.PG))
			}
			fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select("SELECT "+f+"('{\"a\":1}')", []string{f}, []string{"RECORD"}, [][]any{{"(a,1)"}}), b.PG))
			fixture.JSONEachBoth(t, p, b, c, fixture.Select("SELECT * FROM ROWS FROM("+f+"('{\"a\":1}'),generate_series(7::int,8::int)) WITH ORDINALITY AS r(k,v,g,n) ORDER BY n", []string{"k", "v", "g", "n"}, []string{"TEXT", jsonEachType(f), "INT4", "INT8"}, [][]any{{"a", "1", int64(7), int64(1)}, {nil, nil, int64(8), int64(2)}}))
		}
		jsonEachHeld(t, p, b, c)
		jsonEachControls(t, p, b, c, checks)
	})
}
