package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func backendMemoryDenied(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,q string,function bool) {
	t.Helper()
	message:="permission denied for view pg_backend_memory_contexts"
	if function {message="permission denied for function pg_get_backend_memory_contexts"}
	s:=fixture.ImplicitError(q,fixture.Diagnostic{Code:"42501",Message:message})
	s.SelectRows=true;s.ErrorStage="execute"
	fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
}

func backendMemoryInitialPrivileges(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor) {
	t.Helper()
	owner:="postgres";if b.PG{owner="stuntgres"}
	for _,tc:=range []struct{class,object,acl string}{
		{"pg_class","'pg_catalog.pg_backend_memory_contexts'::regclass","{"+owner+"=arwdDxtm/"+owner+",pg_read_all_stats=r/"+owner+"}"},
		{"pg_proc","2282","{"+owner+"=X/"+owner+",pg_read_all_stats=X/"+owner+"}"},
	} {
		s:=fixture.Select("SELECT privtype::text,initprivs::text FROM pg_init_privs WHERE classoid='pg_catalog."+tc.class+"'::regclass AND objoid="+tc.object+" AND objsubid=0",[]string{"privtype","initprivs"},[]string{"TEXT","TEXT"},[][]any{{"i",tc.acl}})
		fixture.RunStep(t,p.Ctx,e,b.Recorder,s);fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Prepared(s))
	}
}

func backendMemoryPrivilegeFlags(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,table,function bool) {
	t.Helper()
	s:=fixture.Select("SELECT has_table_privilege('backend_memory_actor','pg_catalog.pg_backend_memory_contexts','SELECT') AS can_select,has_function_privilege('backend_memory_actor','pg_catalog.pg_get_backend_memory_contexts()','EXECUTE') AS can_execute",[]string{"can_select","can_execute"},[]string{"BOOL","BOOL"},[][]any{{table,function}})
	fixture.RunStep(t,p.Ctx,e,b.Recorder,s)
}

func backendMemoryRoutineVisibility(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,visible bool) {
	t.Helper()
	var want [][]any
	if visible {want=[][]any{{"pg_get_backend_memory_contexts_2282",nil}}}
	s:=fixture.Select("SELECT specific_name::text,routine_definition::text FROM information_schema.routines WHERE specific_schema='pg_catalog' AND specific_name='pg_get_backend_memory_contexts_2282'",[]string{"specific_name","routine_definition"},[]string{"TEXT","TEXT"},want)
	backendMemoryStepRoutes(t,p,b,e,s)
}

