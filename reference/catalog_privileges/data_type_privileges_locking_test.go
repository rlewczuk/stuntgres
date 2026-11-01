package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func dtpLockDenied(query string) fixture.Step {
	return fixture.ImplicitError(query,fixture.Diagnostic{Code:"42501",Message:"permission denied for view data_type_privileges"})
}

func TestReferenceDataTypePrivilegesLocking(t *testing.T) {
	dtpScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn,run func(fixture.Step)){
		defer func(){
			_,_=c.ExecContext(p.Ctx,"RESET SESSION AUTHORIZATION")
			if _,err:=c.ExecContext(p.Ctx,"REVOKE UPDATE ON information_schema.data_type_privileges FROM dtp_reader");err!=nil{t.Error(err)}
		}()
		empty:=fixture.Select("SELECT * FROM information_schema.data_type_privileges WHERE FALSE FOR UPDATE",dtpColumns,dtpTypes,nil)
		empty.CheckMetadata=func(t *testing.T,rows *sql.Rows){types,err:=rows.ColumnTypes();if err!=nil{t.Fatal(err)};fixture.TableDDLColumnTypes(t,b.PG,types,dtpColumns,dtpTypes,[]bool{true,true,true,true,true})}
		run(empty)
		for _,q:=range []string{"CREATE TABLE dtp_lock_table(a integer,b text)","INSERT INTO dtp_lock_table VALUES(7,'sentinel')","CREATE DOMAIN dtp_lock_domain AS integer","CREATE PROCEDURE dtp_lock_proc() LANGUAGE SQL AS 'SELECT 1'"}{
			affected:=int64(0);if q=="INSERT INTO dtp_lock_table VALUES(7,'sentinel')"{affected=1};run(fixture.Exec(q,affected))
		}
		id:=dtpRoutineIdentity(t,p,c,"public","dtp_lock_proc","")
		where:="object_schema='public' AND object_name IN ('dtp_lock_table','dtp_lock_domain','"+id+"')"
		want:=append(dtpRows("public","dtp_lock_table","TABLE","1","2"),dtpRows("public","dtp_lock_domain","DOMAIN","1")...)
		want=append(want,dtpRoutineRows("public",id,nil)...)
		locked:=func(alias bool)fixture.Step{
			s:=dtpProjection(t,p,c,where,want)
			if alias{s.Query="SELECT d.* FROM information_schema.data_type_privileges d WHERE "+where+" FOR UPDATE OF d"}else{s.Query+=" FOR UPDATE"}
			return s
		}
		run(locked(false));run(locked(true))
		run(fixture.Exec("CREATE VIEW dtp_lock_wrapper AS SELECT * FROM information_schema.data_type_privileges WHERE "+where,0))
		wrapper:=dtpProjection(t,p,c,where,want);wrapper.Query="SELECT * FROM dtp_lock_wrapper FOR UPDATE";run(wrapper)
		// SQL UPDATE admission is on the metadata view, never represented tables.
		dtpActor(run,"dtp_reader",func(){
			hidden:="object_schema='public' AND object_name='dtp_lock_table'"
			run(dtpProjection(t,p,c,hidden,nil))
			run(dtpLockDenied("SELECT * FROM information_schema.data_type_privileges WHERE "+hidden+" FOR UPDATE"))
		})
		run(fixture.Exec("GRANT SELECT ON dtp_lock_table TO dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){
			run(dtpProjection(t,p,c,where,want))
			run(dtpLockDenied(locked(false).Query));run(dtpLockDenied(empty.Query))
		})
		run(fixture.Exec("GRANT UPDATE ON information_schema.data_type_privileges TO dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){run(locked(false));run(locked(true));run(empty)})
		// Admit schema lookup before testing the wrapper's own UPDATE privilege.
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO dtp_reader",0))
		run(fixture.Exec("GRANT SELECT ON dtp_lock_wrapper TO dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){run(fixture.ImplicitError(wrapper.Query,fixture.Diagnostic{Code:"42501",Message:"permission denied for view dtp_lock_wrapper"}))})
		run(fixture.Exec("GRANT UPDATE ON dtp_lock_wrapper TO dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){run(wrapper)})
		run(fixture.Exec("REVOKE UPDATE ON information_schema.data_type_privileges FROM dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){run(dtpLockDenied(locked(false).Query));run(dtpLockDenied(empty.Query));run(wrapper)})
		// Physical tables still require their own UPDATE and retain real tuple marks.
		dtpActor(run,"dtp_reader",func(){run(fixture.ImplicitError("SELECT a FROM dtp_lock_table FOR UPDATE",fixture.Diagnostic{Code:"42501",Message:"permission denied for table dtp_lock_table"}))})
		run(fixture.Exec("GRANT UPDATE ON dtp_lock_table TO dtp_reader",0))
		dtpActor(run,"dtp_reader",func(){run(fixture.Select("SELECT a FROM dtp_lock_table FOR UPDATE",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))})
		if !b.PG{run(fixture.ImplicitError("SELECT * FROM pg_catalog.pg_type WHERE FALSE FOR UPDATE",fixture.Diagnostic{Code:"0A000",Message:"row locking is not supported for virtual catalog relations"}))}
		run(fixture.Select("SELECT a FROM dtp_lock_table",[]string{"a"},[]string{"INT4"},[][]any{{int64(7)}}))
	})
}
