package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// TestReferenceGINJSONBStructure witnesses real token/postings structure with
// amcheck in both modes. Pending/main placement and cleanup counts stay in the
// native unit (GinPendingIncidences/CleanGin), which owns logical incidence;
// the public witness here never infers index use from a physical page count.
func TestReferenceGINJSONBStructure(t *testing.T) {
	heapScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE EXTENSION amcheck", 0))
		run(fixture.Exec("CREATE TABLE pjs(id integer,labels jsonb)", 0))
		run(fixture.Exec(`INSERT INTO pjs VALUES
			(1,'{"a":"b","n":1,"arr":[1,2],"o":{"k":"v"}}'::jsonb),
			(2,'{"a":"c","n":1.0,"arr":[3]}'::jsonb),
			(3,'{}'::jsonb),
			(4,NULL),
			(5,'null'::jsonb),
			(6,'[1,2,3]'::jsonb),
			(7,'["a","b"]'::jsonb),
			(8,'{"a":null}'::jsonb),
			(9,'{"x":{"a":1}}'::jsonb),
			(10,'{"a":{"b":1}}'::jsonb)`, 10))
		run(fixture.Exec("CREATE INDEX pjs_def ON pjs USING GIN (labels)", 0))
		run(fixture.Exec("CREATE INDEX pjs_path ON pjs USING GIN (labels jsonb_path_ops)", 0))
		check := func() {
			t.Helper()
			for _, name := range []string{"pjs_def", "pjs_path"} {
				heapBoth(t, p, b, c, amcheckSelect("SELECT gin_index_check('"+name+"') AS ok", false))
			}
		}
		check()
		run(fixture.Exec(`INSERT INTO pjs VALUES(11,'{"a":"b","q":[true]}')`, 1))
		check()
		heapBoth(t, p, b, c, ginIDs(`SELECT id FROM pjs WHERE labels @> '{"a":"b"}' ORDER BY id`, []int64{1, 11}))
		heapBoth(t, p, b, c, ginIDs("SELECT id FROM pjs WHERE labels ? 'q' ORDER BY id", []int64{11}))
		run(fixture.Exec(`UPDATE pjs SET labels='{"a":"z"}' WHERE id=1`, 1))
		check()
		heapBoth(t, p, b, c, ginIDs(`SELECT id FROM pjs WHERE labels @> '{"a":"b"}' ORDER BY id`, []int64{11}))
		run(fixture.Exec("DELETE FROM pjs WHERE id=2", 1))
		check()
		heapBoth(t, p, b, c, ginIDs(`SELECT id FROM pjs WHERE labels @> '{"a":null}' ORDER BY id`, []int64{8}))
	})
}