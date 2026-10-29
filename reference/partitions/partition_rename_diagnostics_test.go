package reference_test

import (
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferencePartitionRenameColumnDiagnostics(t *testing.T){
 cases:=[]struct{q,code,message string}{
 {"ALTER TABLE ONLY pr_root RENAME k TO x","42P16",`inherited column "k" must be renamed in child tables too`},
 {"ALTER TABLE ONLY pr_root RENAME missing TO payload","42P16",`inherited column "missing" must be renamed in child tables too`},
 {"ALTER TABLE ONLY pr_mid RENAME k TO x","42P16",`inherited column "k" must be renamed in child tables too`},
 {"ALTER TABLE pr_h0 RENAME k TO payload","42P16",`cannot rename inherited column "k"`},
 {"ALTER TABLE pr_mid RENAME k TO x","42P16",`cannot rename inherited column "k"`},
 {"ALTER TABLE pr_h0 RENAME missing TO payload","42703",`column "missing" does not exist`},
 {"ALTER TABLE pr_h0 RENAME xmin TO x","0A000",`cannot rename system column "xmin"`},
 {"ALTER TABLE pr_root RENAME xmin TO x","0A000",`cannot rename system column "xmin"`},
 {"ALTER TABLE pr_root RENAME k TO xmin","42701",`column name "xmin" conflicts with a system column name`},
 {"ALTER TABLE pr_root RENAME k TO payload","42701",`column "payload" of relation "pr_mid" already exists`},
 {"ALTER TABLE pr_root RENAME k TO k","42701",`column "k" of relation "pr_mid" already exists`},
 {"ALTER TABLE pr_root RENAME missing TO payload","42703",`column "missing" does not exist`},
 }
 for _,tc:=range cases{t.Run(tc.q,func(t *testing.T){referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)};partitionRenameSetup(run)
  for _,prepared:=range []bool{false,true}{s:=referenceRangeError(tc.q,tc.code,tc.message,fixture.Diagnostic{});s.Prepared=prepared;run(s);partitionRenameRows(run,"k","payload")}
  run(fixture.Exec("ALTER TABLE pr_root RENAME k TO renamed",0));partitionRenameRows(run,"renamed","payload")
 })})}
 t.Run("boundaries_and_quoting",func(t *testing.T){referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,b.DB,b.Recorder,s)}
  run(fixture.Exec("CREATE TABLE pr_empty(k INT) PARTITION BY RANGE(k)",0))
  run(fixture.Exec("ALTER TABLE ONLY pr_empty RENAME k TO renamed",0))
  run(fixture.Select("SELECT * FROM pr_empty",[]string{"renamed"},[]string{"INT4"},nil))
  run(referenceRangeError("ALTER TABLE pr_empty RENAME missing TO renamed","42703",`column "missing" does not exist`,fixture.Diagnostic{}))
  run(fixture.Exec("CREATE TABLE pr_plain(k INT)",0));run(fixture.Exec("ALTER TABLE ONLY pr_plain RENAME k TO renamed",0))
  partitionRenameSetup(run)
  for _,q:=range []string{`ALTER TABLE pr_root RENAME k TO "New Key"`,`ALTER TABLE pr_root RENAME "New Key" TO "a""b"`,`ALTER TABLE pr_root RENAME "a""b" TO "select"`}{run(fixture.Exec(q,0))}
  for _,table:=range []string{"pr_root","pr_mid","pr_hash","pr_h0","pr_h1"}{run(fixture.Select("SELECT * FROM ONLY "+table+" WHERE false",[]string{"select","bucket","payload"},[]string{"INT4","INT4","TEXT"},nil))}
  s:=referenceRangeError("SELECT k FROM pr_root","42703",`column "k" does not exist`,fixture.Diagnostic{Position:"8"});s.SelectRows=true;run(s)
  q:="INSERT INTO pr_root(k,bucket,payload) VALUES(4,0,'bad')";run(referenceRangeError(q,"42703",`column "k" of relation "pr_root" does not exist`,fixture.Diagnostic{Position:referenceRangePosition(q,"k,")}))
 })})
}

func TestReferencePartitionRenameColumnAuthority(t *testing.T){
 for _,mode:=range []string{"root_nonowner","child_nonowner","only","missing_precedence","breadth_first","owner_no_create","schema_owner"}{t.Run(mode,func(t *testing.T){
  fixture.RoleReferenceCleanup(t,[]string{"pr_owner","pr_other"})
  referenceRangeScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend){
   c,e:=b.DB.Conn(p.Ctx);if e!=nil{t.Fatal(e)};defer c.Close()
   run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
   run(fixture.Exec("CREATE ROLE pr_owner LOGIN",0));run(fixture.Exec("CREATE ROLE pr_other LOGIN",0));run(fixture.Exec("GRANT USAGE ON SCHEMA public TO pr_owner,pr_other",0))
   partitionRenameSetup(run)
   for _,table:=range []string{"pr_root","pr_mid","pr_hash","pr_h0","pr_h1"}{run(fixture.Exec("ALTER TABLE "+table+" OWNER TO pr_owner",0))}
   run(fixture.Exec("GRANT ALL ON pr_root TO pr_other",0))
   q,code,message:="ALTER TABLE pr_root RENAME k TO renamed","42501","must be owner of table pr_root"
   actor:="pr_other"
   switch mode{
   case "breadth_first":
    actor="pr_owner";message="must be owner of table pr_side"
    run(fixture.Exec("CREATE TABLE pr_side PARTITION OF pr_root FOR VALUES FROM(10) TO(20)",0))
    run(fixture.Exec("ALTER TABLE pr_side OWNER TO pr_other",0))
    run(fixture.Exec("ALTER TABLE pr_h0 OWNER TO pr_other",0))
   case "child_nonowner","only","missing_precedence":
    actor="pr_owner";run(fixture.Exec("ALTER TABLE pr_mid OWNER TO pr_other",0));message="must be owner of table pr_mid"
    if mode=="only"{q="ALTER TABLE ONLY pr_root RENAME missing TO payload";code="42P16";message=`inherited column "missing" must be renamed in child tables too`}
    if mode=="missing_precedence"{q="ALTER TABLE pr_root RENAME missing TO payload"}
   case "root_nonowner":q="ALTER TABLE ONLY pr_root RENAME missing TO payload"
   case "owner_no_create":actor="pr_owner"
   }
   if mode=="owner_no_create"{run(fixture.Exec("REVOKE CREATE ON SCHEMA public FROM PUBLIC,pr_owner",0))}
   body:=func(){run(fixture.Exec("SET SESSION AUTHORIZATION "+actor,0));defer run(fixture.Exec("RESET SESSION AUTHORIZATION",0));if mode=="owner_no_create"{run(fixture.Exec(q,0))}else{run(referenceRangeError(q,code,message,fixture.Diagnostic{}))}}
   if mode=="schema_owner"{fixture.WithPublicSchemaOwner(t,p,b,"pr_other",body)}else{body()}
   key:="k";if mode=="owner_no_create"{key="renamed"};partitionRenameRows(run,key,"payload")
   if mode!="owner_no_create"{run(fixture.Exec("ALTER TABLE pr_root RENAME k TO renamed",0));partitionRenameRows(run,"renamed","payload")}
   if mode=="breadth_first"{run(fixture.Select("SELECT * FROM pr_side",[]string{"renamed","bucket","payload"},[]string{"INT4","INT4","TEXT"},nil))}
  })
 })}
}
