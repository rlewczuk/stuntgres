package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePointMetadata(t *testing.T) {
	pointScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE point_meta(id integer NOT NULL,p point NOT NULL,n point)", 0))
		run(fixture.Exec("INSERT INTO point_meta VALUES(1,'1,2',NULL)", 1))
		for _, empty := range []bool{false, true} {
			q := "SELECT p,n FROM point_meta"
			want := [][]any{{"(1,2)", nil}}
			if empty {
				q += " WHERE FALSE"
				want = nil
			}
			s := boxSelect(b, q, []string{"p", "n"}, []string{"POINT", "POINT"}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				boxMetadata(t, b.PG, rows, []string{"POINT", "POINT"}, []bool{false, true})
			}
			run(s)
			run(fixture.Prepared(s))
			q = "SELECT p[0] AS x,n[1] AS y FROM point_meta"
			want = [][]any{{float64(1), nil}}
			if empty {
				q += " WHERE FALSE"
				want = nil
			}
			s = fixture.Select(q, []string{"x", "y"}, []string{"FLOAT8", "FLOAT8"}, want)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				cols, err := rows.ColumnTypes()
				if err != nil {
					t.Fatal(err)
				}
				for _, col := range cols {
					if col.ScanType() != reflect.TypeFor[float64]() {
						t.Fatalf("coordinate ScanType %v", col.ScanType())
					}
					if n, ok := col.Length(); n != 0 || ok {
						t.Fatalf("coordinate length %d/%t", n, ok)
					}
					if precision, scale, ok := col.DecimalSize(); precision != 0 || scale != 0 || ok {
						t.Fatalf("coordinate precision %d,%d/%t", precision, scale, ok)
					}
				}
			}
			run(s)
			run(fixture.Prepared(s))
		}
		for _, q := range []string{"SELECT point '1,2' AS p", "SELECT NULL::point AS p", "SELECT $1::point AS p"} {
			want := any("(1,2)")
			var args []any
			if q == "SELECT NULL::point AS p" {
				want = nil
			}
			if q == "SELECT $1::point AS p" {
				args = []any{"1,2"}
			}
			s := boxSelect(b, q, []string{"p"}, []string{"POINT"}, [][]any{{want}}, args...)
			run(s)
			run(fixture.Prepared(s))
		}
		q := "SELECT oid::bigint,typname,typlen,typbyval,typalign,typstorage,typtype,typcategory,typispreferred,typcollation::bigint,typelem::bigint,typarray::bigint,typsubscript::oid::bigint,typinput::oid::bigint,typoutput::oid::bigint,typreceive::oid::bigint,typsend::oid::bigint,typdelim FROM pg_type WHERE oid=600"
		run(fixture.Select(q, []string{"oid", "typname", "typlen", "typbyval", "typalign", "typstorage", "typtype", "typcategory", "typispreferred", "typcollation", "typelem", "typarray", "typsubscript", "typinput", "typoutput", "typreceive", "typsend", "typdelim"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "CHAR"}, [][]any{{int64(600), "point", int64(16), false, "d", "p", "b", "G", false, int64(0), int64(701), int64(1017), int64(6180), int64(117), int64(118), int64(2428), int64(2429), ","}}))
		run(fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint,attstorage FROM pg_attribute WHERE attrelid='point_meta'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation", "attstorage"}, []string{"NAME", "INT8", "INT4", "INT8", "CHAR"}, [][]any{{"id", int64(23), int64(-1), int64(0), "p"}, {"p", int64(600), int64(-1), int64(0), "p"}, {"n", int64(600), int64(-1), int64(0), "p"}}))
	})
}
