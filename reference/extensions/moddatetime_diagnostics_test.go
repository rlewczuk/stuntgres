package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceModdatetimeDiagnostics(t *testing.T) {
 cases:=[]struct{name,timing,event,level,args,typ,query,code,message string}{
  {"direct","","","","","timestamp","SELECT moddatetime()","XX000","moddatetime: not fired by trigger manager"},
  {"statement","AFTER","UPDATE","STATEMENT","","timestamp","UPDATE mdt SET id=id WHERE false","XX000","moddatetime: must be fired for row"},
  {"before_statement","BEFORE","UPDATE","STATEMENT","","timestamp","UPDATE mdt SET id=id WHERE false","XX000","moddatetime: must be fired for row"},
  {"before_truncate","BEFORE","TRUNCATE","STATEMENT","","timestamp","TRUNCATE mdt","XX000","moddatetime: must be fired for row"},
  {"after_truncate","AFTER","TRUNCATE","STATEMENT","","timestamp","TRUNCATE mdt","XX000","moddatetime: must be fired for row"},
  {"after_precedence","AFTER","UPDATE","ROW","","timestamp","UPDATE mdt SET id=id","XX000","moddatetime: must be fired before event"},
  {"insert_precedence","BEFORE","INSERT","ROW","","timestamp","INSERT INTO mdt VALUES(3,NULL)","XX000","moddatetime: cannot process INSERT events"},
  {"delete","BEFORE","DELETE","ROW","'stamp'","timestamp","DELETE FROM mdt","XX000","moddatetime: cannot process DELETE events"},
  {"zero_args","BEFORE","UPDATE","ROW","","timestamp","UPDATE mdt SET id=id","XX000","moddatetime (mdt): A single argument was expected"},
  {"two_args","BEFORE","UPDATE","ROW","'stamp','stamp'","timestamp","UPDATE mdt SET id=id","XX000","moddatetime (mdt): A single argument was expected"},
 }
 for _,name:=range []string{"missing","Stamp","mdt.stamp","ctid","tableoid"} {cases=append(cases,struct{name,timing,event,level,args,typ,query,code,message string}{name,"BEFORE","UPDATE","ROW","'"+name+"'","timestamp","UPDATE mdt SET id=id","09000",`"mdt" has no attribute "`+name+`"`})}
 for _,typ:=range []string{"integer","text","md_domain"} {cases=append(cases,struct{name,timing,event,level,args,typ,query,code,message string}{typ,"BEFORE","UPDATE","ROW","'stamp'",typ,"UPDATE mdt SET id=id","09000",`attribute "stamp" of "mdt" must be type TIMESTAMP or TIMESTAMPTZ`})}
 for _,tc:=range cases {t.Run(tc.name,func(t *testing.T){moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","CREATE DOMAIN md_domain AS timestamp","CREATE TABLE mdt(id integer,stamp "+tc.typ+")")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO mdt VALUES(1,NULL),(2,NULL)",2))
  if tc.timing!="" {mdExec(t,p,b,c,"CREATE TRIGGER a "+tc.timing+" "+tc.event+" ON mdt FOR EACH "+tc.level+" EXECUTE FUNCTION moddatetime("+tc.args+")");if tc.level=="ROW" {mdRun(t,p,b,c,fixture.Exec("UPDATE mdt SET id=id WHERE false",0))}}
  // Both statement timings and TRUNCATE enter the shared native frame.
  // The helper's row check precedes timing, event and argument validation.
  mdError(t,p,b,c,tc.query,tc.code,tc.message)
  mdRun(t,p,b,c,fixture.Select("SELECT id,stamp::text AS stamp FROM mdt ORDER BY id",[]string{"id","stamp"},[]string{"INT4","TEXT"},[][]any{{int64(1),nil},{int64(2),nil}}))
  mdExec(t,p,b,c,"BEGIN")
  mdRun(t,p,b,c,fixture.ErrorDiagnostic(tc.query,fixture.ControlsReferenceDiagnostic(tc.code,tc.message)))
  mdRun(t,p,b,c,fixture.ErrorDiagnostic("SELECT 1",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
  mdExec(t,p,b,c,"ROLLBACK")
 })})}
 t.Run("unqualified_relation",func(t *testing.T){moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","CREATE SCHEMA md_other","CREATE TABLE md_other.mdt(id integer,stamp timestamp)","CREATE TRIGGER strange BEFORE UPDATE ON md_other.mdt FOR EACH ROW EXECUTE FUNCTION moddatetime('missing')")
  mdRun(t,p,b,c,fixture.Exec("INSERT INTO md_other.mdt VALUES(1,NULL)",1));mdError(t,p,b,c,"UPDATE md_other.mdt SET id=id","09000",`"mdt" has no attribute "missing"`)
 })})
 t.Run("literal_identifier_diagnostics",func(t *testing.T){moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime","CREATE SCHEMA md_other",`CREATE TABLE md_other."md""t\name"(id integer,"sta""mp\field" integer)`)
  mdRun(t,p,b,c,fixture.Exec(`INSERT INTO md_other."md""t\name" VALUES(1,NULL),(2,NULL)`,2))
  for _,tc:=range []struct{name,args,message string}{
   {"missing",`'bad"name\part'`,`"md"t\name" has no attribute "bad"name\part"`},
   {"wrong_type",`'sta"mp\field'`,`attribute "sta"mp\field" of "md"t\name" must be type TIMESTAMP or TIMESTAMPTZ`},
  } {t.Run(tc.name,func(t *testing.T){
   mdExec(t,p,b,c,`CREATE TRIGGER quoted BEFORE UPDATE ON md_other."md""t\name" FOR EACH ROW EXECUTE FUNCTION moddatetime(`+tc.args+`)`)
   defer func(){
    if _,err:=c.ExecContext(p.Ctx,"ROLLBACK");err!=nil {t.Error(err)}
    if _,err:=c.ExecContext(p.Ctx,`DROP TRIGGER IF EXISTS quoted ON md_other."md""t\name"`);err!=nil {t.Error(err)}
   }()
   query:=`UPDATE md_other."md""t\name" SET id=id`
   mdError(t,p,b,c,query,"09000",tc.message)
   for _,prepared:=range []bool{false,true} {
    mdExec(t,p,b,c,"BEGIN")
    step:=fixture.ErrorDiagnostic(query,fixture.ControlsReferenceDiagnostic("09000",tc.message));step.Prepared=prepared;step.ErrorStage="execute";mdRun(t,p,b,c,step)
    mdRun(t,p,b,c,fixture.ErrorDiagnostic("SELECT 1",fixture.ControlsReferenceDiagnostic("25P02","current transaction is aborted, commands ignored until end of transaction block")))
    mdExec(t,p,b,c,"ROLLBACK")
   }
   mdRun(t,p,b,c,fixture.Select(`SELECT id,"sta""mp\field" AS stamp FROM md_other."md""t\name" ORDER BY id`,[]string{"id","stamp"},[]string{"INT4","INT4"},[][]any{{int64(1),nil},{int64(2),nil}}))
  })}
 })})
}
