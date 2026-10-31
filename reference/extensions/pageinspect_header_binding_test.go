package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectHeaderBinding(t *testing.T) {headerCase(t,false,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,tc:=range []struct{q,message string}{
  {"SELECT page_header(NULL::bytea)","function page_header(bytea) does not exist"},
  {"SELECT page_checksum(NULL::bytea,0::bigint)","function page_checksum(bytea, bigint) does not exist"},
 } {
  s:=fixture.ImplicitError(tc.q,fixture.Diagnostic{Code:"42883",Message:tc.message,Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(8)});s.SelectRows=true;headerBoth(t,p,b,c,s)
 }
 for _,q:=range []string{"CREATE SCHEMA header_part","CREATE EXTENSION pageinspect SCHEMA header_part","SET search_path TO header_part,public"} {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 defer c.ExecContext(p.Ctx,"RESET search_path")
 for _,q:=range []string{"SELECT * FROM page_header($1)","SELECT * FROM header_part.page_header(page=>$1)"} {headerHeld(t,p,b,c,q,[]fixture.Step{headerStep(q,fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow),headerStep(q,nil,make([]any,9)),headerStep(q,make([]byte,8192),fixture.HeaderZero)})}
 for _,q:=range []string{"SELECT page_checksum($1,$2)","SELECT * FROM header_part.page_checksum(page=>$1,blkno=>$2)","SELECT header_part.page_checksum(blkno=>$2,page=>$1)"} {
  headerHeld(t,p,b,c,q,[]fixture.Step{checksumStep(q,fixture.HeaderPattern(0xab,0xcd),int64(0),int64(-30781)),checksumStep(q,fixture.HeaderPattern(0xab,0xcd),int64(50),int64(-30795)),checksumStep(q,fixture.HeaderPattern(4),int64(100),int64(28438)),checksumStep(q,nil,int64(0),nil),headerError(q,"22023","invalid block number","",fixture.HeaderPattern(1),int64(-1)),checksumStep(q,fixture.HeaderPattern(1),int64(100),int64(1139))})
 }
 // Written order is block first; inference must bind $1 to INT8 and $2 to BYTEA.
 q:="SELECT header_part.page_checksum(blkno=>$1,page=>$2)"
 steps:=[]fixture.Step{}
 for _,tc:=range []struct{block any;page any;want any}{{int64(0),fixture.HeaderPattern(1),int64(1175)},{int64(50),fixture.HeaderPattern(4),int64(28352)},{nil,[]byte{},nil},{int64(100),fixture.HeaderPattern(0xab,0xcd),int64(-30881)}} {s:=fixture.Select(q,[]string{"page_checksum"},[]string{"INT2"},[][]any{{tc.want}},tc.block,tc.page);steps=append(steps,s)}
 headerHeld(t,p,b,c,q,steps)
 headerBoth(t,p,b,c,fixture.Select("SELECT p.proname::text AS name,p.proargtypes::text AS args FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='header_part' AND ((p.proname='page_header' AND p.proargtypes::text='17') OR (p.proname='page_checksum' AND p.proargtypes::text='17 20')) ORDER BY p.proname",[]string{"name","args"},[]string{"TEXT","TEXT"},[][]any{{"page_checksum","17 20"},{"page_header","17"}}))
 headerBoth(t,p,b,c,fixture.Select("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='header_part' AND p.proname='page_checksum' AND p.proargtypes::text='17 23'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
 headerBoth(t,p,b,c,checksumStep("SELECT page_checksum($1,$2::integer)",fixture.HeaderPattern(1),100,int64(1139)))
 for _,q:=range []string{"CREATE SCHEMA header_moved","ALTER FUNCTION header_part.page_header(bytea) RENAME TO decoded","ALTER FUNCTION header_part.decoded(bytea) SET SCHEMA header_moved","ALTER FUNCTION header_part.page_checksum(bytea,bigint) RENAME TO checked","ALTER FUNCTION header_part.checked(bytea,bigint) SET SCHEMA header_moved","SET search_path TO header_moved,public"} {fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 headerBoth(t,p,b,c,headerStep("SELECT * FROM decoded(page=>$1)",fixture.HeaderAsymmetric(),fixture.HeaderAsymmetricRow))
 s:=checksumStep("SELECT checked(blkno=>$2,page=>$1)",fixture.HeaderPattern(1),int64(50),int64(1225));s.Columns=[]string{"checked"};headerBoth(t,p,b,c,s)
})}
