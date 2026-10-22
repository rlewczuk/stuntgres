package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLineMetadata(t *testing.T) {
	lineScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE line_meta(l line NOT NULL,n line)", 0))
		run(fixture.Exec("INSERT INTO line_meta VALUES('{2,4,6}',NULL)", 1))
		for _, prepared := range []bool{false, true} {
			for _, empty := range []bool{false, true} {
				q := "SELECT l,n FROM line_meta"
				want := [][]any{{"{2,4,6}", nil}}
				if empty {
					q += " WHERE FALSE"
					want = nil
				}
				s := lineSelect(b, q, []string{"l", "n"}, []string{"LINE", "LINE"}, want)
				s.Prepared = prepared
				s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
					lineMetadata(t, b.PG, rows, []string{"LINE", "LINE"}, []bool{false, true})
				}
				run(s)
			}
			for _, arg := range []any{nil, []byte(nil)} {
				s := lineSelect(b, "SELECT $1::line AS l,NULL::line AS n", []string{"l", "n"}, []string{"LINE", "LINE"}, [][]any{{nil, nil}}, arg)
				s.Prepared = prepared
				run(s)
			}
		}
		run(fixture.Select("SELECT oid::bigint,typname,typlen,typbyval,typalign,typstorage,typtype,typcategory,typispreferred,typcollation::bigint,typelem::bigint,typarray::bigint,typsubscript::oid::bigint,typinput::oid::bigint,typoutput::oid::bigint,typreceive::oid::bigint,typsend::oid::bigint,typdelim FROM pg_type WHERE oid=628", []string{"oid", "typname", "typlen", "typbyval", "typalign", "typstorage", "typtype", "typcategory", "typispreferred", "typcollation", "typelem", "typarray", "typsubscript", "typinput", "typoutput", "typreceive", "typsend", "typdelim"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "CHAR"}, [][]any{{int64(628), "line", int64(24), false, "d", "p", "b", "G", false, int64(0), int64(701), int64(629), int64(6180), int64(1490), int64(1491), int64(2488), int64(2489), ","}}))
		run(fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint,attstorage FROM pg_attribute WHERE attrelid='line_meta'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation", "attstorage"}, []string{"NAME", "INT8", "INT4", "INT8", "CHAR"}, [][]any{{"l", int64(628), int64(-1), int64(0), "p"}, {"n", int64(628), int64(-1), int64(0), "p"}}))
	})
}
