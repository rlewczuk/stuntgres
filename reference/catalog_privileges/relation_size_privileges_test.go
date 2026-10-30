package reference_test

import (
	"context"
	"database/sql"
	"strings"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferenceRelationSizePrivileges(t *testing.T) {
	roles:=[]string{"rs_size_reader","rs_size_group","rs_size_noinherit"}
	fixture.RoleReferenceCleanup(t,roles)
	dbs:=[]string{"rs_size_target"}
	fixture.DatabaseReferenceCleanup(t,&dbs)
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		conn,err:=b.DB.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer conn.Close()
		run:=func(s fixture.Step){fixture.RunStep(t,p.Ctx,conn,b.Recorder,s)}
		for _,role:=range roles {run(fixture.Exec("CREATE ROLE "+role,0))}
		relationSizeRestoreTablespaceACL(t,p,b,roles)
		run(fixture.Exec("CREATE DATABASE rs_size_target",0))
		run(fixture.Exec("REVOKE CONNECT ON DATABASE rs_size_target FROM PUBLIC",0))
		run(fixture.Exec("CREATE TABLE public.rs_secret(i INT)",0))
		run(fixture.Exec("INSERT INTO public.rs_secret VALUES(1),(2)",2))
		run(fixture.Exec("ALTER TABLE public.rs_secret ENABLE ROW LEVEL SECURITY",0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rs_size_reader",0))
		allowed:=func(q string){run(fixture.Select("SELECT "+q+">=0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))}
		denied:=func(q,label,name string,prepared bool){s:=relationSizeBodyError("SELECT "+q,"42501","permission denied for "+label+" "+name,"");s.Prepared=prepared;run(s)}
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_reader",0))
		for _,prepared:=range []bool{false,true} {
			denied("pg_database_size('rs_size_target')","database","rs_size_target",prepared)
			denied("pg_tablespace_size('pg_global')","tablespace","pg_global",prepared)
		}
		allowed("pg_database_size(current_database())")
		allowed("pg_tablespace_size('pg_default')")
		allowed("pg_table_size('public.rs_secret')") // No SELECT privilege and no RLS policy.
		run(fixture.Select("SELECT pg_table_size('public.rs_secret')>0 AS v",[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		if !b.PG {run(relationSizeSelect("SELECT pg_table_size('public.rs_secret') AS v",[][]any{{int64(153)}}))}
		run(fixture.Exec("SET default_tablespace='pg_global'",0))
		allowed("pg_tablespace_size('pg_default')")
		denied("pg_tablespace_size('pg_global')","tablespace","pg_global",false)
		run(fixture.Exec("RESET default_tablespace",0))
		for _,q:=range []string{"SELECT pg_database_size(NULL) AS v","SELECT pg_tablespace_size(NULL) AS v"} {run(relationSizeSelect(q,[][]any{{nil}}))}
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Exec("GRANT CONNECT ON DATABASE rs_size_target TO rs_size_reader",0))
		run(fixture.Exec("GRANT CREATE ON TABLESPACE pg_global TO rs_size_reader",0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_reader",0))
		q:="SELECT pg_database_size('rs_size_target')>=0 AS v"
		stmt,err:=conn.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:conn,Statement:stmt,SQL:q}
		fixture.RunStep(t,p.Ctx,held,b.Recorder,fixture.Select(q,[]string{"v"},[]string{"BOOL"},[][]any{{true}}))
		allowed("pg_tablespace_size('pg_global')")
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Exec("REVOKE CONNECT ON DATABASE rs_size_target FROM rs_size_reader",0))
		run(fixture.Exec("REVOKE CREATE ON TABLESPACE pg_global FROM rs_size_reader",0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_reader",0))
		fixture.RunStep(t,p.Ctx,held,b.Recorder,relationSizeBodyError(q,"42501","permission denied for database rs_size_target",""))
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		for _,grant:=range []string{"GRANT pg_read_all_stats TO rs_size_reader","GRANT pg_read_all_stats TO rs_size_group; GRANT rs_size_group TO rs_size_reader"} {
			run(fixture.Exec(grant,0))
			run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_reader",0))
			allowed("pg_database_size('rs_size_target')");allowed("pg_tablespace_size('pg_global')")
			run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
			if grant=="GRANT pg_read_all_stats TO rs_size_reader" {run(fixture.Exec("REVOKE pg_read_all_stats FROM rs_size_reader",0))} else {run(fixture.Exec("REVOKE rs_size_group FROM rs_size_reader",0))}
		}
		run(fixture.Exec("GRANT pg_read_all_stats TO rs_size_noinherit WITH INHERIT FALSE",0))
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_noinherit",0))
		denied("pg_database_size('rs_size_target')","database","rs_size_target",false)
		denied("pg_tablespace_size('pg_global')","tablespace","pg_global",true)
		for _,tc:=range []struct{call,code,message string}{
			{"pg_database_size(0::oid)","42704","database with OID 0 does not exist"},
			{"pg_tablespace_size(0::oid)","42704","tablespace with OID 0 does not exist"},
			{"pg_database_size('rs_missing_db')","3D000",`database "rs_missing_db" does not exist`},
			{"pg_tablespace_size('rs_missing_ts')","42704",`tablespace "rs_missing_ts" does not exist`},
		} {run(relationSizeBodyError("SELECT "+tc.call,tc.code,tc.message,""))}
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		// Common routine EXECUTE admission is not replaced by the body's ACL.
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.pg_relation_size(regclass) FROM PUBLIC",0))
		defer func(){if _,err:=conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION; GRANT EXECUTE ON FUNCTION pg_catalog.pg_relation_size(regclass) TO PUBLIC");err!=nil {t.Error(err)}}()
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_reader",0))
		s:=relationSizeBodyError("SELECT pg_relation_size('public.rs_secret')","42501","permission denied for function pg_relation_size","");s.Prepared=true;run(s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_relation_size(regclass) TO PUBLIC",0))
		wrapperSQL:="SELECT pg_relation_size($1::regclass) AS v"
		wrapper,err:=conn.PrepareContext(p.Ctx,wrapperSQL);if err!=nil{t.Fatal(err)};defer wrapper.Close()
		wrapperHeld:=fixture.HeldStatement{Executor:conn,Statement:wrapper,SQL:wrapperSQL}
		run(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.pg_relation_size(regclass,text) FROM PUBLIC",0))
		defer func(){if _,err:=conn.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION; GRANT EXECUTE ON FUNCTION pg_catalog.pg_relation_size(regclass,text) TO PUBLIC");err!=nil{t.Error(err)}}()
		run(fixture.Exec("SET SESSION AUTHORIZATION rs_size_reader",0))
		innerDenied:=relationSizeBodyError(wrapperSQL,"42501","permission denied for function pg_relation_size","")
		innerDenied.Args=[]any{"public.rs_secret"}
		fixture.RunStep(t,p.Ctx,wrapperHeld,b.Recorder,innerDenied)
		innerDenied.Prepared=true;run(innerDenied)
		run(relationSizeSelect("SELECT pg_relation_size(NULL::regclass) AS v",[][]any{{nil}}))
		fixture.RunStep(t,p.Ctx,wrapperHeld,b.Recorder,relationSizeSelect(wrapperSQL,[][]any{{nil}},nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION",0))
		run(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.pg_relation_size(regclass,text) TO PUBLIC",0))
	})}
}

// GRANT followed by REVOKE leaves an explicit owner ACL in PostgreSQL.
// Restore the exact shared-object state after removing the scenario grants,
// so later catalog assertions still observe the original NULL/default ACL.
func relationSizeRestoreTablespaceACL(t *testing.T, p *fixture.Pair, b fixture.Backend, roles []string) {
	t.Helper()
	if !b.PG {
		return
	}
	var acl sql.NullString
	if err := b.DB.QueryRowContext(p.Ctx, "SELECT spcacl::text FROM pg_tablespace WHERE spcname='pg_global'").Scan(&acl); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		conn, err := b.DB.Conn(ctx)
		if err != nil {
			t.Error(err)
			return
		}
		defer conn.Close()
		if _, err := conn.ExecContext(ctx, "REVOKE ALL ON TABLESPACE pg_global FROM "+strings.Join(roles, ",")+" CASCADE"); err != nil {
			t.Error(err)
			return
		}
		if _, err := conn.ExecContext(ctx, "UPDATE pg_tablespace SET spcacl=$1::aclitem[] WHERE spcname='pg_global'", acl); err != nil {
			t.Error(err)
		}
	})
}
