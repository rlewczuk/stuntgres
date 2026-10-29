package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)
func TestReferencePageinspectBtreeMetadata(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 principal:="postgres";if b.PG{principal="stuntgres"};btBoth(t,p,b,c,fixture.BTDeclaration("bt_page_items","bt_part",principal))
 btBoth(t,p,b,c,btRelationalIdentity())
 btBoth(t,p,b,c,fixture.Select("SELECT oid::bigint AS id,typelem::bigint AS element FROM pg_type WHERE typname='_tid'",[]string{"id","element"},[]string{"INT8","INT8"},[][]any{{int64(1010),int64(27)}}))
 st,err:=c.PrepareContext(p.Ctx,btQuery);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:btQuery}
 for _,s:=range []fixture.Step{btStep(fixture.BTOrdinary(),fixture.BTOrdinaryRows),btStep(fixture.BTPosting(),fixture.BTPostingRows),btStep(nil,nil),btStep(make([]byte,8192),fixture.BTNullRows),btStep(fixture.BTPage(1,0,0),nil),btStep(fixture.BTPage(5,0,0),nil)}{s=fixture.BTMetadata(s,b.PG,false);btBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
 s:=btStep(fixture.BTOrdinary(),nil);s.Query="SELECT * FROM bt_part.bt_page_items($1) WHERE false";btBoth(t,p,b,c,fixture.BTMetadata(s,b.PG,false))
 btBoth(t,p,b,c,fixture.BTMetadata(fixture.Select("SELECT bt_part.bt_page_items($1) WHERE false",[]string{"bt_page_items"},[]string{"RECORD"},nil,fixture.BTOrdinary()),b.PG,false))
})}

func btRelationalIdentity()fixture.Step{
 return fixture.Select("SELECT p.oid::bigint>0 AS valid,p.oid<>b.oid AS distinct_member FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_proc b ON b.pronamespace=n.oid WHERE n.nspname='bt_part' AND p.proname='bt_page_items' AND p.proargtypes::text='17' AND b.proname='brin_page_type' AND b.proargtypes::text='17'",[]string{"valid","distinct_member"},[]string{"BOOL","BOOL"},[][]any{{true,true}})
}
