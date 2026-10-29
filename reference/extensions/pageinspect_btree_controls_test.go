package reference_test

import (
 "database/sql"
 "encoding/binary"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)
func btControls(t *testing.T,smoke bool){fixture.RoleReferenceCleanup(t,[]string{"bt_owner"});btCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 run:=func(s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 for _,q:=range []string{"CREATE USER bt_owner SUPERUSER","CREATE SCHEMA bt_moved","CREATE TABLE bt_images(raw bytea)"}{run(fixture.Exec(q,0))}
 run(fixture.Exec("INSERT INTO bt_images VALUES($1)",1,fixture.BTPosting()))
 run(fixture.Exec("CREATE VIEW bt_view AS SELECT i.* FROM bt_images CROSS JOIN LATERAL bt_part.bt_page_items(raw) AS i",0))
 view:=fixture.Select("SELECT * FROM bt_view ORDER BY itemoffset",fixture.BTNames,fixture.BTTypes,fixture.BTPostingRows)
 st,err:=c.PrepareContext(p.Ctx,view.Query);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:view.Query}
 rawStatement,err:=c.PrepareContext(p.Ctx,btQuery);if err!=nil{t.Fatal(err)};defer rawStatement.Close();rawHeld:=fixture.HeldStatement{Executor:c,Statement:rawStatement,SQL:btQuery}
 for _,s:=range []fixture.Step{btStep(fixture.BTPosting(),fixture.BTPostingRows),btStep(fixture.BTOrdinary(),fixture.BTOrdinaryRows),btStep(nil,nil),btStep(make([]byte,8192),fixture.BTNullRows)}{fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,fixture.BTMetadata(s,b.PG,false))}
 fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.BTMetadata(view,b.PG,true))
 if smoke{
  bad:=fixture.BTPage(1,0,0,fixture.BTTuple(1,1,7));s:=btError(bad,"XX000","invalid tuple length -1 for tuple at offset number 1","");btBoth(t,p,b,c,s);btBoth(t,p,b,c,fixture.BTMetadata(btStep(fixture.BTPosting(),fixture.BTPostingRows),b.PG,false))
  btBoth(t,p,b,c,fixture.Select("SELECT tids[2] AS tid,array_lower(tids,1) AS lo,array_upper(tids,1) AS hi FROM bt_part.bt_page_items($1)",[]string{"tid","lo","hi"},[]string{"TID","INT4","INT4"},[][]any{{"(65538,513)",int64(1),int64(2)}},fixture.BTPosting()))
  btNoticeRun(t,p,b,func(conn *sql.Conn,r *fixture.Recorder,notices *[]fixture.Diagnostic){deleted:=fixture.BTPosting();binary.LittleEndian.PutUint16(deleted[8188:],5);*notices=nil;fixture.RunStep(t,p.Ctx,conn,r,btStep(deleted,nil));want:=[]fixture.Diagnostic{{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"page is deleted"},{Severity:"NOTICE",SeverityUnlocalized:"NOTICE",Code:"00000",Message:"page from block is deleted"}};if len(*notices)!=2 || (*notices)[0]!=want[0] || (*notices)[1]!=want[1]{t.Fatal("deleted notices",*notices)}})
 }
 for _,q:=range []string{"ALTER FUNCTION bt_part.bt_page_items(bytea) OWNER TO bt_owner","ALTER FUNCTION bt_part.bt_page_items(bytea) RENAME TO items","ALTER FUNCTION bt_part.items(bytea) SET SCHEMA bt_moved"}{run(fixture.Exec(q,0))}
 // With the old declaration gone, the untyped parameter remains unknown.
 missing:=fixture.ImplicitError(btQuery,fixture.Diagnostic{Code:"42883",Message:"function bt_part.bt_page_items(unknown) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(15)},fixture.BTOrdinary());btBoth(t,p,b,c,missing)
 check:=func(conn *sql.Conn,r *fixture.Recorder,pg bool){
  execute:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,r,s);fixture.RunStep(t,p.Ctx,conn,r,fixture.Prepared(s))}
  execute(fixture.BTDeclaration("items","bt_moved","bt_owner"));execute(fixture.BTMetadata(view,pg,true));empty:=view;empty.Query="SELECT * FROM bt_view WHERE false";empty.Rows=nil;execute(fixture.BTMetadata(empty,pg,true))
  for _,s:=range []fixture.Step{btStep(fixture.BTOrdinary(),fixture.BTOrdinaryRows),btStep(fixture.BTPosting(),fixture.BTPostingRows),btStep(fixture.BTPage(0,1,0,fixture.BTTuple(7,0,0x2008)),[][]any{{int64(1),"(7,0)",int64(8),false,false,"",nil,nil,nil}}),btStep(nil,nil),btStep(make([]byte,8192),fixture.BTNullRows)}{s.Query="SELECT * FROM bt_moved.items($1) ORDER BY itemoffset";execute(fixture.BTMetadata(s,pg,false))}
  principal:="postgres";if pg{principal="stuntgres"};execute(fixture.Select("SELECT n.nspname::text AS target,r.rolname::text AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pageinspect'",[]string{"target","owner"},[]string{"TEXT","TEXT"},[][]any{{"bt_part",principal}}))
 }
 checkOriginal:=func(){check(c,b.Recorder,b.PG);fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.BTMetadata(view,b.PG,true))};checkOriginal()
 // A retained consumer sees changed physical input in both modes after DDL.
 run(fixture.Exec("UPDATE bt_images SET raw=$1",1,fixture.BTOrdinary()));ordinaryView:=view;ordinaryView.Rows=fixture.BTOrdinaryRows;fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.BTMetadata(ordinaryView,b.PG,true));run(fixture.Exec("UPDATE bt_images SET raw=$1",1,fixture.BTPosting()));checkOriginal()
 if b.PG{return}
 dump:=fixture.ExtensionDump(t,p.Ctx,p.Native);other,destination,r:=pgcryptoControlDestination(t,p,"bt_restore");if _,err:=other.ExecContext(p.Ctx,"CREATE ROLE bt_owner SUPERUSER");err!=nil{t.Fatal(err)}
 if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil{t.Fatal(err)};check(destination,r,false)
 if redump:=fixture.ExtensionDump(t,p.Ctx,other);redump!=dump{t.Fatal("symbolic replay changed")}
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)};if _,err:=c.QueryContext(p.Ctx,"SELECT * FROM bt_moved.items($1)",fixture.BTPosting());err==nil{t.Fatal("reset retained callable provider")}
 if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)};checkOriginal()
 // The same retained view consumer observes independently different stored bytes.
 run(fixture.Exec("UPDATE bt_images SET raw=$1",1,fixture.BTOrdinary()));fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.BTMetadata(ordinaryView,false,true));run(fixture.Exec("UPDATE bt_images SET raw=$1",1,fixture.BTPosting()))
 before:=fixture.ExtensionDump(t,p.Ctx,p.Native);if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE EXTENSION missing_pageinspect_provider;"));err==nil{t.Fatal("invalid provider replay accepted")};if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("failed replay replaced live state")};checkOriginal()
})}
func TestReferencePageinspectBtreeControls(t *testing.T){btControls(t,false)}
func TestReferencePageinspectBtreeFeatureSmoke(t *testing.T){btControls(t,true)}
