package reference_test

import (
 "database/sql"
 "strconv"
 "strings"
 "testing"

 "stuntgres/reference/fixture"
)

func citextRegexpCatalogChecks(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
 t.Helper()
 for _,d:=range citextRegexpDeclarations {
 signature:="public."+d.name+"("+strings.Join(d.args,",")+")"
 rows,err:=c.QueryContext(p.Ctx,"SELECT oid::bigint FROM pg_proc WHERE oid=$1::regprocedure::oid",signature);if err!=nil {t.Fatal(err)}
 var oid int64;if !rows.Next(){rows.Close();t.Fatal("missing",signature)};if err:=rows.Scan(&oid);err!=nil {t.Fatal(err)};if rows.Next(){t.Fatal("duplicate identity")};if err:=rows.Close();err!=nil {t.Fatal(err)};if oid<=0 {t.Fatal("invalid identity",oid)}
 result:="text";if d.result=="_TEXT" {result="text[]"}
 names:=[]string{"string","pattern"};if d.name=="regexp_replace" {names=append(names,"replacement")};if len(names)<len(d.args){names=append(names,"flags")}
 q:="SELECT p.proname,p.pronargs,p.prorettype=$2::regtype::oid AS result,p.proisstrict,p.provolatile,p.proparallel,p.prokind,p.proargnames,p.proretset,p.prorows,p.pronamespace=t.typnamespace AS namespace,p.proowner=t.typowner AS owner,d.deptype FROM (SELECT * FROM pg_proc WHERE oid=$1::oid) p JOIN (SELECT * FROM pg_type WHERE oid='citext'::regtype::oid) t ON t.typnamespace=p.pronamespace JOIN (SELECT * FROM pg_depend WHERE classid=1255 AND refclassid=3079 AND refobjid=(SELECT oid FROM pg_extension WHERE extname='citext')) d ON d.objid=p.oid"
 citextPatternRoutes(t,p,b,c,fixture.Select(q,[]string{"proname","pronargs","result","proisstrict","provolatile","proparallel","prokind","proargnames","proretset","prorows","namespace","owner","deptype"},[]string{"NAME","INT2","BOOL","BOOL","CHAR","CHAR","CHAR","_TEXT","BOOL","FLOAT4","BOOL","BOOL","CHAR"},[][]any{{d.name,int64(len(d.args)),true,true,"i","s","f","{"+strings.Join(names,",")+"}",d.set,d.rows,true,true,"e"}},oid,result))
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT external_language,routine_definition,external_name FROM information_schema.routines WHERE specific_schema='public' AND specific_name=$1",[]string{"external_language","routine_definition","external_name"},[]string{"VARCHAR","VARCHAR","VARCHAR"},[][]any{{"SQL","",nil}},d.name+"_"+strconv.FormatInt(oid,10)))
 }
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT count(*) AS n FROM pg_proc WHERE pronamespace='public'::regnamespace::oid AND proname IN ('regexp_match','regexp_matches','regexp_replace','regexp_split_to_array','regexp_split_to_table')",[]string{"n"},[]string{"INT8"},[][]any{{int64(10)}}))
}
func TestReferenceCitextRegexpCatalog(t *testing.T) {
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c);citextRegexpCatalogChecks(t,p,b,c)
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT oid::bigint AS oid,proretset,prorows FROM pg_proc WHERE oid IN (1067,1689,2331,2763,2764,3219,3396,5064,6159) ORDER BY oid",[]string{"oid","proretset","prorows"},[]string{"INT8","BOOL","FLOAT4"},[][]any{{int64(1067),true,float64(1000)},{int64(1689),true,float64(10)},{int64(2331),true,float64(100)},{int64(2763),true,float64(1)},{int64(2764),true,float64(10)},{int64(3219),true,float64(100)},{int64(3396),false,float64(0)},{int64(5064),true,float64(50)},{int64(6159),true,float64(250)}}))
 citextPatternRoutes(t,p,b,c,fixture.Select("SELECT attname,attnum,atttypid='bool'::regtype::oid AS boolean_type FROM pg_attribute WHERE attrelid='pg_catalog.pg_proc'::regclass::oid AND attname IN ('prorows','proretset') ORDER BY attnum",[]string{"attname","attnum","boolean_type"},[]string{"NAME","INT2","BOOL"},[][]any{{"prorows",int64(7),false},{"proretset",int64(14),true}}))
 })
}
func TestReferenceCitextRegexpPrivileges(t *testing.T) {
 fixture.RoleReferenceCleanup(t,[]string{"cr_reader"})
 citextPatternScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
 citextInstall(t,p,b,c)
 run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
 run(fixture.Exec("CREATE ROLE cr_reader",0));run(fixture.Exec("GRANT USAGE ON SCHEMA public TO cr_reader",0))
 defer c.ExecContext(p.Ctx,"RESET ROLE")
 for _,name:=range []string{"regexp_match","regexp_matches"} {
 signature:=name+"(citext,citext)";run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM PUBLIC",0))
 q:="SELECT public."+name+"($1::citext,$2::citext) AS v"
 stmt,err:=c.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)}
 held:=fixture.HeldStatement{Executor:c,Statement:stmt,SQL:q}
 good:=citextRegexpValue(q,"_TEXT","{A}","AbA","a")
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 run(fixture.Exec("SET ROLE cr_reader",0))
 denied:=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+name),"AbA","a");denied.SelectRows=true;denied.ErrorStage="execute"
 run(denied);fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
 // A sibling overload is not revoked.
 citextPatternRoutes(t,p,b,c,citextRegexpValue("SELECT public."+name+"('AbA'::citext,'a','i') AS v","_TEXT","{A}"))
 run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("GRANT EXECUTE ON FUNCTION "+signature+" TO cr_reader",0));run(fixture.Exec("SET ROLE cr_reader",0))
 fixture.RunStep(t,p.Ctx,held,b.Recorder,good)
 citextPatternRoutes(t,p,b,c,good)
 run(fixture.Exec("RESET ROLE",0));run(fixture.Exec("REVOKE EXECUTE ON FUNCTION "+signature+" FROM cr_reader",0));run(fixture.Exec("SET ROLE cr_reader",0))
 fixture.RunStep(t,p.Ctx,held,b.Recorder,denied)
 // PostgreSQL removes strict scalar NULL calls, but still initializes an SRF.
 nullStep:=citextRegexpValue(q,"_TEXT",nil,nil,"[")
 if name=="regexp_matches" {
 nullStep=fixture.ErrorDiagnostic(q,fixture.ControlsReferenceDiagnostic("42501","permission denied for function "+name),nil,"[")
 nullStep.SelectRows=true;nullStep.ErrorStage="execute"
 }
 fixture.RunStep(t,p.Ctx,held,b.Recorder,nullStep)
 citextPatternRoutes(t,p,b,c,nullStep)
 run(fixture.Exec("RESET ROLE",0));stmt.Close()
 }
 run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM PUBLIC",0))
 run(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM cr_reader",0))
 run(fixture.Exec("SET ROLE cr_reader",0))
 query:="SELECT public.regexp_match('AbA'::public.citext,'a') AS v"
 namespace:=fixture.ControlsReferenceDiagnostic("42501","permission denied for schema public")
 namespace.Position=citextPosition(query,"public.citext")
 failure:=fixture.ErrorDiagnostic(query,namespace);failure.SelectRows=true
 citextPatternRoutes(t,p,b,c,failure)
 run(fixture.Exec("RESET ROLE",0))
 })
}
