package reference_test

import (
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioAllTablesMembership(t *testing.T) {
	fixture.SchemaReferenceCleanup(t,"statio_table_members")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		a,peer:=fixture.LockConnections(t,p.Ctx,b.DB)
		for _,q:=range []string{
			"CREATE SCHEMA statio_table_members",
			"CREATE TABLE statio_table_members.ordinary(k integer)",
			"CREATE UNLOGGED TABLE statio_table_members.unlogged(k integer)",
			"CREATE TABLE statio_table_members.parent(k integer) PARTITION BY RANGE(k)",
			"CREATE TABLE statio_table_members.leaf PARTITION OF statio_table_members.parent FOR VALUES FROM(0) TO(10)",
			"CREATE MATERIALIZED VIEW statio_table_members.materialized AS SELECT k FROM statio_table_members.ordinary",
			"CREATE VIEW statio_table_members.not_table AS SELECT k FROM statio_table_members.ordinary",
			"CREATE SEQUENCE statio_table_members.not_heap",
			"CREATE INDEX statio_table_member_index ON statio_table_members.ordinary(k)",
			"CREATE TEMP TABLE statio_table_member_temp(k integer)",
			"CREATE FOREIGN DATA WRAPPER statio_table_fdw",
			"CREATE SERVER statio_table_server FOREIGN DATA WRAPPER statio_table_fdw",
			"CREATE FOREIGN TABLE statio_table_members.foreign_t(k integer) SERVER statio_table_server",
		} {statioSequenceExec(t,p,b,a,q)}
		defer a.ExecContext(context.Background(),"DROP FOREIGN DATA WRAPPER statio_table_fdw CASCADE")
		want:=map[int64]string{}
		for _,name:=range []string{"ordinary","unlogged","leaf","materialized"} {want[statioSequenceOID(t,p,a,"statio_table_members."+name)]=name}
		want[statioSequenceOID(t,p,a,"pg_temp.statio_table_member_temp")]="statio_table_member_temp"
		q:="SELECT * FROM pg_statio_all_tables WHERE schemaname='statio_table_members' OR relname='statio_table_member_temp'"
		for _,prepared:=range []bool{false,true} {
			var e fixture.Executor=peer
			if prepared {stmt,err:=peer.PrepareContext(p.Ctx,q);if err!=nil {t.Fatal(err)};defer stmt.Close();e=fixture.HeldStatement{Executor:peer,Statement:stmt,SQL:q}}
			got:=map[int64]string{}
			for _,r:=range statioTableRows(t,p,b,e,q) {statioTableAbsentToast(t,r);if _,ok:=got[r.oid];ok {t.Fatal("duplicate",r)};got[r.oid]=r.name}
			if !reflect.DeepEqual(got,want) {t.Fatal("complete unordered actual membership",got,want)}
		}
		// Independently count represented catalog storage, not a fabricated
		// bootstrap descriptor population or PostgreSQL's full catalog count.
		fixture.RunStep(t,p.Ctx,peer,b.Recorder,fixture.Select("SELECT count(*)>0 AS represented FROM pg_statio_all_tables s JOIN pg_class c ON c.oid=s.relid WHERE s.schemaname='pg_catalog' AND c.relkind='r'",[]string{"represented"},[]string{"BOOL"},[][]any{{true}}))
		predicate:="schemaname IN ('pg_catalog','information_schema') OR schemaname ~ '^pg_toast'"
		fixture.RunStep(t,p.Ctx,peer,b.Recorder,fixture.Select("SELECT count(*) FROM pg_statio_all_tables WHERE ("+predicate+") AND schemaname='statio_table_members'",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
		fixture.RunStep(t,p.Ctx,peer,b.Recorder,fixture.Select("SELECT count(*) FROM pg_statio_all_tables WHERE NOT ("+predicate+") AND schemaname='statio_table_members'",[]string{"count"},[]string{"INT8"},[][]any{{int64(4)}}))
		statioSequenceExec(t,p,b,a,"BEGIN");defer a.ExecContext(context.Background(),"ROLLBACK")
		statioSequenceExec(t,p,b,a,"CREATE TABLE statio_table_members.private_birth(k integer)")
		private:=statioSequenceOID(t,p,a,"statio_table_members.private_birth")
		statioTableOne(t,p,b,a,private)
		if rows:=statioTableRows(t,p,b,peer,statioTableProbe,private);len(rows)!=0 {t.Fatal("uncommitted identity leaked",rows)}
		statioSequenceExec(t,p,b,a,"ROLLBACK")
		if rows:=statioTableRows(t,p,b,a,statioTableProbe,private);len(rows)!=0 {t.Fatal("aborted identity retained",rows)}
		{
			statioSequenceExec(t,p,b,peer,"BEGIN")
			defer peer.ExecContext(context.Background(),"ROLLBACK")
			statioSequenceExec(t,p,b,peer,"SET LOCAL stats_fetch_consistency=snapshot")
			for oid:=range want {
				all:=statioTableOne(t,p,b,peer,oid)
				user:=statioTableRows(t,p,b,peer,"SELECT * FROM pg_statio_user_tables WHERE relid=$1::oid",oid)
				if len(user)!=1 || user[0]!=all {t.Fatal("named companion same snapshot",all,user)}
				if sys:=statioTableRows(t,p,b,peer,"SELECT * FROM pg_statio_sys_tables WHERE relid=$1::oid",oid);len(sys)!=0 {t.Fatal("user in system companion",sys)}
			}
			statioSequenceExec(t,p,b,peer,"ROLLBACK")
		}
	})}
}

