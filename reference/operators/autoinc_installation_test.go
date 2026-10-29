package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceAutoincInstallation(t *testing.T) {
	t.Run("metadata",func(t *testing.T) {
		fixture.SchemaReferenceCleanup(t,"ai_install")
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			run:=autoincRoute(t,p,b,c,"direct","")
			s:=autoincError("SELECT autoinc()","42883","function autoinc() does not exist")
			s.SelectRows=true;s.ErrorStage="";s.Diagnostic.Hint=parametersNoMatchHint;s.Diagnostic.Position=fixture.Position(8)
			run(s)
			userOperatorExec(t,p,b,c,"CREATE SCHEMA ai_install","CREATE EXTENSION autoinc WITH SCHEMA ai_install")
			run(fixture.Select("SELECT extversion,extrelocatable FROM pg_extension WHERE extname='autoinc'",[]string{"extversion","extrelocatable"},[]string{"TEXT","BOOL"},[][]any{{"1.0",true}}))
			run(fixture.Select("SELECT version,superuser,trusted,relocatable,comment FROM pg_available_extension_versions WHERE name='autoinc' ORDER BY version",[]string{"version","superuser","trusted","relocatable","comment"},[]string{"TEXT","BOOL","BOOL","BOOL","TEXT"},[][]any{{"1.0",true,false,true,"functions for autoincrementing fields"}}))
			q:=`SELECT p.proname,p.pronargs::integer,p.prorettype::bigint,l.lanname,p.prosrc,p.probin,p.provolatile::text,p.proisstrict,p.proparallel::text,p.proretset,p.procost::double precision FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='ai_install' ORDER BY p.proname`
			run(fixture.Select(q,[]string{"proname","pronargs","prorettype","lanname","prosrc","probin","provolatile","proisstrict","proparallel","proretset","procost"},[]string{"NAME","INT4","INT8","NAME","TEXT","TEXT","TEXT","BOOL","TEXT","BOOL","FLOAT8"},[][]any{{"autoinc",int64(0),int64(2279),"c","autoinc","$libdir/autoinc","v",false,"u",false,float64(1)}}))
			run(autoincScalar("SELECT count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='autoinc' AND d.deptype='e' AND d.classid='pg_proc'::regclass","count",1))
			run(autoincError("CREATE EXTENSION autoinc","42710",`extension "autoinc" already exists`))
			var notices []fixture.Diagnostic
			db,nc,rec:=fixture.NoticeReferencePool(t,p,b.PG,&notices)
			defer db.Close();defer nc.Close()
			fixture.RunStep(t,p.Ctx,nc,rec,fixture.Exec("CREATE EXTENSION IF NOT EXISTS autoinc",0))
			want:=fixture.ControlsReferenceDiagnostic("42710",`extension "autoinc" already exists, skipping`);want.Severity="NOTICE";want.SeverityUnlocalized="NOTICE"
			if len(notices)!=1 || notices[0]!=want { t.Fatalf("notices=%#v want=%#v",notices,want) }
			userOperatorExec(t,p,b,c,"CREATE TABLE t(id integer)","CREATE SEQUENCE s START 10","CREATE TRIGGER a BEFORE INSERT ON t FOR EACH ROW EXECUTE FUNCTION ai_install.autoinc('id','s')")
			run(fixture.Select("INSERT INTO t VALUES(NULL) RETURNING id",[]string{"id"},[]string{"INT4"},[][]any{{int64(10)}}))
		})
	})
	t.Run("transaction-and-collision",func(t *testing.T) {
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			run:=autoincRoute(t,p,b,c,"direct","")
			run(fixture.Exec("BEGIN",0));run(fixture.Exec("CREATE EXTENSION autoinc",0));run(fixture.Exec("ROLLBACK",0))
			run(autoincScalar("SELECT count(*) FROM pg_extension WHERE extname='autoinc'","count",0))
			run(autoincScalar("SELECT count(*) FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='public' AND p.proname='autoinc'","count",0))
			userOperatorExec(t,p,b,c,"CREATE FUNCTION autoinc() RETURNS integer LANGUAGE SQL AS 'SELECT 7'")
			collision:=autoincError("CREATE EXTENSION autoinc","42723",`function "autoinc" already exists with same argument types`)
			collision.Diagnostic.Context="SQL statement \"CREATE FUNCTION autoinc()\nRETURNS trigger\nAS '$libdir/autoinc'\nLANGUAGE C\"\nextension script file \"autoinc--1.0.sql\", near line 6"
			run(collision)
			run(autoincScalar("SELECT count(*) FROM pg_extension WHERE extname='autoinc'","count",0))
			run(fixture.Select("SELECT autoinc()",[]string{"autoinc"},[]string{"INT4"},[][]any{{int64(7)}}))
		})
	})
	t.Run("superuser",func(t *testing.T) {
		fixture.RoleReferenceCleanup(t,[]string{"ai_installer"})
		triggerScenario(t,func(t *testing.T,p *fixture.Pair,b fixture.Backend,c *sql.Conn) {
			var database string
			if err:=c.QueryRowContext(p.Ctx,"SELECT current_database()").Scan(&database);err!=nil { t.Fatal(err) }
			target:=`"`+strings.ReplaceAll(database,`"`,`""`)+`"`
			userOperatorExec(t,p,b,c,"CREATE ROLE ai_installer","GRANT CREATE ON DATABASE "+target+" TO ai_installer","GRANT CREATE,USAGE ON SCHEMA public TO ai_installer")
			defer c.ExecContext(p.Ctx,"REVOKE CREATE ON DATABASE "+target+" FROM ai_installer")
			userOperatorExec(t,p,b,c,"SET ROLE ai_installer")
			defer c.ExecContext(p.Ctx,"RESET ROLE")
			s:=autoincError("CREATE EXTENSION autoinc","42501",`permission denied to create extension "autoinc"`)
			s.Diagnostic.Hint="Must be superuser to create this extension."
			fixture.RunStep(t,p.Ctx,c,b.Recorder,s)
		})
	})
}
