package reference_test

import (
	"database/sql"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceBitStringFunctionsConsumersControls(t *testing.T) {
	bitStringFunctionsScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		bitStringFunctionsConsumers(t, p, b, c)
		bitStringFunctionsControls(t, p, b, c)
	})
}
func bitStringFunctionsConsumers(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	run(fixture.Exec(`CREATE TABLE bsf_cons(id INTEGER,v VARBIT,r "bit",i INTEGER,n INTEGER)`, 0))
	run(fixture.Exec("INSERT INTO bsf_cons VALUES(1,B'10110',B'00',2,2),(2,B'000',B'1',1,1),(3,NULL,B'1',1,1)", 3))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT id,CASE WHEN length(v)>3 THEN substring(overlay(v PLACING r FROM i FOR n) FROM 2 FOR 2) ELSE substring(v FROM 1) END AS s FROM bsf_cons WHERE bit_count(v)>0 ORDER BY length(v),id", []string{"id", "s"}, []string{"INT4", "BIT"}, [][]any{{int64(1), "00"}}))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT length(v) AS n,count(*) AS c FROM bsf_cons GROUP BY length(v) HAVING length(v)>0 ORDER BY length(v)", []string{"n", "c"}, []string{"INT4", "INT8"}, [][]any{{int64(3), int64(1)}, {int64(5), int64(1)}}))
	run(fixture.Exec("CREATE TABLE bsf_dst(id INTEGER,v VARBIT)", 0))
	for _, prepared := range []bool{false, true} {
		s := fixture.Exec("INSERT INTO bsf_dst SELECT id,substring(v FROM i FOR n) FROM bsf_cons ORDER BY id", 3)
		s.Prepared = prepared
		run(s)
		bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT id,v FROM bsf_dst ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARBIT"}, [][]any{{int64(1), "01"}, {int64(2), "0"}, {int64(3), nil}}))
		s = fixture.Select("UPDATE bsf_dst SET v=overlay(v PLACING B'1' FROM 1 FOR 0) RETURNING id,v", []string{"id", "v"}, []string{"INT4", "VARBIT"}, [][]any{{int64(1), "101"}, {int64(2), "10"}, {int64(3), nil}})
		s.Bag = true
		s.Prepared = prepared
		run(s)
		run(fixture.Exec("DELETE FROM bsf_dst", 3))
	}
	run(fixture.Exec("CREATE TABLE bsf_fixed(v BIT(3))", 0))
	run(fixture.Exec("CREATE TABLE bsf_narrow(v VARBIT(2))", 0))
	for _, prepared := range []bool{false, true} {
		for _, tc := range []struct{ q, code, msg string }{
			{"INSERT INTO bsf_fixed SELECT substring(v FROM 2 FOR 2) FROM bsf_cons WHERE id=1", "22026", "bit string length 2 does not match type bit(3)"},
			{"INSERT INTO bsf_narrow SELECT overlay(v PLACING r FROM i FOR n) FROM bsf_cons WHERE id=1", "22001", "bit string too long for type bit varying(2)"},
		} {
			s := bitStringFunctionsError(tc.q, tc.code, tc.msg, 0, prepared, false)
			s.SelectRows = false
			run(s)
		}
	}
	run(fixture.Select("SELECT v FROM bsf_fixed", []string{"v"}, []string{"BIT"}, nil))
	run(fixture.Select("SELECT v FROM bsf_narrow", []string{"v"}, []string{"VARBIT"}, nil))
	run(fixture.Exec("UPDATE bsf_cons SET i=0 WHERE id=2", 1))
	for _, q := range []string{"SELECT overlay(v PLACING r FROM i FOR n) FROM bsf_cons ORDER BY id", "INSERT INTO bsf_dst SELECT id,overlay(v PLACING r FROM i FOR n) FROM bsf_cons ORDER BY id", "UPDATE bsf_cons SET v=overlay(v PLACING r FROM i FOR n) RETURNING id,v"} {
		for _, prepared := range []bool{false, true} {
			s := bitStringFunctionsError(q, "22011", "negative substring length not allowed", 0, prepared, false)
			run(s)
		}
		run(fixture.Select("SELECT id,v FROM bsf_cons ORDER BY id", []string{"id", "v"}, []string{"INT4", "VARBIT"}, [][]any{{int64(1), "10110"}, {int64(2), "000"}, {int64(3), nil}}))
		run(fixture.Select("SELECT id,v FROM bsf_dst", []string{"id", "v"}, []string{"INT4", "VARBIT"}, nil))
	}
	run(fixture.Exec("UPDATE bsf_cons SET i=1 WHERE id=2", 1))
	bitStringFunctionsBoth(t, p, b, c, fixture.Select("SELECT overlay(v PLACING r FROM i FOR n) AS o FROM bsf_cons ORDER BY id", []string{"o"}, []string{"BIT"}, [][]any{{"10010"}, {"100"}, {nil}}))
}
