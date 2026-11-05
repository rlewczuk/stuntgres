package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFileFDWInstallation(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname IN('file_fdw_handler','file_fdw_validator')", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='file_fdw'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE EXTENSION file_fdw", 0))
		fileFDWGraph(run, "file_fdw", "public.file_fdw_handler()", "public.file_fdw_validator(text[],oid)")
		run(fixture.Select("SELECT e.extowner=r.oid AS installing_owner FROM pg_extension e JOIN pg_roles r ON r.rolname=current_user WHERE e.extname='file_fdw'", []string{"installing_owner"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Select("SELECT p.proname,p.prorettype=3115::oid AS handler_result,p.prorettype=2278::oid AS void_result,p.pronargs,p.proisstrict,p.provolatile,p.proparallel,p.prosecdef,p.proretset,l.lanname,p.probin,p.prosrc,p.proowner=e.extowner AS installed_owner FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang JOIN pg_extension e ON e.extname='file_fdw' WHERE p.proname IN('file_fdw_handler','file_fdw_validator') ORDER BY p.proname", []string{"proname", "handler_result", "void_result", "pronargs", "proisstrict", "provolatile", "proparallel", "prosecdef", "proretset", "lanname", "probin", "prosrc", "installed_owner"}, []string{"NAME", "BOOL", "BOOL", "INT2", "BOOL", "CHAR", "CHAR", "BOOL", "BOOL", "NAME", "TEXT", "TEXT", "BOOL"}, [][]any{{"file_fdw_handler", true, false, int64(0), true, "v", "u", false, false, "c", "$libdir/file_fdw", "file_fdw_handler", true}, {"file_fdw_validator", false, true, int64(2), true, "v", "u", false, false, "c", "$libdir/file_fdw", "file_fdw_validator", true}}))
		run(fixture.Select("SELECT proargtypes::text AS arguments FROM pg_proc WHERE proname='file_fdw_validator'", []string{"arguments"}, []string{"TEXT"}, [][]any{{"1009 26"}}))
		run(fileFDWError("CREATE EXTENSION file_fdw", "42710", `extension "file_fdw" already exists`))
		var notices []fixture.Diagnostic
		_, noticeConn, noticeRecord := fixture.NoticeReferencePool(t, p, b.PG, &notices)
		noticeStep := fixture.Exec("CREATE EXTENSION IF NOT EXISTS file_fdw", 0)
		noticeStep.Prepared = prepared
		fixture.RunStep(t, p.Ctx, noticeConn, noticeRecord, noticeStep)
		wantNotices := []fixture.Diagnostic{{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42710", Message: `extension "file_fdw" already exists, skipping`}}
		if !reflect.DeepEqual(notices, wantNotices) {
			t.Fatalf("extension notice got %+v want %+v", notices, wantNotices)
		}
		run(fixture.Exec("DROP EXTENSION file_fdw", 0))
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("CREATE EXTENSION file_fdw", 0))
		run(fixture.Exec("ROLLBACK", 0))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_data_wrapper WHERE fdwname='file_fdw'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("CREATE FUNCTION file_fdw_validator(text[],oid) RETURNS void LANGUAGE SQL AS 'SELECT NULL::void'", 0))
		collision := fdwDiagnostic("42723", "function \"file_fdw_validator\" already exists with same argument types")
		collision.Context = "SQL statement \"CREATE FUNCTION file_fdw_validator(text[], oid)\nRETURNS void\nAS '$libdir/file_fdw'\nLANGUAGE C STRICT\"\nextension script file \"file_fdw--1.0.sql\", near line 11"
		run(fixture.ErrorDiagnostic("CREATE EXTENSION file_fdw", collision))
		run(fixture.Select("SELECT count(*) FROM pg_proc WHERE proname='file_fdw_handler'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Select("SELECT count(*) FROM pg_extension WHERE extname='file_fdw'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		run(fixture.Exec("DROP FUNCTION file_fdw_validator(text[],oid)", 0))
		run(fixture.Exec("CREATE ROLE ff_actor", 0))
		run(fixture.Exec("GRANT USAGE ON SCHEMA public TO ff_actor", 0))
		run(fixture.Exec("SET ROLE ff_actor", 0))
		d := fdwDiagnostic("42501", `permission denied to create extension "file_fdw"`)
		d.Hint = "Must be superuser to create this extension."
		run(fixture.ErrorDiagnostic("CREATE EXTENSION file_fdw", d))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE SCHEMA ff_helpers", 0))
		run(fixture.Exec("CREATE EXTENSION file_fdw SCHEMA ff_helpers", 0))
		fileFDWGraph(run, "file_fdw", "ff_helpers.file_fdw_handler()", "ff_helpers.file_fdw_validator(text[],oid)")
	})
}

func TestReferenceFileFDWConfiguration(t *testing.T) {
	fileFDWScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, prepared bool, run func(fixture.Step)) {
		fileFDWInstall(run)
		run(fixture.Exec(`CREATE FOREIGN TABLE ff_table(id integer,txt text OPTIONS(force_not_null 'true',force_null 'on')) SERVER ff_server OPTIONS(filename '/not-a-real-filefdw-source',format 'csv',header 'match',delimiter ';',quote '"',escape '!',null 'NIL',encoding 'UTF8')`, 0))
		run(fileFDWOptions("ff_table", "filename=/not-a-real-filefdw-source", "format=csv", "header=match", "delimiter=;", `quote="`, "escape=!", "null=NIL", "encoding=UTF8"))
		run(fixture.Select("SELECT c.relname,c.relkind,c.relowner=r.oid AS current_owner FROM pg_class c JOIN pg_roles r ON r.rolname=current_user WHERE c.oid='ff_table'::regclass", []string{"relname", "relkind", "current_owner"}, []string{"NAME", "CHAR", "BOOL"}, [][]any{{"ff_table", "f", true}}))
		run(fixture.Select("SELECT attname,attnum,attfdwoptions FROM pg_attribute WHERE attrelid='ff_table'::regclass AND attnum>0 AND NOT attisdropped ORDER BY attnum", []string{"attname", "attnum", "attfdwoptions"}, []string{"NAME", "INT2", "_TEXT"}, [][]any{{"id", int64(1), nil}, {"txt", int64(2), "{force_not_null=true,force_null=on}"}}))
		for _, tc := range []struct{ table, options string }{
			{"ff_program", "program 'not-a-real-filefdw-program'"},
			{"ff_binary", "filename '/not-a-real-filefdw-binary',format 'binary'"},
			{"ff_empty", "filename ''"},
			{"ff_text", "filename '/not-a-real-filefdw-text',format 'text'"},
		} {
			run(fixture.Exec("CREATE FOREIGN TABLE "+tc.table+"(id integer OPTIONS(force_null 'true',force_not_null 'false')) SERVER ff_server OPTIONS("+tc.options+")", 0))
		}
		run(fileFDWOptions("ff_program", "program=not-a-real-filefdw-program"))
		run(fileFDWOptions("ff_binary", "filename=/not-a-real-filefdw-binary", "format=binary"))
		run(fileFDWOptions("ff_empty", "filename="))
		run(fixture.Exec("ALTER FOREIGN TABLE ff_empty OPTIONS(DROP filename,ADD program '')", 0))
		run(fileFDWOptions("ff_empty", "program="))
		run(fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER ff_server", 0))
		run(fixture.Select("SELECT srvoptions IS NULL AS no_options FROM pg_foreign_server WHERE srvname='ff_server'", []string{"no_options"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Select("SELECT umoptions IS NULL AS no_options FROM pg_user_mapping m JOIN pg_foreign_server s ON s.oid=m.umserver WHERE s.srvname='ff_server'", []string{"no_options"}, []string{"BOOL"}, [][]any{{true}}))
	})
}
