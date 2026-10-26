package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func normalMissing(q,name,position string)fixture.Step {
	return fixture.ImplicitError(q,fixture.Diagnostic{Code:"42883",Message:"function "+name+"(integer, integer, integer) does not exist",Hint:"No function matches the given name and argument types. You might need to add explicit type casts.",Position:position})
}
func normalInstallBoundaries(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
	t.Helper()
	run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	q:="SELECT * FROM normal_part.normal_rand(1,9,0)"
	var original int64
	if err:=c.QueryRowContext(p.Ctx,"SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='normal_part' AND p.proname='normal_rand'").Scan(&original);err!=nil{t.Fatal(err)}
	run(fixture.ImplicitError("CREATE EXTENSION tablefunc",fixture.Diagnostic{Code:"42710",Message:`extension "tablefunc" already exists`}))
	run(fixture.Exec("DROP EXTENSION tablefunc",0))
	missing:=normalMissing(q,"normal_part.normal_rand","15");run(missing);missing=fixture.Prepared(missing);missing.ErrorStage="prepare";run(missing)
	run(fixture.Exec("BEGIN",0));run(fixture.Exec("CREATE EXTENSION tablefunc WITH SCHEMA normal_part",0));run(normalSelect(q,1,9));run(fixture.Exec("ROLLBACK",0));run(normalMissing(q,"normal_part.normal_rand","15"))
	run(fixture.Exec("CREATE EXTENSION tablefunc WITH SCHEMA normal_part",0))
	var current int64
	if err:=c.QueryRowContext(p.Ctx,"SELECT p.oid::bigint FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='normal_part' AND p.proname='normal_rand'").Scan(&current);err!=nil||current==original{t.Fatal("reinstall identity",original,current,err)}
	catalogQuery:="SELECT p.proargnames IS NULL AS unnamed,p.pronargdefaults::integer AS defaults,p.provariadic::integer AS variadic,l.lanname AS language,d.refobjid=e.oid AS member FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid AND d.deptype='e' JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='normal_part' AND p.proname='normal_rand' AND e.extname='tablefunc'"
	s:=fixture.Select(catalogQuery,[]string{"unnamed","defaults","variadic","language","member"},[]string{"BOOL","INT4","INT4","NAME","BOOL"},[][]any{{true,int64(0),int64(0),"c",true}});run(s);run(fixture.Prepared(s))
	run(fixture.Exec("SET search_path TO public",0));run(normalMissing("SELECT normal_rand(1,9,0)","normal_rand","8"))
	run(fixture.Exec("RESET search_path",0))
}
func normalDropDependency(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
	t.Helper()
	run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,c,b.Recorder,s)}
	run(fixture.ImplicitError("DROP EXTENSION tablefunc RESTRICT",fixture.Diagnostic{Code:"2BP01",Message:"cannot drop extension tablefunc because other objects depend on it",Detail:"view normal_saved depends on function normal_moved.normal_renamed(integer,double precision,double precision)",Hint:"Use DROP ... CASCADE to drop the dependent objects too."}))
	run(fixture.Exec("DROP EXTENSION tablefunc CASCADE",0))
	run(fixture.Select("SELECT count(*) AS n FROM pg_class WHERE relname='normal_saved'",[]string{"n"},[]string{"INT8"},[][]any{{int64(0)}}))
	run(fixture.Exec("CREATE EXTENSION tablefunc WITH SCHEMA normal_part",0))
	run(normalSelect("SELECT * FROM normal_part.normal_rand(3,9,0)",3,9))
}
