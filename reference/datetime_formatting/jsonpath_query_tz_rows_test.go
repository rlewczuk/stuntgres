package reference_test

import (
	"database/sql"
	"reflect"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceJSONPathQueryTZMetadata(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, f := range jsonpathQueryTZFunctions {
			for _, mode := range []string{"value", "empty", "null"} {
				q := `SELECT ` + f.name + `('true','$') AS changed`
				want := any(true)
				if f.typ == "JSONB" {
					want = `true`
				}
				if f.name == "jsonb_path_query_array_tz" {
					want = `[true]`
				}
				rows := jsonpathQueryRows(want)
				if mode == "empty" {
					q += ` WHERE false`
					rows = nil
				}
				if mode == "null" {
					q = `SELECT ` + f.name + `(NULL::jsonb,'$') AS changed`
					rows = jsonpathQueryRows(nil)
					if f.set {
						rows = nil
					}
				}
				s := jsonpathQueryResult(q, f.typ, rows)
				s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
					cols, err := r.ColumnTypes()
					if err != nil {
						t.Fatal(err)
					}
					if len(cols) != 1 || cols[0].Name() != "changed" || cols[0].DatabaseTypeName() != f.typ {
						t.Fatalf("metadata before Next: %v", cols)
					}
					if f.typ == "JSONB" {
						fixture.JSONBObjectMetadata(t, r, b.PG, "changed")
						return
					}
					if cols[0].ScanType() != reflect.TypeOf(false) {
						t.Fatalf("BOOL scan type %v", cols[0].ScanType())
					}
					if _, ok := cols[0].Length(); ok {
						t.Fatal("BOOL length exposed")
					}
					if _, _, ok := cols[0].DecimalSize(); ok {
						t.Fatal("BOOL precision exposed")
					}
					if _, ok := cols[0].Nullable(); ok {
						t.Fatal("computed BOOL nullability exposed")
					}
				}
				fixture.JSONPathNavigateBoth(t, p, b, c, s)
			}
		}
		for _, doc := range []string{`[2,3]`, `[]`} {
			rows := [][]any{{`2`, int64(1)}, {`3`, int64(2)}}
			if doc == `[]` {
				rows = nil
			}
			s := fixture.Select(`SELECT * FROM jsonb_path_query_tz($1,'$[*]') WITH ORDINALITY`, []string{"jsonb_path_query_tz", "ordinality"}, []string{"JSONB", "INT8"}, rows, doc)
			s.Bag = true
			s.CheckMetadata = func(t *testing.T, r *sql.Rows) {
				cols, err := r.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				if len(cols) != 2 || cols[1].DatabaseTypeName() != "INT8" || cols[1].ScanType() != reflect.TypeOf(int64(0)) {
					t.Fatalf("ordinality metadata: %v", cols)
				}
			}
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
	})
}

func TestReferenceJSONPathQueryTZFromComposition(t *testing.T) {
	jsonpathQueryScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, s := range []fixture.Step{
			fixture.Select(`SELECT * FROM jsonb_path_query_tz('[2,3,4]','$[*]')`, []string{"jsonb_path_query_tz"}, []string{"JSONB"}, jsonpathQueryRows(`2`, `3`, `4`)),
			fixture.Select(`SELECT * FROM jsonb_path_query_tz('[2,3,4]','$[*]') WITH ORDINALITY AS q(v,n) WHERE v>'2'::jsonb`, []string{"v", "n"}, []string{"JSONB", "INT8"}, [][]any{{`3`, int64(2)}, {`4`, int64(3)}}),
			fixture.Select(`SELECT * FROM ROWS FROM(jsonb_path_query_tz('[2,3,4]','$[*]'),generate_series(10,11)) WITH ORDINALITY AS q(v,g,n)`, []string{"v", "g", "n"}, []string{"JSONB", "INT4", "INT8"}, [][]any{{`2`, int64(10), int64(1)}, {`3`, int64(11), int64(2)}, {`4`, nil, int64(3)}}),
		} {
			s.Bag = true
			fixture.JSONPathNavigateBoth(t, p, b, c, s)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`CREATE TABLE jpqtz_lateral(id int,doc jsonb)`, 0))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(`INSERT INTO jpqtz_lateral VALUES(1,'[2,3]'),(2,'[]'),(3,'[null]')`, 3))
		for _, lateral := range []string{"", "LATERAL "} {
			q := `SELECT d.id,q.v,q.n FROM jpqtz_lateral d LEFT JOIN ` + lateral + `jsonb_path_query_tz(d.doc,'$[*]') WITH ORDINALITY AS q(v,n) ON true ORDER BY d.id,q.n`
			fixture.JSONPathNavigateBoth(t, p, b, c, fixture.Select(q, []string{"id", "v", "n"}, []string{"INT4", "JSONB", "INT8"}, [][]any{{int64(1), `2`, int64(1)}, {int64(1), `3`, int64(2)}, {int64(2), nil, nil}, {int64(3), `null`, int64(1)}}))
		}
	})
}
