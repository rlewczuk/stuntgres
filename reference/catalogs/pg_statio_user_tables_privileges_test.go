package reference_test

import (
 "context"
 "testing"
 "time"
 "stuntgres/reference/fixture"
)

func TestReferencePGStatioUserTablesPrivileges(t *testing.T) {
	// Cleanup is LIFO: drop the schema and its actor USAGE ACL before the role.
	fixture.RoleReferenceCleanup(t,[]string{"statio_user_actor"})
	fixture.SchemaReferenceCleanup(t,"statio_user_private")
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		admin,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		// Later-registered actor pool cleanup runs first; this admin borrower
		// remains live until the PUBLIC monitoring ACL has been restored.
		t.Cleanup(func() {
			ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel()
			if _,err:=admin.ExecContext(ctx,"GRANT SELECT ON pg_catalog.pg_statio_user_tables TO PUBLIC");err!=nil {t.Error("restore pg_statio_user_tables PUBLIC ACL",err)}
		})
		for _,q:=range []string{"CREATE ROLE statio_user_actor LOGIN PASSWORD 'role-core-fixture'","CREATE SCHEMA statio_user_private","CREATE TABLE statio_user_private.hidden(k integer)","REVOKE ALL ON SCHEMA statio_user_private FROM PUBLIC","REVOKE ALL ON statio_user_private.hidden FROM PUBLIC","ALTER TABLE statio_user_private.hidden ENABLE ROW LEVEL SECURITY"} {statioSequenceExec(t,p,b,admin,q)}
		statioSequenceExec(t,p,b,admin,"ALTER TABLE statio_user_private.hidden SET (autovacuum_enabled=false)")
		oid:=statioSequenceOID(t,p,admin,"statio_user_private.hidden")
		statioUserTableSeed(t,p,b,oid,"INSERT INTO statio_user_private.hidden VALUES(1)",1)
		statioSequenceExec(t,p,b,observer,"SET stats_fetch_consistency=none")
		before:=statioUserTableOne(t,p,b,observer,oid)
		statioTableWork(t,p,b,nil,"SELECT k FROM statio_user_private.hidden",[][]any{{int64(1)}})
		published:=statioUserTablePublished(t,p,b,before)
		statioTableHeap(t,b,published,1)
		if statioUserTableHeapTotal(t,published)<=0 {t.Fatal("admin must observe positive published heap access",published)}
		var database string
		if err:=admin.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database);err!=nil {t.Fatal(err)}
		actorPool,_,actorRecorder:=fixture.DatabaseScenarioPool(t,p,b.PG,database,"statio_user_actor")
		actor,err:=actorPool.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer actor.Close()
		statioSequenceExec(t,p,b,actor,"SET stats_fetch_consistency=none")
		stmt,err:=actor.PrepareContext(p.Ctx,statioUserTableProbe);if err!=nil {t.Fatal(err)};defer stmt.Close()
		held:=fixture.HeldStatement{Executor:actor,Statement:stmt,SQL:statioUserTableProbe}
		for _,e:=range []fixture.Executor{actor,held} {
			r:=statioUserTableOne(t,p,b,e,oid)
			if r.schema!="statio_user_private" || r.name!="hidden" {t.Fatal(r)}
			statioTableHeap(t,b,r,1);statioTableAbsentToast(t,r)
			if r!=published {t.Fatal("inaccessible table's entire monitoring row must equal nonzero admin observation",published,r)}
			if current:=statioUserTableOne(t,p,b,observer,oid);current!=published {t.Fatal("admin statistics cut changed during actor comparison",published,current)}
		}
		fixture.RunStep(t,p.Ctx,actor,actorRecorder,fixture.Select("SELECT has_schema_privilege(current_user,'statio_user_private','USAGE') AS schema_access,has_table_privilege(current_user,$1::oid,'SELECT') AS table_access,has_table_privilege(current_user,'pg_statio_user_tables','SELECT') AS monitor_access",[]string{"schema_access","table_access","monitor_access"},[]string{"BOOL","BOOL","BOOL"},[][]any{{false,false,true}},oid))
		for _,prepared:=range []bool{false,true} {
			s:=fixture.ErrorDiagnostic("SELECT k FROM statio_user_private.hidden",fixture.ControlsReferenceDiagnostic("42501","permission denied for schema statio_user_private"));s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";if prepared {s.ErrorStage="prepare"};s.Diagnostic.Position="15";fixture.RunStep(t,p.Ctx,actor,actorRecorder,s)
		}
		statioSequenceExec(t,p,b,admin,"GRANT USAGE ON SCHEMA statio_user_private TO statio_user_actor")
		d:=fixture.ControlsReferenceDiagnostic("42501","permission denied for table hidden")
		denied:=fixture.ImplicitError("SELECT k FROM statio_user_private.hidden",d);denied.SelectRows=true
		fixture.RunStep(t,p.Ctx,actor,actorRecorder,denied)
		statioSequenceExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_statio_user_tables FROM PUBLIC")
		d=fixture.ControlsReferenceDiagnostic("42501","permission denied for view pg_statio_user_tables")
		for _,e:=range []fixture.Executor{actor,held} {rows,err:=e.QueryContext(p.Ctx,statioUserTableProbe,oid);if rows!=nil {rows.Close()};fixture.AssertDiagnostic(t,err,&d)}
		fresh,err:=actor.PrepareContext(p.Ctx,statioUserTableProbe);if err!=nil {t.Fatal("fresh Prepare must not evaluate ACL",err)}
		rows,err:=fresh.QueryContext(p.Ctx,oid);if rows!=nil {rows.Close()};fixture.AssertDiagnostic(t,err,&d);fresh.Close()
		statioSequenceExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_statio_user_tables TO PUBLIC")
		for _,e:=range []fixture.Executor{actor,held,observer} {
			if current:=statioUserTableOne(t,p,b,e,oid);current!=published {t.Fatal("regrant restores the same unredacted published row",published,current)}
		}
	})}
}
