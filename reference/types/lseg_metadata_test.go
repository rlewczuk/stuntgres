package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLsegMetadata(t *testing.T) {
	lsegScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE TABLE lseg_meta(id integer NOT NULL,s lseg NOT NULL,n lseg)", 0))
		run(fixture.Exec("INSERT INTO lseg_meta VALUES(1,'1,4,3,2',NULL),(2,'3,2,1,4','0,0,0,0')", 2))
		for _, tc := range []struct {
			q    string
			rows [][]any
		}{
			{"SELECT s,n FROM lseg_meta ORDER BY id", [][]any{{"[(1,4),(3,2)]", nil}, {"[(3,2),(1,4)]", "[(0,0),(0,0)]"}}},
			{"SELECT s,n FROM lseg_meta WHERE FALSE", nil},
		} {
			s := boxSelect(b, tc.q, []string{"s", "n"}, []string{"LSEG", "LSEG"}, tc.rows)
			s.CheckMetadata = func(t *testing.T, rows *sql.Rows) {
				fixture.BoxMetadata(t, b.PG, rows, []string{"LSEG", "LSEG"}, []bool{false, true})
			}
			run(s)
			run(fixture.Prepared(s))
			stmt, err := c.PrepareContext(p.Ctx, tc.q)
			if err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: stmt, SQL: tc.q}, b.Recorder, s)
			stmt.Close()
		}
		for _, prepared := range []bool{false, true} {
			for _, q := range []string{"SELECT NULL::lseg AS s", "SELECT s AS s FROM lseg_meta WHERE FALSE"} {
				want := [][]any{{nil}}
				if q != "SELECT NULL::lseg AS s" {
					want = nil
				}
				s := boxSelect(b, q, []string{"s"}, []string{"LSEG"}, want)
				s.Prepared = prepared
				run(s)
			}
			for _, arg := range []any{"", []byte{}} {
				d := fixture.ControlsReferenceDiagnostic("22P02", "invalid input syntax for type lseg: \"\"")
				d.Context = "unnamed portal parameter $1 = ''"
				s := fixture.ErrorDiagnostic("SELECT $1::lseg", d, arg)
				s.Prepared, s.SelectRows, s.ErrorStage = prepared, true, "execute"
				run(s)
			}
		}
		q := "SELECT oid::bigint,typname,typlen,typbyval,typalign,typstorage,typtype,typcategory,typispreferred,typcollation::bigint,typelem::bigint,typarray::bigint,typsubscript::oid::bigint,typinput::oid::bigint,typoutput::oid::bigint,typreceive::oid::bigint,typsend::oid::bigint,typdelim FROM pg_type WHERE oid=601"
		run(fixture.Select(q, []string{"oid", "typname", "typlen", "typbyval", "typalign", "typstorage", "typtype", "typcategory", "typispreferred", "typcollation", "typelem", "typarray", "typsubscript", "typinput", "typoutput", "typreceive", "typsend", "typdelim"}, []string{"INT8", "NAME", "INT2", "BOOL", "CHAR", "CHAR", "CHAR", "CHAR", "BOOL", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "INT8", "CHAR"}, [][]any{{int64(601), "lseg", int64(32), false, "d", "p", "b", "G", false, int64(0), int64(600), int64(1018), int64(6180), int64(119), int64(120), int64(2480), int64(2481), ","}}))
		run(fixture.Select("SELECT attname,atttypid::bigint,atttypmod,attcollation::bigint,attstorage FROM pg_attribute WHERE attrelid='lseg_meta'::regclass AND attnum>0 ORDER BY attnum", []string{"attname", "atttypid", "atttypmod", "attcollation", "attstorage"}, []string{"NAME", "INT8", "INT4", "INT8", "CHAR"}, [][]any{{"id", int64(23), int64(-1), int64(0), "p"}, {"s", int64(601), int64(-1), int64(0), "p"}, {"n", int64(601), int64(-1), int64(0), "p"}}))
	})
}