func TestReferencePGStatioAllTablesPrivileges(t *testing.T) {
	// Cleanup is LIFO: drop the schema and its actor USAGE ACL before the role.
	fixture.RoleReferenceCleanup(t,[]string{"statio_table_actor"})
	fixture.SchemaReferenceCleanup(t,"statio_table_private")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		admin,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		// Later-registered actor pool cleanup runs first; this admin borrower
		// remains live until the PUBLIC monitoring ACL has been restored.
		t.Cleanup(func() {
			ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
			if _,err:=admin.ExecContext(ctx,"GRANT SELECT ON pg_catalog.pg_statio_all_tables TO PUBLIC");err!=nil {t.Error("restore pg_statio_all_tables PUBLIC ACL",err)}
		})
		for _,q:=range []string{"CREATE ROLE statio_table_actor LOGIN PASSWORD 'role-core-fixture'","CREATE SCHEMA statio_table_private","CREATE TABLE statio_table_private.hidden(k integer)","REVOKE ALL ON SCHEMA statio_table_private FROM PUBLIC","REVOKE ALL ON statio_table_private.hidden FROM PUBLIC","ALTER TABLE statio_table_private.hidden ENABLE ROW LEVEL SECURITY"} {statioSequenceExec(t,p,b,admin,q)}
		fixture.RunStep(t,p.Ctx,admin,b.Recorder,fixture.Exec("INSERT INTO statio_table_private.hidden VALUES(1)",1))
		oid:=statioSequenceOID(t,p,admin,"statio_table_private.hidden")
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_table_private.hidden",[][]any{{int64(1)}})
		var database string
		if err:=admin.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database);err!=nil {t.Fatal(err)}
		actorPool,_,actorRecorder:=fixture.DatabaseScenarioPool(t,p,b.PG,database,"statio_table_actor")
		actor,err:=actorPool.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer actor.Close()
		stmt,err:=actor.PrepareContext(p.Ctx,statioTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:actor,Statement:stmt,SQL:statioTableProbe}
		for _,e:=range []fixture.Executor{actor,held} {
			r:=statioTableOne(t,p,b,e,oid)
			if r.schema!="statio_table_private" || r.name!="hidden" {t.Fatal(r)}
			statioTableHeap(t,b,r,1);statioTableAbsentToast(t,r)
		}
		fixture.RunStep(t,p.Ctx,actor,actorRecorder,fixture.Select("SELECT has_schema_privilege(current_user,'statio_table_private','USAGE') AS schema_access,has_table_privilege(current_user,$1::oid,'SELECT') AS table_access,has_table_privilege(current_user,'pg_statio_all_tables','SELECT') AS monitor_access",[]string{"schema_access","table_access","monitor_access"},[]string{"BOOL","BOOL","BOOL"},[][]any{{false,false,true}},oid))
		for _,prepared:=range []bool{false,true} {
			s:=fixture.ErrorDiagnostic("SELECT k FROM statio_table_private.hidden",fixture.ControlsReferenceDiagnostic("42501","permission denied for schema statio_table_private"));s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";if prepared {s.ErrorStage="prepare"};s.Diagnostic.Position="15";fixture.RunStep(t,p.Ctx,actor,actorRecorder,s)
		}
		statioSequenceExec(t,p,b,admin,"GRANT USAGE ON SCHEMA statio_table_private TO statio_table_actor")
		d:=fixture.ControlsReferenceDiagnostic("42501","permission denied for table hidden")
		denied:=fixture.ImplicitError("SELECT k FROM statio_table_private.hidden",d);denied.SelectRows=true
		fixture.RunStep(t,p.Ctx,actor,actorRecorder,denied)
		statioSequenceExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_statio_all_tables FROM PUBLIC")
		d=fixture.ControlsReferenceDiagnostic("42501","permission denied for view pg_statio_all_tables")
		for _,e:=range []fixture.Executor{actor,held} {rows,err:=e.QueryContext(p.Ctx,statioTableProbe,oid);if rows!=nil {rows.Close()};fixture.AssertDiagnostic(t,err,&d)}
		statioSequenceExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_statio_all_tables TO PUBLIC")
		statioTableOne(t,p,b,held,oid);statioTableOne(t,p,b,observer,oid)
	})}
}
