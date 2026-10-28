package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachValues(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			t.Run(f, func(t *testing.T) {
				const input = `{"zz":0,"b":1,"aa":2,"é":3,"中":4,"":5,"a":6,"\u0061":7}`
				ordered := fixture.Select("SELECT key,value,n FROM "+f+"($1) WITH ORDINALITY AS j(key,value,n) ORDER BY n", []string{"key", "value", "n"}, []string{"TEXT", jsonbEachType(f), "INT8"}, [][]any{{"", "5", int64(1)}, {"a", "7", int64(2)}, {"b", "1", int64(3)}, {"aa", "2", int64(4)}, {"zz", "0", int64(5)}, {"é", "3", int64(6)}, {"中", "4", int64(7)}}, input)
				fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(ordered, b.PG))
				const mixed = `{"n":null,"s":"null","t":true,"f":false,"x":"x\n","e":"","d":1.00,"q":1.230e-5,"a":[1,{"z":0,"a":2}],"o":{"zz":0,"a":1}}`
				rows := [][]any{{"n", "null"}, {"s", `"null"`}, {"t", "true"}, {"f", "false"}, {"x", `"x\n"`}, {"e", `""`}, {"d", "1.00"}, {"q", "0.00001230"}, {"a", `[1, {"a": 2, "z": 0}]`}, {"o", `{"a": 1, "zz": 0}`}}
				if f == "jsonb_each_text" {
					rows[0][1], rows[1][1], rows[4][1], rows[5][1] = nil, "null", "x\n", ""
				}
				fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, "SELECT * FROM "+f+"($1::jsonb)", rows, mixed))
				fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, "SELECT * FROM "+f+"('"+mixed+"'::jsonb)", rows))
				for _, empty := range []any{`{}`, nil, []byte(nil)} {
					fixture.JSONEachBoth(t, p, b, c, jsonEachWithMetadata(jsonbEachSelect(f, "SELECT * FROM "+f+"($1)", nil, empty), b.PG))
				}
				long := strings.Repeat("k", 1024)
				keyRows := [][]any{{"", "0"}, {"\n", "1"}, {"\"", "2"}, {"\\", "3"}, {long, `{"nested": 4}`}}
				fixture.JSONEachBoth(t, p, b, c, jsonbEachSelect(f, "SELECT * FROM "+f+"($1)", keyRows, `{"":0,"\n":1,"\"":2,"\\":3,"`+long+`":{"nested":4}}`))
				for _, prepared := range []bool{false, true} {
					s := fixture.Exec("SELECT * FROM "+f+"($1)", 7, input)
					s.Prepared = prepared
					fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
				}
			})
		}
	})
}
