package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pgSeclabelsSetup(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	pgSeclabelExec(t,p,b,c,"CREATE SCHEMA sls_private")
	t.Cleanup(func(){c.ExecContext(p.Ctx,"ROLLBACK");c.ExecContext(p.Ctx,"RESET ROLE");c.ExecContext(p.Ctx,"DROP SCHEMA IF EXISTS sls_private CASCADE")})
	pgSeclabelExec(t,p,b,c,"CREATE TABLE sls_private.sentinel(a INT,note TEXT)", "COMMENT ON TABLE sls_private.sentinel IS 'table kept'", "COMMENT ON COLUMN sls_private.sentinel.note IS 'column kept'")
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Exec("INSERT INTO sls_private.sentinel VALUES(7,'kept')",1))
}

func pgSeclabelsSentinel(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
	t.Helper()
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT a,note FROM sls_private.sentinel ORDER BY a",[]string{"a","note"},[]string{"INT4","TEXT"},[][]any{{int64(7),"kept"}}))
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT obj_description('sls_private.sentinel'::regclass,'pg_class') AS table_comment,col_description('sls_private.sentinel'::regclass,2) AS column_comment",[]string{"table_comment","column_comment"},[]string{"TEXT","TEXT"},[][]any{{"table kept","column kept"}}))
	pgSeclabelsEmpty(t,p,b,c)
}

func pgSeclabelsQueryError(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor,q string,want fixture.Diagnostic) {
	t.Helper()
	s:=fixture.ErrorDiagnostic(q,want);s.SelectRows,s.ErrorStage=true,"execute";fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
}

func TestReferencePGSeclabelsProviderBoundary(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"sls_provider_reader"})
	pgSeclabelsScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn){
		pgSeclabelsSetup(t,p,b,c)
		pgSeclabelExec(t,p,b,c,"CREATE ROLE sls_provider_reader")
		t.Cleanup(func(){c.ExecContext(p.Ctx,"RESET ROLE");c.ExecContext(p.Ctx,"DROP ROLE sls_provider_reader")})
		for _, reader:=range []bool{false,true} {
			if reader {
				pgSeclabelExec(t,p,b,c,"SET ROLE sls_provider_reader")
				fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT has_schema_privilege(n.oid,'USAGE') AS schema_access,has_table_privilege(c.oid,'SELECT') AS table_access FROM pg_catalog.pg_class c JOIN pg_catalog.pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='sls_private' AND c.relname='sentinel'",[]string{"schema_access","table_access"},[]string{"BOOL","BOOL"},[][]any{{false,false}}))
				denied:=pgSeclabelDiagnostic("42501","permission denied for schema sls_private");denied.Position="15"
				pgSeclabelsQueryError(t,p,b,c,"SELECT * FROM sls_private.sentinel",denied)
			}
			for _, target:=range []string{"TABLE sls_private.sentinel","COLUMN sls_private.sentinel.note","TABLE absent.missing","COLUMN absent.missing.note","ROLE sls_provider_reader","DATABASE absent"} {
				for _, provider:=range []struct{sql string;diagnostic fixture.Diagnostic}{
					{"",pgSeclabelAbsent()}, {" FOR missing",pgSeclabelProvider("missing")}, {" FOR ''",pgSeclabelProvider("")}, {` FOR "MiSsInG"`,pgSeclabelProvider("MiSsInG")}, {" FOR 'MiSsInG'",pgSeclabelProvider("MiSsInG")},
				} {
					for _, label:=range []string{"'first'","'second'","NULL"} {
						q:="SECURITY LABEL"+provider.sql+" ON "+target+" IS "+label
						held:=pgSeclabelHeld(t,p,c,q)
						for _, x:=range []fixture.Executor{c,held} {
							pgSeclabelError(t,p,b,x,q,provider.diagnostic)
							if reader {pgSeclabelExec(t,p,b,c,"RESET ROLE")}
							pgSeclabelsSentinel(t,p,b,c)
							if reader {pgSeclabelExec(t,p,b,c,"SET ROLE sls_provider_reader")}
						}
					}
				}
			}
			if reader {pgSeclabelExec(t,p,b,c,"RESET ROLE")}
		}
	})
}

