package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONBObjectMetadata(t *testing.T) {
	jsonbObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jbo_meta(a text[])", 0))
		for _, tc := range []struct {
			q, name string
			want    [][]any
		}{
			{`SELECT jsonb_object('{a,1}')`, "jsonb_object", [][]any{{`{"a": "1"}`}}},
			{`SELECT jsonb_object(NULL::text[]) AS v`, "v", [][]any{{nil}}},
			{`SELECT jsonb_object('{a}',ARRAY[NULL]::text[]) AS v`, "v", [][]any{{`{"a": null}`}}},
			{`SELECT jsonb_object(a) AS v FROM jbo_meta`, "v", nil},
			{`SELECT jsonb_object('{}'::text[],'{}'::text[]) AS v`, "v", [][]any{{`{}`}}},
			{`SELECT jsonb_object(k,v) AS v FROM (SELECT '{a}'::text[] AS k,'{1}'::text[] AS v) AS x`, "v", [][]any{{`{"a": "1"}`}}},
			{`SELECT jsonb_object(a) AS v FROM (SELECT NULL::text[] AS a) AS x`, "v", [][]any{{nil}}},
		} {
			s := fixture.Select(tc.q, []string{tc.name}, []string{"JSONB"}, tc.want)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { fixture.JSONBObjectMetadata(t, r, b.PG, tc.name) }
			s.CheckScan = func(t *testing.T, r *sql.Rows, i int) {
				want := tc.want[i][0]
				var raw any
				if err := r.Scan(&raw); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					if raw != nil {
						t.Fatalf("NULL raw=%T %v", raw, raw)
					}
				} else if b.PG {
					if _, ok := raw.([]byte); !ok {
						t.Fatalf("pq raw=%T", raw)
					}
				} else if _, ok := raw.(string); !ok {
					t.Fatalf("native raw=%T", raw)
				}
				var n sql.NullString
				if err := r.Scan(&n); err != nil {
					t.Fatal(err)
				}
				if n.Valid != (want != nil) || (n.Valid && n.String != want) {
					t.Fatalf("NullString=%#v want=%v", n, want)
				}
				var bytes []byte
				if err := r.Scan(&bytes); err != nil {
					t.Fatal(err)
				}
				if want == nil {
					if bytes != nil {
						t.Fatal(bytes)
					}
					return
				}
				if string(bytes) != want {
					t.Fatalf("bytes=%q want=%q", bytes, want)
				}
				bytes[0] = 'x'
				var again string
				if err := r.Scan(&again); err != nil || again != want {
					t.Fatalf("detached rescan=%q err=%v", again, err)
				}
			}
			jsonObjectBoth(t, p, b, c, s)
		}
	})
}
