package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgvisibilityFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"pv_smoke_reader"})
	pvCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		pvEmpty(t,p,b,c,"pv_part.heap")
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.pg_visibility(NULL::regclass,0)",pvPageNames,pvPageTypes,[][]any{{nil,nil,nil}}))
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.pg_visibility(NULL::regclass)",pvPageRowsNames,pvPageRowsTypes,nil))
		pvRun(t,p,b,c,fixture.Exec("INSERT INTO pv_part.heap VALUES(1)",1));pvFreeze(t,p,b,c);pvOneFrozen(t,p,b,c)
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.pg_visibility_map('pv_part.heap'::regclass) ORDER BY blkno",pvMapRowsNames,pvMapRowsTypes,[][]any{{int64(0),true,true}}))
		pvBoth(t,p,b,c,pvError("SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass,-1)","22023","invalid block number"))
		pvProbe(t,p,b,c,"pv_part.heap",4294967294,false,false,false)
		for _,q:=range []string{"CREATE ROLE pv_smoke_reader","GRANT USAGE ON SCHEMA pv_part TO pv_smoke_reader","ALTER TABLE pv_part.heap OWNER TO pv_smoke_reader","SET SESSION AUTHORIZATION pv_smoke_reader"}{pvRun(t,p,b,c,fixture.Exec(q,0))}
		defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
		pvBoth(t,p,b,c,pvError("SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)","42501","permission denied for function pg_visibility_map_summary"))
		for _,q:=range []string{"RESET SESSION AUTHORIZATION","ALTER TABLE pv_part.heap OWNER TO CURRENT_USER","GRANT EXECUTE ON FUNCTION pv_part.pg_visibility_map_summary(regclass) TO pv_smoke_reader","SET SESSION AUTHORIZATION pv_smoke_reader"}{pvRun(t,p,b,c,fixture.Exec(q,0))}
		pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.pg_visibility_map_summary('pv_part.heap'::regclass)",pvMapNames,pvSummaryTypes,[][]any{{int64(1),int64(1)}}))
		pvBoth(t,p,b,c,pvError("SELECT k FROM pv_part.heap","42501","permission denied for table heap"))
		pvRun(t,p,b,c,fixture.Exec("RESET SESSION AUTHORIZATION",0))
		pvRun(t,p,b,c,fixture.Exec("CREATE VIEW pv_part.smoke_view AS SELECT * FROM pv_part.pg_visibility('pv_part.heap'::regclass)",0))
		q:="SELECT * FROM pv_part.pg_visibility($1::regclass,$2::bigint)"
		st,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer st.Close()
		held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:q}
		read:=func(){fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,pvPageNames,pvPageTypes,[][]any{{true,true,true}},"pv_part.heap",int64(0)))}
		read()
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,pvPageNames,pvPageTypes,[][]any{{nil,nil,nil}},nil,int64(0)))
		if !b.PG {
			dump:=fixture.ExtensionDump(t,p.Ctx,p.Native)
			if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
			if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)}
			read()
			pvBoth(t,p,b,c,fixture.Select("SELECT * FROM pv_part.smoke_view ORDER BY blkno",pvPageRowsNames,pvPageRowsTypes,[][]any{{int64(0),true,true,true}}))
			pvCoherent(t,p,b,c,"pv_part.heap")
		}
	})
}
