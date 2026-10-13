package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func jsonObjectMetadata(t *testing.T, r *sql.Rows, pg bool, name string) {
	t.Helper()
	cs, err := r.ColumnTypes()
	if err != nil {
		t.Fatal(err)
	}
	if len(cs) != 1 {
		t.Fatal(cs)
	}
	ct := cs[0]
	scan := reflect.TypeFor[string]()
	if pg {
		scan = reflect.TypeFor[any]()
	}
	if ct.Name() != name || ct.DatabaseTypeName() != "JSON" || ct.ScanType() != scan {
		t.Fatalf("metadata=%q/%q/%v want=%q/JSON/%v", ct.Name(), ct.DatabaseTypeName(), ct.ScanType(), name, scan)
	}
	if n, ok := ct.Length(); ok {
		t.Fatalf("unexpected length %d", n)
	}
	if p, s, ok := ct.DecimalSize(); ok {
		t.Fatalf("unexpected decimal %d,%d", p, s)
	}
	if n, ok := ct.Nullable(); n || ok {
		t.Fatalf("computed nullable=%v,%v want false,false", n, ok)
	}
}

func TestReferenceJSONObjectMetadata(t *testing.T) {
	jsonObjectScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE TABLE jo_meta(a text[])", 0))
		for _, tc := range []struct {
			q, name string
			want    [][]any
		}{
			{`SELECT json_object('{a,1}')`, "json_object", [][]any{{`{"a" : "1"}`}}},
			{`SELECT json_object(NULL::text[]) AS v`, "v", [][]any{{nil}}},
			{`SELECT json_object('{a}',ARRAY[NULL]::text[]) AS v`, "v", [][]any{{`{"a" : null}`}}},
			{`SELECT json_object(a) AS v FROM jo_meta`, "v", nil},
		} {
			s := fixture.Select(tc.q, []string{tc.name}, []string{"JSON"}, tc.want)
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) { jsonObjectMetadata(t, r, b.PG, tc.name) }
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
