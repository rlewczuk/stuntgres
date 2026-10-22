package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceBoxMetadata(t *testing.T) {
	boxScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE box_meta(id integer NOT NULL,b box NOT NULL,p point)", 0))
		run(fixture.Exec("INSERT INTO box_meta VALUES(1,'1,4,3,2','(3,4)'),(2,'0,0,0,0',NULL)", 2))
		for _, prepared := range []bool{false, true} {
			for _, tc := range []struct {
				query string
				rows  [][]any
			}{
				{"SELECT b,p FROM box_meta ORDER BY id", [][]any{{"(3,4),(1,2)", "(3,4)"}, {"(0,0),(0,0)", nil}}},
				{"SELECT b,p FROM box_meta WHERE FALSE", nil},
			} {
				s := boxSelect(b, tc.query, []string{"b", "p"}, []string{"BOX", "POINT"}, tc.rows)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) { boxMetadata(t, b.PG, rows, s.Types, []bool{false, true}) }
				run(s)
			}
			for _, tc := range []struct {
				query string
				args  []any
				rows  [][]any
			}{
				{"SELECT NULL::box AS b,NULL::point AS p", nil, [][]any{{nil, nil}}},
				{"SELECT (NULL::box) AS b,(NULL::box)[0] AS p", nil, [][]any{{nil, nil}}},
				{"SELECT $1::box AS b,$2::point AS p", []any{nil, nil}, [][]any{{nil, nil}}},
				{"SELECT $1::box AS b,$2::point AS p", []any{[]byte(nil), []byte(nil)}, [][]any{{nil, nil}}},
				{"SELECT $1::box AS b,$2::point AS p", []any{[]byte("1,4,3,2"), []byte("(3,4)")}, [][]any{{"(3,4),(1,2)", "(3,4)"}}},
			} {
				s := boxSelect(b, tc.query, []string{"b", "p"}, []string{"BOX", "POINT"}, tc.rows, tc.args...)
				s.Prepared = prepared
				run(s)
			}
			for _, typ := range []string{"box", "point"} {
				for _, arg := range []any{"", []byte{}} {
					d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type "+typ+": \"\"")
					d.Context = "unnamed portal parameter $1 = ''"
					s := fixture.ErrorDiagnostic("SELECT $1::"+typ, d, arg)
					s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
					run(s)
				}
			}
		}
		q := "SELECT oid::bigint,typname,typlen,typbyval,typalign,typstorage,typtype,typcategory,typispreferred,typcollation::bigint,typelem::bigint,typarray::bigint,typsubscript::oid::bigint,typinput::oid::bigint,typoutput::oid::bigint,typdelim FROM pg_type WHERE oid IN (600,603) ORDER BY oid"
		run(fixture.Select(q, []string{"oid", "typname", "typlen", "typbyval", "typalign", "typstorage", "typtype", "typcategory", "typispreferred", "typcollation", "typelem", "typarray", "typsubscript", "typinput", "typoutput", "typdelim"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "CHAR"}, [][]any{
			{int64(600), "point", int64(16), false, "d", "p", "b", "G", false, int64(0), int64(701), int64(1017), int64(6180), int64(117), int64(118), ","},
			{int64(603), "box", int64(32), false, "d", "p", "b", "G", false, int64(0), int64(600), int64(1020), int64(6180), int64(123), int64(124), ";"},
		}))
		run(fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint,attstorage FROM pg_attribute WHERE attrelid='box_meta'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation", "attstorage"}, []string{"NAME", "INT8", "INT4", "INT8", "CHAR"}, [][]any{
			{"id", int64(23), int64(-1), int64(0), "p"}, {"b", int64(603), int64(-1), int64(0), "p"}, {"p", int64(600), int64(-1), int64(0), "p"},
		}))
	})
}
