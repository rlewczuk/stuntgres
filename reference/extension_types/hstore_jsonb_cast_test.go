package reference_test

import (
 "database/sql"
 "testing"
 "stuntgres/reference/fixture"
)

func hstoreJSONBCatalogCheck(t *testing.T,p *fixture.Pair,b fixture.Backend,db fixture.Executor){
 t.Helper()
 hstoreRun(t,p,b,db,fixture.Select("SELECT castcontext,castmethod,castfunc='hstore_to_jsonb(hstore)'::regprocedure AS strict_function,oid>0 AS identity FROM pg_cast WHERE castsource='hstore'::regtype AND casttarget=3802",[]string{"castcontext","castmethod","strict_function","identity"},[]string{"CHAR","CHAR","BOOL","BOOL"},[][]any{{"e","f",true,true}}))
 hstoreRun(t,p,b,db,fixture.Select("SELECT deptype FROM pg_depend WHERE classid=2605 AND objid=(SELECT oid FROM pg_cast WHERE castsource='hstore'::regtype AND casttarget=3802) AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='hstore')",[]string{"deptype"},[]string{"CHAR"},[][]any{{"e"}}))
 hstoreRun(t,p,b,db,fixture.Select("SELECT refobjid='hstore'::regtype AS source,deptype FROM pg_depend WHERE classid=2605 AND objid=(SELECT oid FROM pg_cast WHERE castsource='hstore'::regtype AND casttarget=3802) AND refclassid=1247 AND refobjid='hstore'::regtype",[]string{"source","deptype"},[]string{"BOOL","CHAR"},[][]any{{true,"n"}}))
 hstoreRun(t,p,b,db,fixture.Select("SELECT deptype FROM pg_depend WHERE classid=2605 AND objid=(SELECT oid FROM pg_cast WHERE castsource='hstore'::regtype AND casttarget=3802) AND refclassid=1255 AND refobjid='hstore_to_jsonb(hstore)'::regprocedure",[]string{"deptype"},[]string{"CHAR"},[][]any{{"n"}}))
 hstoreRun(t,p,b,db,fixture.Select("SELECT castcontext FROM pg_cast WHERE castsource=3802 AND casttarget='hstore'::regtype",[]string{"castcontext"},[]string{"CHAR"},[][]any{}))
}

func TestReferenceHstoreJSONBCatalog(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){hstoreInstall(t,p,b,c);hstoreJSONBCatalogCheck(t,p,b,c)})}

func TestReferenceHstoreJSONBCast(t *testing.T){
 t.Run("explicit",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  for _,q:=range []string{"SELECT CAST($1::hstore AS jsonb) AS j","SELECT ($1::hstore)::jsonb AS j"}{
   stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close();held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
   for _,tc:=range []struct{arg,want any}{{"a=>1",`{"a": "1"}`},{"",`{}`},{nil,nil},{"a=>1e131072",`{"a": "1e131072"}`}}{
    s:=hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{tc.want}},false,tc.arg);hstoreRun(t,p,b,c,s);hstoreRun(t,p,b,c,fixture.Prepared(s));hstoreRun(t,p,b,held,s)
   }
   hstoreError(t,p,b,held,q,false,true,false,hstoreParameterDiagnostic("42601",`syntax error in hstore, near "b" at position 2`,1),"a=b")
   hstoreRun(t,p,b,held,hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{`{"a": "1"}`}},false,"a=>1"))
  }
  for _,q:=range []string{"SELECT CAST('a=>1'::hstore AS jsonb) AS j","SELECT ('a=>1'::hstore)::jsonb AS j"}{hstoreRun(t,p,b,c,hstoreJSONBSelect(b,q,[]string{"j"},[][]any{{`{"a": "1"}`}},false))}
  hstoreRun(t,p,b,c,fixture.Select(`SELECT ('b=>2,a=>1'::hstore)::jsonb='{"a":"1","b":"2"}'::jsonb AS same`,[]string{"same"},[]string{"BOOL"},[][]any{{true}}))
 })})
 t.Run("stored_source_transport",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_jsonb_capture(id integer,v hstore)",0))
  hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_jsonb_capture VALUES (1,'a=>1'),(2,NULL)",2))
  hstoreRun(t,p,b,c,fixture.Exec("CREATE VIEW hs_jsonb_capture_view AS SELECT id,CAST(v AS jsonb) AS standard,v::jsonb AS postfix,CAST('a=>NULL'::hstore AS jsonb) AS literal,CAST(NULL::hstore AS jsonb) AS typed_null FROM hs_jsonb_capture",0))
  q:="SELECT id,standard,postfix,literal,typed_null FROM hs_jsonb_capture_view ORDER BY id"
  s:=hstoreJSONBSelect(b,q,[]string{"id","standard","postfix","literal","typed_null"},[][]any{
   {int64(1),`{"a": "1"}`,`{"a": "1"}`,`{"a": null}`,nil},
   {int64(2),nil,nil,`{"a": null}`,nil},
  },true)
  hstoreRun(t,p,b,c,s)
  hstoreRun(t,p,b,c,fixture.Prepared(s))
  stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil{t.Fatal(err)};defer stmt.Close()
  hstoreRun(t,p,b,fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q},s)
 })})
 for _,input:=range []string{"'a=>1'::hstore","NULL::hstore"}{t.Run("contexts/"+input,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_jsonb_assign(v jsonb)",0))
  hstoreRun(t,p,b,c,fixture.Exec("CREATE FUNCTION hs_jsonb_accept(jsonb) RETURNS jsonb LANGUAGE SQL AS $$SELECT $1$$",0))
  for _,q:=range []string{"INSERT INTO hs_jsonb_assign VALUES ("+input+")","SELECT hs_jsonb_accept("+input+")"}{
   want:=fixture.ControlsReferenceDiagnostic("42804",`column "v" is of type jsonb but expression is of type hstore`)
   want.Hint="You will need to rewrite or cast the expression."
   want.Position="37"
   query:=false
   if q[0]=='S' {
    query=true
    want=fixture.ControlsReferenceDiagnostic("42883","function hs_jsonb_accept(hstore) does not exist")
    want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
    want.Position="8"
   }
   for _,prepared:=range []bool{false,true}{hstoreError(t,p,b,c,q,prepared,query,true,want)}
  }
  hstoreRun(t,p,b,c,hstoreJSONBSelect(b,"SELECT v FROM hs_jsonb_assign",[]string{"v"},[][]any{},true))
  hstoreRun(t,p,b,c,fixture.Exec("INSERT INTO hs_jsonb_assign VALUES (("+input+")::jsonb)",1))
 })})}
}

