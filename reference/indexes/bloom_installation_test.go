package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from REL_18_6 bloom.control and bloom--1.0.sql, not
// allocator OIDs or parity with native. Families/classes refer to builtin
// equality/hash procedures; these are not additional extension members.
func bloomIdentity(run func(fixture.Step), schema string) {
	bloomBoth(run, fixture.Select("SELECT e.extversion,e.extrelocatable,n.nspname,obj_description(e.oid,'pg_extension') AS comment FROM pg_extension e JOIN pg_namespace n ON n.oid=e.extnamespace WHERE e.extname='bloom'", []string{"extversion", "extrelocatable", "nspname", "comment"}, []string{"TEXT", "BOOL", "NAME", "TEXT"}, [][]any{{"1.0", true, schema, "bloom access method - signature file based index"}}))
	bloomBoth(run, fixture.Select("SELECT name,version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='bloom' AND version='1.0'", []string{"name", "version", "superuser", "trusted", "relocatable"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"bloom", "1.0", true, false, true}}))
	bloomBoth(run, fixture.Select("SELECT a.amname,a.amtype,a.amhandler=p.oid AS handler_link,p.pronamespace=n.oid AS handler_schema,p.prorettype='index_am_handler'::regtype AS handler_return,p.proargtypes::text AS args,l.lanname,p.probin,p.prosrc,p.provolatile,p.proisstrict,p.proparallel FROM pg_am a JOIN pg_proc p ON p.oid=a.amhandler JOIN pg_namespace n ON n.nspname=$1 JOIN pg_language l ON l.oid=p.prolang WHERE a.amname='bloom'", []string{"amname", "amtype", "handler_link", "handler_schema", "handler_return", "args", "lanname", "probin", "prosrc", "provolatile", "proisstrict", "proparallel"}, []string{"NAME", "CHAR", "BOOL", "BOOL", "BOOL", "TEXT", "NAME", "TEXT", "TEXT", "CHAR", "BOOL", "CHAR"}, [][]any{{"bloom", "i", true, true, true, "2281", "c", "$libdir/bloom", "blhandler", "v", false, "u"}}, schema))
	bloomBoth(run, fixture.Select("SELECT c.opcname,c.opcdefault,c.opcintype::regtype::text AS input,c.opcmethod=a.oid AS method_link,c.opcfamily=f.oid AS family_link,f.opfmethod=a.oid AS family_method,c.opcnamespace=f.opfnamespace AS same_schema FROM pg_opclass c JOIN pg_am a ON a.oid=c.opcmethod JOIN pg_opfamily f ON f.oid=c.opcfamily JOIN pg_namespace n ON n.oid=c.opcnamespace WHERE a.amname='bloom' AND n.nspname=$1 ORDER BY c.opcname", []string{"opcname", "opcdefault", "input", "method_link", "family_link", "family_method", "same_schema"}, []string{"NAME", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL", "BOOL"}, [][]any{{"int4_ops", true, "integer", true, true, true, true}, {"text_ops", true, "text", true, true, true, true}}, schema))
	bloomBoth(run, fixture.Select("SELECT c.opcname,o.amopstrategy,o.amoppurpose,o.amoplefttype=c.opcintype AS left_type,o.amoprighttype=c.opcintype AS right_type,o.amopopr=CASE WHEN c.opcname='int4_ops' THEN '=(integer,integer)'::regoperator ELSE '=(text,text)'::regoperator END AS equality,p.amprocnum,p.amproclefttype=c.opcintype AS hash_left,p.amprocrighttype=c.opcintype AS hash_right,p.amproc=CASE WHEN c.opcname='int4_ops' THEN 'hashint4(integer)'::regprocedure ELSE 'hashtext(text)'::regprocedure END AS hash_identity FROM pg_opclass c JOIN pg_am a ON a.oid=c.opcmethod JOIN pg_amop o ON o.amopfamily=c.opcfamily JOIN pg_amproc p ON p.amprocfamily=c.opcfamily WHERE a.amname='bloom' ORDER BY c.opcname", []string{"opcname", "amopstrategy", "amoppurpose", "left_type", "right_type", "equality", "amprocnum", "hash_left", "hash_right", "hash_identity"}, []string{"NAME", "INT2", "CHAR", "BOOL", "BOOL", "BOOL", "INT2", "BOOL", "BOOL", "BOOL"}, [][]any{{"int4_ops", int64(1), "s", true, true, true, int64(1), true, true, true}, {"text_ops", int64(1), "s", true, true, true, int64(1), true, true, true}}))
	bloomBoth(run, amCount("SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='bloom') AND deptype='e'", 6))
	for _, tc := range []struct {
		class string
		count int64
	}{{"pg_proc", 1}, {"pg_am", 1}, {"pg_opfamily", 2}, {"pg_opclass", 2}} {
		run(amCount("SELECT count(*) FROM pg_depend WHERE classid=$1::regclass AND refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='bloom') AND deptype='e'", tc.count, tc.class))
	}
}

func TestReferenceBloomInstallation(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "bloom_alt")
	fixture.RoleReferenceCleanup(t, []string{"bloom_reader"})
	bloomScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := bloomRun(t, p, b, c)
		run(fixture.Exec("CREATE TABLE bloom_install(i integer,s text)", 0))
		bloomError(run, "CREATE INDEX bloom_retry ON bloom_install USING bloom(i)", fixture.Diagnostic{Code: "42704", Message: `access method "bloom" does not exist`})
		bloomIndexAbsent(run, "bloom_retry")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Prepared(fixture.Exec("CREATE EXTENSION bloom", 0)))
		bloomIdentity(run, "public")
		run(fixture.Exec("ROLLBACK", 0))
		run(amCount("SELECT count(*) FROM pg_extension WHERE extname='bloom'", 0))
		run(amCount("SELECT count(*) FROM pg_am WHERE amname='bloom'", 0))
		run(fixture.Exec("CREATE ROLE bloom_reader", 0))
		run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO bloom_reader", 0))
		run(fixture.Exec("SET ROLE bloom_reader", 0))
		bloomError(run, "CREATE EXTENSION bloom", fixture.Diagnostic{Code: "42501", Message: `permission denied to create extension "bloom"`, Hint: "Must be superuser to create this extension."})
		run(fixture.Exec("RESET ROLE", 0))
		run(amCount("SELECT count(*) FROM pg_extension WHERE extname='bloom'", 0))

		// A late script failure must undo the earlier CREATE FUNCTION too.
		run(fixture.Exec("CREATE ACCESS METHOD bloom TYPE INDEX HANDLER pg_catalog.bthandler", 0))
		// The pinned PostgreSQL 18.6 oracle exposes the failing script statement.
		conflict := fixture.Diagnostic{Code: "42710", Message: `access method "bloom" already exists`, Context: "SQL statement \"CREATE ACCESS METHOD bloom TYPE INDEX HANDLER blhandler\"\nextension script file \"bloom--1.0.sql\", near line 12"}
		bloomError(run, "CREATE EXTENSION bloom", conflict)
		run(amCount("SELECT count(*) FROM pg_extension WHERE extname='bloom'", 0))
		run(amCount("SELECT count(*) FROM pg_proc WHERE proname='blhandler' AND pronamespace='public'::regnamespace", 0))
		run(fixture.Select("SELECT amhandler='bthandler'::regproc AS unchanged FROM pg_am WHERE amname='bloom'", []string{"unchanged"}, []string{"BOOL"}, [][]any{{true}}))
		run(fixture.Exec("DROP ACCESS METHOD bloom", 0))
		run(fixture.Exec("CREATE SCHEMA bloom_alt", 0))
		run(fixture.Prepared(fixture.Exec("CREATE EXTENSION bloom SCHEMA bloom_alt", 0)))
		bloomIdentity(run, "bloom_alt")
		bloomError(run, "CREATE EXTENSION bloom", fixture.Diagnostic{Code: "42710", Message: `extension "bloom" already exists`})
		bloomIdentity(run, "bloom_alt")
		run(fixture.Exec("CREATE INDEX bloom_retry ON bloom_install USING bloom(i bloom_alt.int4_ops,s bloom_alt.text_ops)", 0))
		run(fixture.Exec("INSERT INTO bloom_install VALUES(7,'a'),(7,'a'),(NULL,NULL)", 3))
		bloomBoth(run, bloomSelect(b, "SELECT i,s FROM bloom_install WHERE s='a'", [][]any{{int64(7), "a"}, {int64(7), "a"}}))
	})
}
