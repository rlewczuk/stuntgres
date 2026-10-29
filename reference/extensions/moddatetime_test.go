package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func TestReferenceModdatetimeInstall(t *testing.T) {
 moddatetimeCase(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  mdExec(t,p,b,c,"CREATE SCHEMA md_other","BEGIN","CREATE EXTENSION moddatetime WITH SCHEMA md_other")
  mdRun(t,p,b,c,fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='moddatetime'",[]string{"extversion","extrelocatable"},[]string{"TEXT","BOOL"},[][]any{{"1.0",true}}))
  mdExec(t,p,b,c,"ROLLBACK")
  mdRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='moddatetime'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
  mdExec(t,p,b,c,"CREATE EXTENSION moddatetime WITH SCHEMA md_other")
  mdRun(t,p,b,c,fixture.Select("SELECT obj_description(oid,'pg_extension') AS comment FROM pg_extension WHERE extname='moddatetime'",[]string{"comment"},[]string{"TEXT"},[][]any{{"functions for tracking last modification time"}}))
  mdRun(t,p,b,c,fixture.Select("SELECT p.proname,p.pronargs::integer,p.prorettype::integer,p.prosrc,p.probin,p.provolatile::text,p.proisstrict,p.prosecdef,p.proparallel::text,l.lanname FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass JOIN pg_extension e ON e.oid=d.refobjid AND d.refclassid='pg_extension'::regclass WHERE e.extname='moddatetime' AND d.deptype='e' ORDER BY p.proname",[]string{"proname","pronargs","prorettype","prosrc","probin","provolatile","proisstrict","prosecdef","proparallel","lanname"},[]string{"NAME","INT4","INT4","TEXT","TEXT","TEXT","BOOL","BOOL","TEXT","NAME"},[][]any{{"moddatetime",int64(0),int64(2279),"moddatetime","$libdir/moddatetime","v",false,false,"u","c"}}))
  mdRun(t,p,b,c,fixture.Select("SELECT p.oid>0 AS valid,p.pronamespace=e.extnamespace AS placed FROM pg_proc p JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass JOIN pg_extension e ON e.oid=d.refobjid AND d.refclassid='pg_extension'::regclass WHERE e.extname='moddatetime' AND d.deptype='e'",[]string{"valid","placed"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
  mdRun(t,p,b,c,fixture.ErrorDiagnostic("CREATE EXTENSION moddatetime",fixture.ControlsReferenceDiagnostic("42710",`extension "moddatetime" already exists`)))
  mdExec(t,p,b,c,"DROP EXTENSION moddatetime","CREATE FUNCTION md_other.moddatetime() RETURNS integer LANGUAGE SQL AS 'SELECT 1'")
  collision:=fixture.ErrorDiagnostic("CREATE EXTENSION moddatetime WITH SCHEMA md_other",fixture.ControlsReferenceDiagnostic("42723",`function "moddatetime" already exists with same argument types`))
  // PostgreSQL executes the released installation script; native installation
  // stages the same logical declaration without pretending to execute C/SQL files.
  pgCollision:=*collision.Diagnostic
  pgCollision.Context="SQL statement \"CREATE FUNCTION moddatetime()\nRETURNS trigger\nAS '$libdir/moddatetime'\nLANGUAGE C\"\nextension script file \"moddatetime--1.0.sql\", near line 6"
  collision.PostgresDiagnostic=&pgCollision
  mdRun(t,p,b,c,collision)
  mdRun(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='moddatetime'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
 })
}