// These are real statements prepared on the same physical owner before any
// authority changes. Fresh Prepare is tested separately, never substituted.
func backendMemoryAuthorization(t *testing.T,p *fixture.Pair,b fixture.Backend,actor,admin *sql.Conn,full bool) {
	t.Helper()
	pgSequenceExec(t,p,b,admin,"CREATE ROLE backend_memory_actor")
	defer actor.ExecContext(context.Background(),"RESET SESSION AUTHORIZATION")
	defer admin.ExecContext(context.Background(),"REVOKE ALL ON pg_catalog.pg_backend_memory_contexts FROM backend_memory_actor")
	defer admin.ExecContext(context.Background(),"REVOKE ALL ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() FROM backend_memory_actor")
	view:=backendMemoryHeld(t,p,actor,backendMemoryView)
	function:=backendMemoryHeld(t,p,actor,backendMemoryFunction)
	before:=backendMemoryRead(t,p,b,actor,backendMemoryView,false)
	backendMemoryRead(t,p,b,view,backendMemoryView,false)
	backendMemoryRead(t,p,b,function,backendMemoryFunction,false)
	pgSequenceExec(t,p,b,actor,"SET SESSION AUTHORIZATION backend_memory_actor")
	for _,e:=range []fixture.Executor{actor,view} {backendMemoryDenied(t,p,b,e,backendMemoryView,false)}
	for _,e:=range []fixture.Executor{actor,function} {backendMemoryDenied(t,p,b,e,backendMemoryFunction,true)}
	backendMemoryPrivilegeFlags(t,p,b,admin,false,false)
	backendMemoryRoutineVisibility(t,p,b,actor,false)
	for _,q:=range []string{backendMemoryView,backendMemoryFunction} {
		fresh:=backendMemoryHeld(t,p,actor,q)
		backendMemoryDenied(t,p,b,fresh,q,q==backendMemoryFunction)
	}
	if full {
		for _,role:=range []string{"pg_read_all_stats","pg_monitor"} {
			pgSequenceExec(t,p,b,admin,"GRANT "+role+" TO backend_memory_actor")
			backendMemoryPrivilegeFlags(t,p,b,admin,true,true)
			for _,e:=range []fixture.Executor{actor,view} {backendMemoryRead(t,p,b,e,backendMemoryView,false)}
			backendMemoryRead(t,p,b,function,backendMemoryFunction,false)
			backendMemoryRoutineVisibility(t,p,b,actor,true)
			pgSequenceExec(t,p,b,admin,"REVOKE "+role+" FROM backend_memory_actor")
			backendMemoryDenied(t,p,b,view,backendMemoryView,false)
			backendMemoryDenied(t,p,b,function,backendMemoryFunction,true)
		}
		for _,grant:=range []string{"GRANT pg_read_all_stats TO backend_memory_actor WITH INHERIT FALSE, SET TRUE","GRANT pg_read_all_stats TO backend_memory_actor"} {
			if grant=="GRANT pg_read_all_stats TO backend_memory_actor" {pgSequenceExec(t,p,b,admin,"ALTER ROLE backend_memory_actor NOINHERIT")}
			pgSequenceExec(t,p,b,admin,grant)
			backendMemoryDenied(t,p,b,view,backendMemoryView,false)
			backendMemoryDenied(t,p,b,function,backendMemoryFunction,true)
			pgSequenceExec(t,p,b,actor,"SET ROLE pg_read_all_stats")
			after:=backendMemoryRead(t,p,b,view,backendMemoryView,false)
			backendMemoryRead(t,p,b,function,backendMemoryFunction,false)
			if !b.PG {backendMemorySameOwn(t,after,before)}
			pgSequenceExec(t,p,b,actor,"RESET ROLE")
			backendMemoryDenied(t,p,b,view,backendMemoryView,false)
			backendMemoryDenied(t,p,b,function,backendMemoryFunction,true)
			pgSequenceExec(t,p,b,admin,"REVOKE pg_read_all_stats FROM backend_memory_actor")
		}
		pgSequenceExec(t,p,b,admin,"ALTER ROLE backend_memory_actor INHERIT")
		// Mutate an actual seeded grant, not only a newly added actor ACL.
		// Always repair cluster-global authority even if an assertion fails.
		defer admin.ExecContext(context.Background(),"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() TO pg_read_all_stats")
		pgSequenceExec(t,p,b,admin,"GRANT pg_read_all_stats TO backend_memory_actor")
		backendMemoryRead(t,p,b,view,backendMemoryView,false)
		pgSequenceExec(t,p,b,admin,"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() FROM pg_read_all_stats")
		backendMemoryDenied(t,p,b,view,backendMemoryView,true)
		backendMemoryDenied(t,p,b,function,backendMemoryFunction,true)
		backendMemoryInitialPrivileges(t,p,b,admin)
		owner:="postgres";if b.PG{owner="stuntgres"}
		fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=2282",[]string{"proacl"},[]string{"TEXT"},[][]any{{"{"+owner+"=X/"+owner+"}"}}))
		pgSequenceExec(t,p,b,admin,"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() TO pg_read_all_stats")
		backendMemoryRead(t,p,b,function,backendMemoryFunction,false)
		pgSequenceExec(t,p,b,admin,"REVOKE pg_read_all_stats FROM backend_memory_actor")
		pgSequenceExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_backend_memory_contexts TO backend_memory_actor")
		backendMemoryPrivilegeFlags(t,p,b,admin,true,false)
		for _,q:=range []string{backendMemoryView,"SELECT name FROM pg_catalog.pg_backend_memory_contexts","SELECT count(*) FROM pg_catalog.pg_backend_memory_contexts"} {
			backendMemoryDenied(t,p,b,actor,q,true)
			fresh:=backendMemoryHeld(t,p,actor,q);backendMemoryDenied(t,p,b,fresh,q,true)
		}
		backendMemoryDenied(t,p,b,view,backendMemoryView,true)
		pgSequenceExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_backend_memory_contexts FROM backend_memory_actor")
	}
	pgSequenceExec(t,p,b,admin,"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() TO backend_memory_actor")
	backendMemoryPrivilegeFlags(t,p,b,admin,false,true)
	backendMemoryRead(t,p,b,actor,backendMemoryFunction,false)
	backendMemoryRead(t,p,b,actor,backendMemoryFunction,true)
	backendMemoryRead(t,p,b,function,backendMemoryFunction,false)
	backendMemoryRoutineVisibility(t,p,b,actor,true)
	backendMemoryDenied(t,p,b,view,backendMemoryView,false)
	pgSequenceExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_backend_memory_contexts TO backend_memory_actor")
	backendMemoryPrivilegeFlags(t,p,b,admin,true,true)
	after:=backendMemoryRead(t,p,b,view,backendMemoryView,false)
	backendMemoryRead(t,p,b,actor,backendMemoryView,true)
	if !b.PG {backendMemorySameOwn(t,after,before)}
	backendMemoryLiveACL(t,p,b,admin,true,true)
	pgSequenceExec(t,p,b,admin,"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() FROM backend_memory_actor")
	backendMemoryDenied(t,p,b,view,backendMemoryView,true)
	backendMemoryDenied(t,p,b,function,backendMemoryFunction,true)
	backendMemoryRoutineVisibility(t,p,b,actor,false)
	backendMemoryPrivilegeFlags(t,p,b,admin,true,false)
	backendMemoryLiveACL(t,p,b,admin,true,false)
	if full {
		pgSequenceExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_backend_memory_contexts FROM backend_memory_actor")
		pgSequenceExec(t,p,b,admin,"GRANT SELECT(name) ON pg_catalog.pg_backend_memory_contexts TO backend_memory_actor")
		backendMemoryDenied(t,p,b,actor,"SELECT name FROM pg_catalog.pg_backend_memory_contexts",true)
		pgSequenceExec(t,p,b,admin,"GRANT EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() TO backend_memory_actor")
		q:="SELECT name FROM pg_catalog.pg_backend_memory_contexts"
		columnHeld:=backendMemoryHeld(t,p,actor,q)
		for _,e:=range []fixture.Executor{actor,columnHeld} {
			rows,err:=e.QueryContext(p.Ctx,q);if err!=nil{t.Fatal(err)};count:=0
			for rows.Next(){var name string;if err:=rows.Scan(&name);err!=nil||name==""{t.Fatal(name,err)};count++};if rows.Err()!=nil||count==0{t.Fatal(count,rows.Err())};rows.Close()
		}
		backendMemoryDenied(t,p,b,view,backendMemoryView,false)
		pgSequenceExec(t,p,b,admin,"REVOKE EXECUTE ON FUNCTION pg_catalog.pg_get_backend_memory_contexts() FROM backend_memory_actor")
		backendMemoryDenied(t,p,b,columnHeld,q,true)
		pgSequenceExec(t,p,b,admin,"REVOKE SELECT(name) ON pg_catalog.pg_backend_memory_contexts FROM backend_memory_actor")
		backendMemoryDenied(t,p,b,columnHeld,q,false)
	}
	backendMemoryInitialPrivileges(t,p,b,admin)
	pgSequenceExec(t,p,b,actor,"RESET SESSION AUTHORIZATION")
	backendMemoryRead(t,p,b,view,backendMemoryView,false)
	backendMemoryRead(t,p,b,function,backendMemoryFunction,false)
}