func pgSeclabelsAuthorization(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
	t.Helper()
	pgSeclabelExec(t,p,b,c,"CREATE ROLE sls_acl_reader","SET ROLE sls_acl_reader")
	t.Cleanup(func(){
		c.ExecContext(p.Ctx,"ROLLBACK");c.ExecContext(p.Ctx,"RESET ROLE")
		c.ExecContext(p.Ctx,"REVOKE SELECT(provider,label) ON pg_catalog.pg_seclabels FROM sls_acl_reader")
		c.ExecContext(p.Ctx,"REVOKE SELECT ON pg_catalog.pg_seclabels FROM sls_acl_reader")
		for _,name:=range []string{"pg_seclabels","pg_seclabel","pg_shseclabel"}{c.ExecContext(p.Ctx,"GRANT SELECT ON pg_catalog."+name+" TO PUBLIC")}
		c.ExecContext(p.Ctx,"DROP ROLE sls_acl_reader")
	})
	full:=pgSeclabelHeld(t,p,c,pgSeclabelsSelect)
	provider:=pgSeclabelHeld(t,p,c,"SELECT provider FROM pg_catalog.pg_seclabels")
	fixture.RunStep(t,p.Ctx,c,b.Recorder,pgSeclabelsMetadata(b,pgSeclabelsSelect))
	fixture.RunStep(t,p.Ctx,full,b.Recorder,pgSeclabelsMetadata(b,full.SQL))
	pgSeclabelExec(t,p,b,c,"RESET ROLE","REVOKE SELECT ON pg_catalog.pg_seclabels FROM PUBLIC","SET ROLE sls_acl_reader")
	denied:=pgSeclabelDiagnostic("42501","permission denied for view pg_seclabels")
	checkDenied:=func(x fixture.Executor,q string){pgSeclabelsQueryError(t,p,b,x,q,denied)}
	for _,q:=range []string{pgSeclabelsSelect,"SELECT provider FROM pg_catalog.pg_seclabels","SELECT count(*) FROM pg_catalog.pg_seclabels","SELECT 1 FROM pg_catalog.pg_seclabels"}{checkDenied(c,q)}
	checkDenied(full,full.SQL);checkDenied(provider,provider.SQL)
	late:=pgSeclabelHeld(t,p,c,pgSeclabelsSelect);checkDenied(late,late.SQL)
	pgSeclabelExec(t,p,b,c,"RESET ROLE","GRANT SELECT(provider) ON pg_catalog.pg_seclabels TO sls_acl_reader","SET ROLE sls_acl_reader")
	for _,x:=range []fixture.Executor{c,provider}{fixture.RunStep(t,p.Ctx,x,b.Recorder,fixture.Select(provider.SQL,[]string{"provider"},[]string{"TEXT"},nil))}
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT has_column_privilege('pg_catalog.pg_seclabels','provider','SELECT') AS provider_access,has_column_privilege('pg_catalog.pg_seclabels','label','SELECT') AS label_access",[]string{"provider_access","label_access"},[]string{"BOOL","BOOL"},[][]any{{true,false}}))
	for _,q:=range []string{pgSeclabelsSelect,"SELECT * FROM pg_catalog.pg_seclabels","SELECT label FROM pg_catalog.pg_seclabels","SELECT objoid FROM pg_catalog.pg_seclabels"}{checkDenied(c,q)}
	checkDenied(full,full.SQL);checkDenied(late,late.SQL)
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT count(*) FROM pg_catalog.pg_seclabels",[]string{"count"},[]string{"INT8"},[][]any{{int64(0)}}))
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT 1 FROM pg_catalog.pg_seclabels",[]string{"?column?"},[]string{"INT4"},nil))
	pgSeclabelExec(t,p,b,c,"RESET ROLE","GRANT SELECT(label) ON pg_catalog.pg_seclabels TO sls_acl_reader","SET ROLE sls_acl_reader")
	fixture.RunStep(t,p.Ctx,c,b.Recorder,fixture.Select("SELECT provider,label FROM pg_catalog.pg_seclabels",[]string{"provider","label"},[]string{"TEXT","TEXT"},nil));checkDenied(full,full.SQL)
	pgSeclabelExec(t,p,b,c,"RESET ROLE","GRANT SELECT ON pg_catalog.pg_seclabels TO sls_acl_reader","SET ROLE sls_acl_reader")
	for _,x:=range []fixture.Executor{full,late}{fixture.RunStep(t,p.Ctx,x,b.Recorder,pgSeclabelsMetadata(b,pgSeclabelsSelect))}
	pgSeclabelExec(t,p,b,c,"RESET ROLE","REVOKE SELECT ON pg_catalog.pg_seclabels FROM sls_acl_reader","SET ROLE sls_acl_reader")
	checkDenied(full,full.SQL);checkDenied(late,late.SQL)
	pgSeclabelExec(t,p,b,c,"RESET ROLE","GRANT SELECT ON pg_catalog.pg_seclabels TO PUBLIC","REVOKE SELECT ON pg_catalog.pg_seclabel FROM PUBLIC","REVOKE SELECT ON pg_catalog.pg_shseclabel FROM PUBLIC","SET ROLE sls_acl_reader")
	for _,name:=range []string{"pg_seclabel","pg_shseclabel"}{pgSeclabelsQueryError(t,p,b,c,"SELECT * FROM pg_catalog."+name,pgSeclabelDiagnostic("42501","permission denied for table "+name))}
	for _,x:=range []fixture.Executor{c,full,late}{fixture.RunStep(t,p.Ctx,x,b.Recorder,pgSeclabelsMetadata(b,pgSeclabelsSelect))}
	pgSeclabelExec(t,p,b,c,"RESET ROLE","GRANT SELECT ON pg_catalog.pg_seclabel TO PUBLIC","GRANT SELECT ON pg_catalog.pg_shseclabel TO PUBLIC")
}

func TestReferencePGSeclabelsAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t,[]string{"sls_acl_reader"})
	pgSeclabelsScenario(t,pgSeclabelsAuthorization)
}
