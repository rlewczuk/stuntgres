package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInsertUsernameInstallation(t *testing.T) {
	insertUsernameCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := insertUsernameRun(t, p, b, c)
		insertUsernameBoth(t, p, b, c, fixture.Select("SELECT name,default_version,installed_version,comment FROM pg_available_extensions WHERE name='insert_username'", []string{"name", "default_version", "installed_version", "comment"}, []string{"NAME", "TEXT", "TEXT", "TEXT"}, [][]any{{"insert_username", "1.0", nil, "functions for tracking who changed a table"}}))
		for _, q := range []string{"CREATE SCHEMA iu_ext", "BEGIN", "CREATE EXTENSION insert_username SCHEMA iu_ext", "ROLLBACK"} { run(fixture.Exec(q, 0)) }
		run(fixture.Select("SELECT extname FROM pg_extension WHERE extname='insert_username'", []string{"extname"}, []string{"NAME"}, nil))
		run(fixture.Exec("CREATE ROLE iu_writer", 0))
		run(fixture.Exec("SET ROLE iu_writer", 0))
		run(fixture.ImplicitError("CREATE EXTENSION insert_username SCHEMA iu_ext", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "insert_username"`, Hint: "Must be superuser to create this extension."}))
		run(fixture.Exec("RESET ROLE", 0))
		run(fixture.Exec("CREATE EXTENSION insert_username SCHEMA iu_ext VERSION '1.0'", 0))
		insertUsernameBoth(t, p, b, c, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,p.proname,p.pronargs,p.prorettype::integer,l.lanname,p.provolatile,p.proisstrict,p.prosecdef,p.proparallel,p.procost,p.proretset,p.prosrc,p.probin FROM pg_extension e JOIN pg_depend d ON d.refobjid=e.oid JOIN pg_proc p ON p.oid=d.objid JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE e.extname='insert_username' AND d.deptype='e' AND d.classid='pg_proc'::regclass", []string{"extversion", "extrelocatable", "nspname", "proname", "pronargs", "prorettype", "lanname", "provolatile", "proisstrict", "prosecdef", "proparallel", "procost", "proretset", "prosrc", "probin"}, []string{"TEXT", "BOOL", "NAME", "NAME", "INT2", "INT4", "NAME", "CHAR", "BOOL", "BOOL", "CHAR", "FLOAT4", "BOOL", "TEXT", "TEXT"}, [][]any{{"1.0", true, "iu_ext", "insert_username", int64(0), int64(2279), "c", "v", false, false, "u", float64(1), false, "insert_username", "$libdir/insert_username"}}))
		insertUsernameBoth(t, p, b, c, fixture.Select("SELECT p.oid>0 AS allocated,p.proowner=e.extowner AS same_owner,p.pronamespace=e.extnamespace AS same_schema FROM pg_proc p JOIN pg_depend d ON d.objid=p.oid JOIN pg_extension e ON e.oid=d.refobjid WHERE e.extname='insert_username' AND d.classid='pg_proc'::regclass AND d.deptype='e'", []string{"allocated", "same_owner", "same_schema"}, []string{"BOOL", "BOOL", "BOOL"}, [][]any{{true, true, true}}))
	})
}
