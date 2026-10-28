package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceJSONBInsertMetadata(t *testing.T) {
	jsonbInsertScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbi_meta(id int,v jsonb)", 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO jbi_meta VALUES(1,'[0]'),(2,NULL)", 2))
		q := "SELECT jsonb_insert(v,'{0}','9') AS changed FROM jbi_meta WHERE id >= $1 AND id <= $2 ORDER BY id"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		for range 2 {
			for _, tc := range []struct {
				lo, hi int
				want   [][]any
			}{
				{1, 2, [][]any{{"[9, 0]"}, {nil}}}, {2, 2, [][]any{{nil}}}, {99, 99, nil},
			} {
				s := fixture.JSONBSetMetadataStep(fixture.Select(q, []string{"changed"}, []string{"JSONB"}, tc.want, tc.lo, tc.hi), b.PG)
				jsonbSetBoth(t, p, b, c, s)
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		for _, tc := range []struct {
			suffix string
			want   [][]any
		}{
			{" ORDER BY id", [][]any{{"[9, 0]"}, {nil}}}, {" WHERE id=2", [][]any{{nil}}}, {" WHERE id=99", nil},
		} {
			s := fixture.Select("SELECT jsonb_insert(v,'{0}','9') FROM jbi_meta"+tc.suffix, []string{"jsonb_insert"}, []string{"JSONB"}, tc.want)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				fixture.JSONBObjectMetadata(t, r, b.PG, "jsonb_insert")
				fixture.JSONBObjectMetadata(t, r, b.PG, "jsonb_insert")
			}
			jsonbSetBoth(t, p, b, c, s)
		}
		jsonbSetBoth(t, p, b, c, fixture.Select("SELECT id,v FROM jbi_meta ORDER BY id", []string{"id", "v"}, []string{"INT4", "JSONB"}, [][]any{{int64(1), "[0]"}, {int64(2), nil}}))
	})
}
