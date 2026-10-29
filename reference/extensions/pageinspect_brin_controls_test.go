package reference_test

import (
 "database/sql"
 "strings"
 "testing"

 "stuntgres"
 "stuntgres/reference/fixture"
)

func TestReferencePageinspectBrinControls(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"pi_owner"})
 brinCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  for _,q:=range []string{
   "CREATE USER pi_owner SUPERUSER","CREATE SCHEMA brin_moved","CREATE TABLE pi_data(n integer)","CREATE INDEX pi_idx ON pi_data USING brin(n)",
   "CREATE TABLE pi_meta(raw bytea)","CREATE TABLE pi_raw(raw bytea)","CREATE TABLE pi_rev(raw bytea)",
  }{run(fixture.Exec(q,0))}
  run(fixture.Exec("INSERT INTO pi_meta VALUES($1)",1,fixture.BRINMeta()))
  run(fixture.Exec("INSERT INTO pi_raw VALUES($1)",1,brinItems(brinInt4(-7,42),nil)))
  run(fixture.Exec("INSERT INTO pi_rev VALUES($1)",1,make([]byte,8192)))
  run(fixture.Exec("CREATE VIEW pi_types AS SELECT brin_part.brin_page_type(raw) AS kind FROM pi_meta",0))
  run(fixture.Exec("CREATE VIEW pi_meta_view AS SELECT i.* FROM pi_meta CROSS JOIN LATERAL brin_part.brin_metapage_info(raw) AS i",0))
  run(fixture.Exec("CREATE VIEW pi_items_view AS SELECT i.* FROM pi_raw CROSS JOIN LATERAL brin_part.brin_page_items(raw,'pi_idx'::regclass) AS i",0))
  run(fixture.Exec("CREATE VIEW pi_rev_view AS SELECT i.* FROM pi_rev CROSS JOIN LATERAL brin_part.brin_revmap_data(raw) AS i",0))
  for _,s:=range brinControlCatalogSteps(false,b.PG){fixture.BRINBoth(t,p,b,c,s)}

  // Prepare actual stable view consumers before any installed member changes.
  // The same statements survive the moves and original-driver replacement;
  // they must follow the ordinary current-definition rebind contract.
  viewSteps:=brinControlViewSteps()
  retained:=make([]fixture.HeldStatement,len(viewSteps))
  for i,s:=range viewSteps{
   statement,err:=c.PrepareContext(p.Ctx,s.Query);if err!=nil{t.Fatal(err)}
   defer statement.Close()
   retained[i]=fixture.HeldStatement{Executor:c,Statement:statement,SQL:s.Query}
   fixture.RunStep(t,p.Ctx,retained[i],b.Recorder,fixture.BRINMetadata(s,b.PG,true))
  }
  run(fixture.Exec("ALTER FUNCTION brin_part.brin_page_type(bytea) OWNER TO pi_owner",0))
  run(fixture.Exec("ALTER FUNCTION brin_part.brin_page_type(bytea) RENAME TO page_kind",0))
  run(fixture.Exec("ALTER FUNCTION brin_part.page_kind(bytea) SET SCHEMA brin_moved",0))
  run(fixture.Exec("ALTER FUNCTION brin_part.brin_metapage_info(bytea) SET SCHEMA brin_moved",0))
  run(fixture.Exec("ALTER FUNCTION brin_part.brin_revmap_data(bytea) SET SCHEMA brin_moved",0))
  run(fixture.Exec("ALTER FUNCTION brin_part.brin_page_items(bytea,regclass) SET SCHEMA brin_moved",0))

  check:=func(conn *sql.Conn,recorder *fixture.Recorder,pg bool){
   execute:=func(s fixture.Step){t.Helper();fixture.RunStep(t,p.Ctx,conn,recorder,s);fixture.RunStep(t,p.Ctx,conn,recorder,fixture.Prepared(s))}
   for _,s:=range brinControlCatalogSteps(true,pg){execute(s)}
   emptyQueries:=[]string{"SELECT * FROM pi_types WHERE false","SELECT * FROM pi_meta_view WHERE false","SELECT * FROM pi_items_view WHERE false","SELECT * FROM pi_rev_view WHERE false"}
   for i,s:=range viewSteps{
    execute(fixture.BRINMetadata(s,pg,true))
    s.Query=emptyQueries[i];s.Rows=nil
    execute(fixture.BRINMetadata(s,pg,true))
   }
   for _,s:=range brinMovedSteps(){execute(fixture.BRINMetadata(s,pg,false))}
  }
  checkOriginal:=func(){
   check(c,b.Recorder,b.PG)
   for i,s:=range viewSteps{fixture.RunStep(t,p.Ctx,retained[i],b.Recorder,fixture.BRINMetadata(s,b.PG,true))}
  }
  checkOriginal()
  if b.PG{return}
  dump:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  other,destination,record:=pgcryptoControlDestination(t,p,"pi_restore")
  if _,err:=other.ExecContext(p.Ctx,"CREATE ROLE pi_owner");err!=nil{t.Fatal(err)}
  if err:=stuntgres.Restore(p.Ctx,other,strings.NewReader(dump));err!=nil{t.Fatal(err)}
  check(destination,record,false)
  if redump:=fixture.ExtensionDump(t,p.Ctx,other);redump!=dump{t.Fatal("independent replay changed symbolic identity")}
  if err:=stuntgres.Reset(p.Ctx,p.Native);err!=nil{t.Fatal(err)}
  if _,err:=c.ExecContext(p.Ctx,"SELECT brin_moved.page_kind($1)",fixture.BRINMeta());err==nil{t.Fatal("reset retained callable installation")}
  if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader(dump));err!=nil{t.Fatal(err)}
  checkOriginal()
  before:=fixture.ExtensionDump(t,p.Ctx,p.Native)
  if err:=stuntgres.Restore(p.Ctx,p.Native,strings.NewReader("CREATE EXTENSION missing_pageinspect_provider;"));err==nil{t.Fatal("invalid provider replay accepted")}
  if fixture.ExtensionDump(t,p.Ctx,p.Native)!=before{t.Fatal("failed replay replaced live decode")}
  checkOriginal()
 })
}
