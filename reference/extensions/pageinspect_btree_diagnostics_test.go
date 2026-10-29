package reference_test

import (
 "database/sql"
 "encoding/binary"
 "fmt"
 "testing"
 "stuntgres/reference/fixture"
)
func btError(page []byte,code,message,detail string)fixture.Step{s:=fixture.ImplicitError(btQuery,fixture.Diagnostic{Code:code,Message:message,Detail:detail},page);s.ErrorStage="execute";return s}
func TestReferencePageinspectBtreeDiagnostics(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("CREATE TABLE bt_sentinel(n integer)",0));fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO bt_sentinel VALUES(7)",1))
 for _,n:=range []int{0,8191,8193}{btBoth(t,p,b,c,btError(make([]byte,n),"22023","invalid page size",fmt.Sprintf("Expected 8192 bytes, got %d.",n)))}
 bad:=fixture.BTPage(9,1,0);binary.LittleEndian.PutUint16(bad[16:],8175);btBoth(t,p,b,c,btError(bad,"22023","input page is not a valid btree page","Expected special size 16, got 17."))
 btBoth(t,p,b,c,btError(fixture.BTPage(9,1,0),"22023","block is a meta page",""));btBoth(t,p,b,c,btError(fixture.BTPage(5,1,0),"22023","block is not a valid btree leaf page",""))
 for _,tc:=range []struct{tuple []byte;length int}{{fixture.BTTuple(1,1,7),-1},{fixture.BTTuple(1,1,0x8008),-8},{fixture.BTTuple(4,8194,0x2020,make([]byte,24)...),-4},{fixture.BTTuple(8200,8194,0x2020,make([]byte,24)...),8192}}{
  for _,slot:=range []int{1,2}{tuples:=[][]byte{tc.tuple};if slot==2{tuples=[][]byte{fixture.BTTuple(1,1,8),tc.tuple}};btBoth(t,p,b,c,btError(fixture.BTPage(1,0,0,tuples...),"XX000",fmt.Sprintf("invalid tuple length %d for tuple at offset number %d",tc.length,slot),""))}
 }
 btBoth(t,p,b,c,btStep(fixture.BTOrdinary(),fixture.BTOrdinaryRows));btBoth(t,p,b,c,fixture.Select("SELECT n FROM bt_sentinel ORDER BY n",[]string{"n"},[]string{"INT4"},[][]any{{int64(7)}}))
})}
func TestReferencePageinspectBtreePermissions(t *testing.T){fixture.RoleReferenceCleanup(t,[]string{"bt_reader"});btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 for _,q:=range []string{"CREATE ROLE bt_reader","GRANT USAGE ON SCHEMA bt_part TO bt_reader"}{fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec(q,0))}
 st,err:=c.PrepareContext(p.Ctx,btQuery);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:btQuery}
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("SET SESSION AUTHORIZATION bt_reader",0));defer func(){if _,err:=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION");err!=nil{t.Error(err)}}()
 for _,raw:=range [][]byte{fixture.BTOrdinary(),{},make([]byte,8192),fixture.BTPage(9,1,0)}{s:=btError(raw,"42501","must be superuser to use raw page functions","");btBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,held,b.Recorder,s)}
 btBoth(t,p,b,c,btStep(nil,nil));fixture.RunStep(t,p.Ctx,held,b.Recorder,btStep(nil,nil))
 fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("RESET SESSION AUTHORIZATION",0));fixture.RunStep(t,p.Ctx,held,b.Recorder,btStep(fixture.BTPosting(),fixture.BTPostingRows))
})}
func btNoticeRun(t *testing.T,p *fixture.Pair,b fixture.Backend,body func(*sql.Conn,*fixture.Recorder,*[]fixture.Diagnostic)){
 t.Helper();var notices []fixture.Diagnostic
 _,conn,recorder:=fixture.NoticeReferencePool(t,p,b.PG,&notices);body(conn,recorder,&notices)
}
func TestReferencePageinspectBtreeNotices(t *testing.T){btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,_ *sql.Conn){btNoticeRun(t,p,b,func(c *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic){
 check:=func(s fixture.Step,want []fixture.Diagnostic){t.Helper();for _,prepared:=range []bool{false,true}{*notices=nil;if prepared{s=fixture.Prepared(s)};fixture.RunStep(t,p.Ctx,c,r,s);if len(*notices)!=len(want){t.Fatalf("notices: %#v want %#v",*notices,want)};for i:=range want{if (*notices)[i]!=want[i]{t.Fatalf("notice %d: %#v want %#v",i,(*notices)[i],want[i])}}}}
 deleted:=fixture.BTOrdinary();binary.LittleEndian.PutUint16(deleted[8188:],5)
 check(btStep(deleted,nil),[]fixture.Diagnostic{{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"page is deleted"},{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"page from block is deleted"}})
 binary.LittleEndian.PutUint16(deleted[14:],0);check(btStep(deleted,fixture.BTNullRows),nil);check(btStep(nil,nil),nil);check(btStep(fixture.BTPage(1,0,0),nil),nil)
 s:=btStep(fixture.BTPage(5,0,0),nil);s.Query="SELECT * FROM bt_part.bt_page_items($1) WHERE false";check(s,nil)
 check(btError(fixture.BTPage(5,1,0),"22023","block is not a valid btree leaf page",""),nil)
})})}