func backendMemoryLiveACL(t *testing.T,p *fixture.Pair,b fixture.Backend,e fixture.Executor,table,function bool) {
	t.Helper()
	owner:="postgres";if b.PG{owner="stuntgres"}
	rel:="{"+owner+"=arwdDxtm/"+owner+",pg_read_all_stats=r/"+owner
	proc:="{"+owner+"=X/"+owner+",pg_read_all_stats=X/"+owner
	if table{rel+=",backend_memory_actor=r/"+owner};if function{proc+=",backend_memory_actor=X/"+owner};rel+="}";proc+="}"
	fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Select("SELECT relacl::text FROM pg_class WHERE oid='pg_catalog.pg_backend_memory_contexts'::regclass",[]string{"relacl"},[]string{"TEXT"},[][]any{{rel}}))
	fixture.RunStep(t,p.Ctx,e,b.Recorder,fixture.Select("SELECT proacl::text FROM pg_proc WHERE oid=2282",[]string{"proacl"},[]string{"TEXT"},[][]any{{proc}}))
}

func TestReferenceBackendMemoryPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"backend_memory_actor"})
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends(){t.Run(b.Name,func(t *testing.T){actor,admin:=fixture.LockConnections(t,p.Ctx,b.DB);backendMemorySetupSentinel(t,p,b,admin);backendMemoryAuthorization(t,p,b,actor,admin,true);backendMemorySentinel(t,p,b,admin)})}
}
