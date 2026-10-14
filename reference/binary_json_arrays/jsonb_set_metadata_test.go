package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonbSetMetadataStep(s fixture.Step, pg bool) fixture.Step {
	s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonbObjectMetadata(t, r, pg, "changed") }
	s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
		want := s.Rows[i][0]
		var raw any
		if err := r.Scan(&raw); err != nil {
			t.Fatal(err)
		}
		fixture.AssertRawValue(t, pg, "JSONB", raw)
		if !reflect.DeepEqual(fixture.Normalize(raw), want) {
			t.Fatalf("raw=%#v want=%#v", raw, want)
		}
		var n sql.NullString
		if err := r.Scan(&n); err != nil {
			t.Fatal(err)
		}
		if n.Valid != (want != nil) || (n.Valid && n.String != want) {
			t.Fatalf("NullString=%#v want=%#v", n, want)
		}
		var data []byte
		if err := r.Scan(&data); err != nil {
			t.Fatal(err)
		}
		if want == nil {
			if data != nil {
				t.Fatal(data)
			}
			return
		}
		if string(data) != want {
			t.Fatalf("bytes=%q want=%q", data, want)
		}
		data[0] = 'x'
		var text string
		if err := r.Scan(&text); err != nil || text != want {
			t.Fatalf("detached text=%q want=%q err=%v", text, want, err)
		}
	}
	return s
}

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
				s := jsonbSetMetadataStep(fixture.Select(q, []string{"changed"}, []string{"JSONB"}, tc.want), b.PG)
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
