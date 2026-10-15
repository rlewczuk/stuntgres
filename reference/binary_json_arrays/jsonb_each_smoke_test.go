package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBEachFeatureSmoke(t *testing.T) {
	jsonbEachScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE jbeach_smoke(id int,j jsonb)", 0))
		insertSQL := "INSERT INTO jbeach_smoke VALUES($1,$2),($3,$4),($5,$6)"
		insert, err := c.PrepareContext(p.Ctx, insertSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer insert.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: insert, SQL: insertSQL}, b.Recorder, fixture.Exec(insertSQL, 3, 1, `{"zz":0,"a":1,"a":null,"b":"x","o":{"z":0,"a":2}}`, 2, `{}`, 3, nil))
		insert.Close()
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			n, x := any("null"), any(`"x"`)
			record := "(n,null)"
			if f == "jsonb_each_text" {
				n, x, record = nil, "x", "(n,)"
			}
			q := "SELECT f.key,f.value,n FROM jbeach_smoke s," + f + "(s.j) WITH ORDINALITY AS f(key,value,n) WHERE s.id=$1 ORDER BY n"
			jsonEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(q, []string{"key", "value", "n"}, []string{"TEXT", jsonbEachType(f), "INT8"}, [][]any{{"a", n, int64(1)}, {"b", x, int64(2)}, {"o", `{"a": 2, "z": 0}`, int64(3)}, {"zz", "0", int64(4)}}, 1), b.PG))
			for _, id := range []int{2, 3} {
				jsonEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select(q, []string{"key", "value", "n"}, []string{"TEXT", jsonbEachType(f), "INT8"}, nil, id), b.PG))
			}
			jsonEachBoth(t, p, b, c, jsonEachWithMetadata(fixture.Select("SELECT "+f+"('{\"n\":null}')", []string{f}, []string{"RECORD"}, [][]any{{record}}), b.PG))
			jsonEachBoth(t, p, b, c, fixture.Select("SELECT * FROM ROWS FROM("+f+"('{\"n\":null}'),generate_series(7::int,8::int)) WITH ORDINALITY AS r(k,v,g,n) ORDER BY n", []string{"k", "v", "g", "n"}, []string{"TEXT", jsonbEachType(f), "INT4", "INT8"}, [][]any{{"n", n, int64(7), int64(1)}, {nil, nil, int64(8), int64(2)}}))
			jsonEachBoth(t, p, b, c, fixture.Select("SELECT s.id,f.key FROM jbeach_smoke s LEFT JOIN LATERAL "+f+"(s.j) f ON true ORDER BY s.id,f.key", []string{"id", "key"}, []string{"INT4", "TEXT"}, [][]any{{int64(1), "a"}, {int64(1), "b"}, {int64(1), "o"}, {int64(1), "zz"}, {int64(2), nil}, {int64(3), nil}}))
		}
		jsonbEachHeld(t, p, b, c)
		jsonbEachAtomic(t, p, b, c)
		run(fixture.Exec(`INSERT INTO jbeach_smoke VALUES(4,'[]')`, 1))
		for _, f := range []string{"jsonb_each", "jsonb_each_text"} {
			jsonEachBoth(t, p, b, c, fixture.Select("SELECT f.key FROM jbeach_smoke s,"+f+"(s.j) f WHERE s.id=$1 ORDER BY f.key", []string{"key"}, []string{"TEXT"}, [][]any{{"a"}, {"b"}, {"o"}, {"zz"}}, 1))
		}
		checks := jsonbEachStored(t, p, b, c)
		jsonbEachControls(t, p, b, c, checks)
	})
}
