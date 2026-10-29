package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func fsmRelationalIdentity(name,schema string)fixture.Step{return fixture.Select("SELECT p.oid::bigint>0 AS valid,p.oid<>b.oid AS distinct_member FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_proc b ON b.pronamespace=(SELECT oid FROM pg_namespace WHERE nspname='fsm_part') WHERE n.nspname='"+schema+"' AND p.proname='"+name+"' AND p.proargtypes::text='17' AND b.proname='brin_page_type' AND b.proargtypes::text='17'",[]string{"valid","distinct_member"},[]string{"BOOL","BOOL"},[][]any{{true,true}})}
func TestReferencePageinspectFSMMetadata(t *testing.T){fsmCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 principal:="postgres";if b.PG{principal="stuntgres"};fsmBoth(t,p,b,c,fixture.FSMDeclaration("fsm_page_contents","fsm_part",principal));fsmBoth(t,p,b,c,fsmRelationalIdentity("fsm_page_contents","fsm_part"))
 for _,query:=range []string{fsmScalar,fsmSource}{st,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:query}
  for _,tc:=range []struct{raw []byte;want any}{{fixture.FSMSparse(),fixture.FSMSparseText},{nil,nil},{make([]byte,8192),nil},{fixture.FSMSparse(),fixture.FSMSparseText}}{s:=fixture.FSMMetadata(fsmStep(query,tc.raw,tc.want),b.PG,false);fsmBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)};if err:=st.Close();err!=nil{t.Fatal(err)}
  for _,raw:=range [][]byte{fixture.FSMSparse(),nil,make([]byte,8192)}{s:=fsmStep(query+" WHERE false",raw,nil);s.Rows=nil;fsmBoth(t,p,b,c,fixture.FSMMetadata(s,b.PG,false))}
 }
})}
