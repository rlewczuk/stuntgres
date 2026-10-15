package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONStripNullsFullJoin(t *testing.T) {
	jsonStripScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_left(id int,v json,flag bool)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec("CREATE TABLE js_right(id int,doc text)", 0))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO js_left VALUES(1,'{"drop":null}',true),(2,'{"keep":1}',false)`, 2))
		jsonTypeRun(t, p, b, c, fixture.Exec(`INSERT INTO js_right VALUES(10,'{}'),(20,'{"other":2}')`, 2))
		for _, expression := range []string{"json_strip_nulls(l.v,l.flag)", "json_strip_nulls(strip_in_arrays => l.flag,target => l.v)"} {
			query := "SELECT l.id AS left_id,r.id AS right_id FROM js_left AS l FULL JOIN js_right AS r ON " + expression + "::text = r.doc ORDER BY left_id NULLS LAST,right_id"
			jsonTypeBoth(t, p, b, c, fixture.Select(query, []string{"left_id", "right_id"}, []string{"INT4", "INT4"}, [][]any{{int64(1), int64(10)}, {int64(2), nil}, {nil, int64(20)}}))
		}
	})
}