func TestReferenceHstoreJSONBCastLifecycle(t *testing.T){
 for _,q:=range []string{"DROP CAST (hstore AS jsonb)","DROP FUNCTION hstore_to_jsonb(hstore)","DROP TYPE hstore"}{t.Run(q,func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  subject:="type hstore"
  if q=="DROP CAST (hstore AS jsonb)"{subject="cast from hstore to jsonb"}
  if q=="DROP FUNCTION hstore_to_jsonb(hstore)"{subject="function hstore_to_jsonb(hstore)"}
  want:=fixture.ControlsReferenceDiagnostic("2BP01","cannot drop "+subject+" because extension hstore requires it")
  want.Hint="You can drop extension hstore instead."
  hstoreError(t,p,b,c,q,false,false,false,want)
  hstoreJSONBCatalogCheck(t,p,b,c)
 })})}
 t.Run("reinstall",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c);var old int64
  if err:=c.QueryRowContext(p.Ctx,"SELECT oid FROM pg_cast WHERE castsource='hstore'::regtype AND casttarget=3802").Scan(&old);err!=nil{t.Fatal(err)}
  hstoreRun(t,p,b,c,fixture.Exec("DROP EXTENSION hstore",0))
  hstoreRun(t,p,b,c,fixture.Select("SELECT oid::integer AS oid FROM pg_cast WHERE oid=$1",[]string{"oid"},[]string{"INT4"},[][]any{},old))
  hstoreInstall(t,p,b,c);hstoreJSONBCatalogCheck(t,p,b,c)
  hstoreRun(t,p,b,c,fixture.Select("SELECT oid<>$1 AS fresh FROM pg_cast WHERE castsource='hstore'::regtype AND casttarget=3802",[]string{"fresh"},[]string{"BOOL"},[][]any{{true}},old))
  hstoreRun(t,p,b,c,hstoreJSONBPair(b,hstoreJSONBQuery("'a=>t'"),[][]any{{`{"a": "t"}`,`{"a": true}`}}))
 })})
 t.Run("schema_cascade",func(t *testing.T){fixture.SchemaReferenceCleanup(t,"hs_jsonb_retire");hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreRun(t,p,b,c,fixture.Exec("CREATE SCHEMA hs_jsonb_retire",0));hstoreRun(t,p,b,c,fixture.Exec("CREATE EXTENSION hstore SCHEMA hs_jsonb_retire",0))
  hstoreRun(t,p,b,c,fixture.Exec("DROP SCHEMA hs_jsonb_retire CASCADE",0));hstoreInstall(t,p,b,c);hstoreJSONBCatalogCheck(t,p,b,c)
 })})
 t.Run("dependent_consumer",func(t *testing.T){hstoreScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  hstoreInstall(t,p,b,c)
  hstoreRun(t,p,b,c,fixture.Exec("CREATE TABLE hs_jsonb_dependent(v hstore)",0))
  hstoreRun(t,p,b,c,fixture.Exec("CREATE VIEW hs_jsonb_dependent_view AS SELECT v::jsonb AS j FROM hs_jsonb_dependent",0))
  want:=fixture.ControlsReferenceDiagnostic("2BP01","cannot drop extension hstore because other objects depend on it")
  want.Detail="column v of table hs_jsonb_dependent depends on type hstore\nview hs_jsonb_dependent_view depends on column v of table hs_jsonb_dependent"
  want.Hint="Use DROP ... CASCADE to drop the dependent objects too."
  hstoreError(t,p,b,c,"DROP EXTENSION hstore",false,false,false,want)
  hstoreJSONBCatalogCheck(t,p,b,c)
  hstoreRun(t,p,b,c,fixture.Exec("DROP VIEW hs_jsonb_dependent_view",0))
  hstoreRun(t,p,b,c,fixture.Exec("DROP TABLE hs_jsonb_dependent",0))
  hstoreRun(t,p,b,c,fixture.Exec("DROP EXTENSION hstore",0))
  hstoreInstall(t,p,b,c);hstoreJSONBCatalogCheck(t,p,b,c)
 })})
}

