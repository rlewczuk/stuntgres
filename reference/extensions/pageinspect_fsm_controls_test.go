package reference_test

import (
 "database/sql"
 "strings"
 "testing"
 "stuntgres"
 "stuntgres/reference/fixture"
)

func fsmControls(t *testing.T,smoke bool){fixture.RoleReferenceCleanup(t,[]string{"fsm_owner","fsm_reader"});fsmCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 run:=func(s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 for _,q:=range []string{"CREATE USER fsm_owner SUPERUSER","CREATE SCHEMA fsm_moved","CREATE TABLE fsm_images(id integer,raw bytea)"}{run(fixture.Exec(q,0))}
 run(fixture.Exec("INSERT INTO fsm_images VALUES(1,$1)",1,fixture.FSMSparse()))
 run(fixture.Exec("CREATE VIEW fsm_view AS SELECT id,fsm_part.fsm_page_contents(raw) AS contents FROM fsm_images",0))
 view:=fixture.Select("SELECT contents FROM fsm_view ORDER BY id",[]string{"contents"},[]string{"TEXT"},[][]any{{fixture.FSMSparseText}})
 st,err:=c.PrepareContext(p.Ctx,view.Query);if err!=nil{t.Fatal(err)};defer st.Close();held:=fixture.HeldStatement{Executor:c,Statement:st,SQL:view.Query}
 for _,query:=range []string{fsmScalar,fsmSource}{
  rawStatement,err:=c.PrepareContext(p.Ctx,query);if err!=nil{t.Fatal(err)};rawHeld:=fixture.HeldStatement{Executor:c,Statement:rawStatement,SQL:query}
  for _,tc:=range []struct{raw []byte;want any}{{fixture.FSMSparse(),fixture.FSMSparseText},{fixture.FSMPage(0xffffffff),"fp_next_slot: -1\n"},{nil,nil},{make([]byte,8192),nil},{fixture.FSMSparse(),fixture.FSMSparseText}}{s:=fixture.FSMMetadata(fsmStep(query,tc.raw,tc.want),b.PG,false);fsmBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,s)}
  if smoke{s:=fsmError(query,[]byte{},"22023","invalid page size","Expected 8192 bytes, got 0.");fsmBoth(t,p,b,c,s);fixture.RunStep(t,p.Ctx,rawHeld,b.Recorder,fixture.FSMMetadata(fsmStep(query,fixture.FSMSparse(),fixture.FSMSparseText),b.PG,false))}
  if err:=rawStatement.Close();err!=nil{t.Fatal(err)}
 }
 if smoke{fsmAuthority(t,p,b,c,[]string{fsmScalar,fsmSource})}
 checkHeld:=func(want any){s:=view;s.Rows=[][]any{{want}};fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.FSMMetadata(s,b.PG,true))}
 changeInputs:=func(){for _,tc:=range []struct{raw []byte;want any}{{fixture.FSMPage(0xffffffff),"fp_next_slot: -1\n"},{make([]byte,8192),nil},{fixture.FSMSparse(),fixture.FSMSparseText}}{run(fixture.Exec("UPDATE fsm_images SET raw=$1 WHERE id=1",1,tc.raw));checkHeld(tc.want)}}
 checkHeld(fixture.FSMSparseText);changeInputs()
 for _,q:=range []string{"ALTER FUNCTION fsm_part.fsm_page_contents(bytea) OWNER TO fsm_owner","ALTER FUNCTION fsm_part.fsm_page_contents(bytea) RENAME TO contents","ALTER FUNCTION fsm_part.contents(bytea) SET SCHEMA fsm_moved"}{run(fixture.Exec(q,0))}
 missing:=fixture.ImplicitError(fsmScalar,fixture.Diagnostic{Code:"42883",Message:"function fsm_part.fsm_page_contents(unknown) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:fixture.Position(strings.Index(fsmScalar,"fsm_part.")+1)},fixture.FSMSparse());fsmBoth(t,p,b,c,missing)
 check:=func(conn *sql.Conn,r *fixture.Recorder,pg bool){
  execute:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,r,s);fixture.RunStep(t,p.Ctx,conn,r,fixture.Prepared(s))}
  execute(fixture.FSMDeclaration("contents","fsm_moved","fsm_owner"));execute(fsmRelationalIdentity("contents","fsm_moved"));execute(fixture.FSMMetadata(view,pg,true));empty:=view;empty.Query="SELECT contents FROM fsm_view WHERE false";empty.Rows=nil;execute(fixture.FSMMetadata(empty,pg,true))
  execute(fixture.Select("SELECT id,raw FROM fsm_images ORDER BY id",[]string{"id","raw"},[]string{"INT4","BYTEA"},[][]any{{int64(1),string(fixture.FSMSparse())}}))
  for _,query:=range []string{"SELECT fsm_moved.contents(page => $1)","SELECT * FROM fsm_moved.contents(page => $1)"}{for _,tc:=range []struct{raw []byte;want any}{{fixture.FSMSparse(),fixture.FSMSparseText},{fixture.FSMPage(0xffffffff),"fp_next_slot: -1\n"},{nil,nil},{make([]byte,8192),nil}}{s:=fsmStep(query,tc.raw,tc.want);s.Columns=[]string{"contents"};execute(fixture.FSMMetadata(s,pg,false))}}
  principal:="postgres";if pg{principal="stuntgres"};execute(fixture.Select("SELECT n.nspname::text AS target,r.rolname::text AS owner FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace JOIN pg_roles r ON r.oid=e.extowner WHERE e.extname='pageinspect'",[]string{"target","owner"},[]string{"TEXT","TEXT"},[][]any{{"fsm_part",principal}}))
 }
 checkOriginal:=func(){check(c,b.Recorder,b.PG);checkHeld(fixture.FSMSparseText)};checkOriginal();changeInputs();checkOriginal()
 if b.PG{return}
 dump:=fixture.ExtensionDump(t,p.Ctx,p.Native);other,destination,r:=pgcryptoControlDestination(t,p,"fsm_restore")
 if _,err:=other.ExecContext(p.Ctx,"CREATE ROLE fsm_owner SUPERUSER");err!=nil{t.Fatal(err)}
 if smoke{if _,err:=other.ExecContext(p.Ctx,"CREATE ROLE fsm_reader");err!=nil{t.Fatal(err)}}
 if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil{t.Fatal(err)};check(destination,r,false)
 if redump:=fixture.ExtensionDump(t,p.Ctx,other);redump!=dump{t.Fatal("symbolic replay changed")}
 if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
 rows,err:=c.QueryContext(p.Ctx,"SELECT fsm_moved.contents($1)",fixture.FSMSparse());if err==nil{rows.Close();t.Fatal("reset retained callable provider")}
 if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)};checkOriginal();changeInputs();checkOriginal()
 before:=fixture.ExtensionDump(t,p.Ctx,p.Native);if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE EXTENSION missing_pageinspect_provider;"));err==nil{t.Fatal("unavailable extension restore accepted")};if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("failed restore replaced live state")};checkOriginal()
})}
func TestReferencePageinspectFSMControls(t *testing.T){fsmControls(t,false)}
func TestReferencePageinspectFSMFeatureSmoke(t *testing.T){fsmControls(t,true)}
