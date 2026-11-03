package reference_test

import (
	"context"
	"database/sql"
	"testing"
	"time"

	"stuntgres/reference/fixture"
)

func TestReferencePGStatioSysTablesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"statio_sys_table_actor"})
	p:=fixture.NewScenario(t)
	for _,b:=range p.Backends() {t.Run(b.Name,func(t *testing.T) {
		admin,observer:=fixture.LockConnections(t,p.Ctx,b.DB)
		var underlyingACL sql.NullString
		if err:=admin.QueryRowContext(p.Ctx,"SELECT relacl::text FROM pg_catalog.pg_class WHERE oid=1259").Scan(&underlyingACL);err!=nil {t.Fatal(err)}
		// Only the two PUBLIC SELECT grants are changed. Preserve their
		// independently checked initial grant state, and restore before the
		// fixture needs pg_class to clean up schemas and roles, even on Fatal.
		t.Cleanup(func(){ctx,cancel:=context.WithTimeout(context.Background(),15*time.Second);defer cancel();for _,q:=range []string{"GRANT SELECT ON pg_catalog.pg_class TO PUBLIC","GRANT SELECT ON pg_catalog.pg_statio_sys_tables TO PUBLIC"} {if _,err:=admin.ExecContext(ctx,q);err!=nil {t.Error("restore catalog PUBLIC SELECT",err)}}})
		statioSequenceExec(t,p,b,admin,"CREATE ROLE statio_sys_table_actor LOGIN PASSWORD 'role-core-fixture'")
		oid:=statioSequenceOID(t,p,admin,"pg_catalog.pg_class")
		var database string;if err:=admin.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database);err!=nil {t.Fatal(err)}
		pool,_,recorder:=fixture.DatabaseScenarioPool(t,p,b.PG,database,"statio_sys_table_actor")
		actor,err:=pool.Conn(p.Ctx);if err!=nil {t.Fatal(err)};defer actor.Close()
		held,stmt:=statioSysTableHeld(t,p,actor);defer stmt.Close()
		fixture.RunStep(t,p.Ctx,actor,recorder,fixture.Select("SELECT has_table_privilege(current_user,'pg_catalog.pg_class','SELECT') AS underlying,has_table_privilege(current_user,'pg_catalog.pg_statio_sys_tables','SELECT') AS monitoring",[]string{"underlying","monitoring"},[]string{"BOOL","BOOL"},[][]any{{true,true}}))
		for _,e:=range []fixture.Executor{actor,held} {statioSysTablePositive(t,p,b,e,oid)}
		statioSequenceExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_class FROM PUBLIC")
		deniedTable:=fixture.ControlsReferenceDiagnostic("42501","permission denied for table pg_class")
		for _,prepared:=range []bool{false,true} {s:=fixture.ErrorDiagnostic("SELECT oid FROM pg_catalog.pg_class WHERE false",deniedTable);s.SelectRows=true;s.Prepared=prepared;s.ErrorStage="execute";fixture.RunStep(t,p.Ctx,actor,recorder,s)}
		for _,e:=range []fixture.Executor{actor,held} {rows:=statioTableRows(t,p,b,e,statioSysTableProbe,oid);if len(rows)!=1 || rows[0].oid!=oid || rows[0].schema!="pg_catalog" || rows[0].name!="pg_class" {t.Fatal("underlying denial filtered monitoring identity",rows)}}
		// Restore the underlying catalog before any unrelated catalog fixture
		// operations; the own-view ACL phase is independent of table access.
		statioSequenceExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_class TO PUBLIC")
		var restoredACL sql.NullString
		if err:=admin.QueryRowContext(p.Ctx,"SELECT relacl::text FROM pg_catalog.pg_class WHERE oid=1259").Scan(&restoredACL);err!=nil || restoredACL!=underlyingACL {t.Fatal("underlying catalog grants not restored",restoredACL,underlyingACL,err)}
		statioSequenceExec(t,p,b,admin,"REVOKE SELECT ON pg_catalog.pg_statio_sys_tables FROM PUBLIC")
		deniedView:=fixture.ControlsReferenceDiagnostic("42501","permission denied for view pg_statio_sys_tables")
		for _,e:=range []fixture.Executor{actor,held} {rows,err:=e.QueryContext(p.Ctx,statioSysTableProbe,oid);if rows!=nil {rows.Close()};fixture.AssertDiagnostic(t,err,&deniedView)}
		// Fresh Prepare is admitted; authority is evaluated on Execute too.
		fresh,err:=actor.PrepareContext(p.Ctx,statioSysTableProbe);if err!=nil {t.Fatal("prepare is not evaluated SELECT authority",err)}
		rows,err:=fresh.QueryContext(p.Ctx,oid);if rows!=nil {rows.Close()};fixture.AssertDiagnostic(t,err,&deniedView);fresh.Close()
		statioSequenceExec(t,p,b,admin,"GRANT SELECT ON pg_catalog.pg_statio_sys_tables TO PUBLIC")
		statioSysTablePositive(t,p,b,held,oid);statioSysTablePositive(t,p,b,observer,oid)
	})}
}
