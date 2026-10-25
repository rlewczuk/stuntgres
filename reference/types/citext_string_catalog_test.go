package reference_test

import (
 "database/sql"
 "strconv"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func citextStringArityChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,d citextStringDeclaration) {
 t.Helper()
 citextStringRoutes(t,p,b,c,citextStringValue(d.query(),d.result,d.expected,d.values...))
 // Qualified identity lookup binds every formal to the current generation.
 // Shortened prefixes prove no defaults; explicit array VARIADIC and an extra
 // scalar argument prove that this is a fixed-arity declaration.
 literals:=make([]string,len(d.args));for i,typ:=range d.args {literals[i]="NULL::"+typ}
 missing:=func(arguments,types []string) {
  q:="SELECT public."+d.name+"("+strings.Join(arguments,",")+") AS v"
  want:=fixture.ControlsReferenceDiagnostic("42883","function public."+d.name+"("+strings.Join(types,", ")+") does not exist")
  want.Position="8";want.Hint="No function matches the given name and argument types. You might need to add explicit type casts."
  for _,prepared:=range []bool{false,true} {citextError(t,p,b,c,q,prepared,true,true,want)}
 }
 for count:=range literals {missing(literals[:count],d.args[:count])}
 missing(append(append([]string(nil),literals...),"NULL::text"),append(append([]string(nil),d.args...),"text"))
 variadic:=append(append([]string(nil),literals[:len(literals)-1]...),"VARIADIC NULL::text[]")
 types:=append(append([]string(nil),d.args[:len(d.args)-1]...),"text[]")
 missing(variadic,types)
}

func citextStringCatalogChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _,d:=range citextStringDeclarations {
  rows,err:=c.QueryContext(p.Ctx,"SELECT oid::bigint FROM pg_proc WHERE oid=$1::regprocedure::oid",d.signature());if err!=nil {t.Fatal(err)}
  var oid int64
  if !rows.Next() {rows.Close();t.Fatal("missing declaration",d.signature())}
  if err:=rows.Scan(&oid);err!=nil {t.Fatal(err)};if rows.Next(){t.Fatal("duplicate routine identity")};if err:=rows.Err();err!=nil {t.Fatal(err)};rows.Close()
  if oid<=0 {t.Fatal("invalid routine identity",oid)}
  result:="text";if d.result=="INT4" {result="integer"}
  q:="SELECT p.proname,p.pronargs,p.prorettype=$2::regtype::oid AS result,p.proisstrict,p.provolatile,p.proparallel,p.prokind,p.proargnames,p.proretset,p.prorows,p.prolang=(SELECT oid FROM pg_language WHERE lanname='sql') AS language,p.pronamespace=t.typnamespace AS namespace,p.proowner=t.typowner AS owner,d.deptype FROM (SELECT * FROM pg_proc WHERE oid=$1::oid) p JOIN (SELECT * FROM pg_type WHERE oid='citext'::regtype::oid) t ON t.typnamespace=p.pronamespace JOIN (SELECT * FROM pg_depend WHERE classid=1255 AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='citext')) d ON d.objid=p.oid"
  citextStringRoutes(t,p,b,c,fixture.Select(q,[]string{"proname","pronargs","result","proisstrict","provolatile","proparallel","prokind","proargnames","proretset","prorows","language","namespace","owner","deptype"},[]string{"NAME","INT2","BOOL","BOOL","CHAR","CHAR","CHAR","_TEXT","BOOL","FLOAT4","BOOL","BOOL","BOOL","CHAR"},[][]any{{d.name,int64(len(d.args)),true,true,"i","s","f",nil,false,float64(0),true,true,true,"e"}},oid,result))
  citextStringArityChecks(t,p,b,c,d)
  citextStringRoutes(t,p,b,c,fixture.Select("SELECT external_language,routine_definition,external_name FROM information_schema.routines WHERE specific_schema='public' AND specific_name=$1",[]string{"external_language","routine_definition","external_name"},[]string{"VARCHAR","VARCHAR","VARCHAR"},[][]any{{"SQL","",nil}},d.name+"_"+strconv.FormatInt(oid,10)))
 }
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace::oid AND proname IN ('strpos','replace','split_part','translate')",[]string{"n"},[]string{"INT8"},[][]any{{int64(4)}}))
 citextStringRoutes(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace::oid AND proname IN ('regexp_match','regexp_matches','regexp_replace','regexp_split_to_array','regexp_split_to_table')",[]string{"n"},[]string{"INT8"},[][]any{{int64(10)}}))
}
func TestReferenceCitextStringCatalog(t *testing.T) {
 t.Run("declarations",func(t *testing.T){citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  citextInstall(t,p,b,c);citextStringCatalogChecks(t,p,b,c)
  for _,d:=range citextStringDeclarations {
   signature:=d.signature()
   var oid int64
   rows,err:=c.QueryContext(p.Ctx,"SELECT $1::regprocedure::oid::bigint",signature);if err!=nil {t.Fatal(err)}
   if !rows.Next() {rows.Close();t.Fatal("missing current routine identity")}
   if err:=rows.Scan(&oid);err!=nil {rows.Close();t.Fatal(err)}
   if rows.Next() {rows.Close();t.Fatal("duplicate current identity")}
   if err:=rows.Err();err!=nil {rows.Close();t.Fatal(err)};rows.Close()
   citextStringRoutes(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM information_schema.routine_privileges WHERE specific_schema='public' AND specific_name=$1 AND grantee='PUBLIC' AND privilege_type='EXECUTE' AND is_grantable='NO'",[]string{"n"},[]string{"INT8"},[][]any{{int64(1)}},d.name+"_"+strconv.FormatInt(oid,10)))
  }
 })})
 t.Run("trusted_installer",func(t *testing.T){
 fixture.RoleReferenceCleanup(t,[]string{"cs_installer"})
 citextStringScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
  run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
  run(fixture.Exec("CREATE USER cs_installer NOSUPERUSER",0));run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cs_installer",0))
  var database string;if err:=c.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database);err!=nil {t.Fatal(err)}
  run(fixture.Exec("GRANT CREATE ON DATABASE "+citextIdentifier(database)+" TO cs_installer",0))
  defer c.ExecContext(p.Ctx,"REVOKE CREATE ON DATABASE "+citextIdentifier(database)+" FROM cs_installer")
  run(fixture.Exec("SET SESSION AUTHORIZATION cs_installer",0));defer c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
  citextInstall(t,p,b,c);run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
  citextStringCatalogChecks(t,p,b,c)
  for _,d:=range citextStringDeclarations {
   citextStringRoutes(t,p,b,c,citextPatternBool("SELECT p.proowner<>e.extowner AND r.rolsuper AS v FROM (SELECT * FROM pg_proc WHERE oid=$1::regprocedure::oid) p JOIN pg_extension e ON e.extname='citext' JOIN pg_roles r ON r.oid=p.proowner",true,d.signature()))
  }
 })})
}
