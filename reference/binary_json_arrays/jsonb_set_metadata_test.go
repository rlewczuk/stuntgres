package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBSetMetadata(t *testing.T) {
	jsonbSetScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbs_meta(id int,v jsonb)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jbs_meta VALUES(1,'{"a":1}'),(2,NULL)`, 2))
		for _, f := range []string{"jsonb_set", "jsonb_set_lax"} {
			for _, tc := range []struct {
				suffix string
				want   [][]any
			}{
				{" ORDER BY id", [][]any{{`{"a": 2}`}, {nil}}}, {" WHERE id=2", [][]any{{nil}}}, {" WHERE id=99", nil},
			} {
				q := "SELECT " + f + "(v,'{a}','2') AS changed FROM jbs_meta" + tc.suffix
				s := fixture.JSONBSetMetadataStep(fixture.Select(q, []string{"changed"}, []string{"JSONB"}, tc.want), b.PG)
				jsonbSetBoth(t, p, b, c, s)
				stmt, err := c.PrepareContext(p.Ctx, q)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}, b.Recorder, s)
				stmt.Close()
			}
		}
	})
}
