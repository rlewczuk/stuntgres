package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func pkeyAnalysisFailure(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, q, message string) {
	t.Helper()
	for _, prepared := range []bool{false, true} {
		name := "direct"
		if prepared {
			name = "prepared"
		}
		t.Run(name, func(t *testing.T) {
			s := pkeyError(q, "42883", message)
			s.Diagnostic.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			s.Diagnostic.Position = fixture.Position(15)
			if prepared {
				s.Prepared, s.ErrorStage = true, "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		})
	}
}

func pkeyNominalLinks() fixture.Step {
	// Compare relationships, never allocated OID literals. A RECORD-shaped
	// impostor cannot satisfy the installed named type and reciprocal links.
	q := "SELECT p.prorettype=t.oid AS result,t.typtype::text AS kind,c.relkind::text AS backing_kind,c.reltype=t.oid AS backing_link,a.typelem=t.oid AS array_element,a.typtype::text AS array_kind,p.proowner=t.typowner AND t.typowner=a.typowner AND t.typowner=c.relowner AND t.typowner=e.extowner AS owners FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_type t ON t.typnamespace=n.oid AND t.typname='dblink_pkey_results' JOIN pg_type a ON a.oid=t.typarray JOIN pg_class c ON c.oid=t.typrelid JOIN pg_extension e ON e.extnamespace=n.oid AND e.extname='dblink' WHERE n.nspname='pk_ext' AND p.proname='dblink_get_pkey' AND p.proargtypes::text='25'"
	return fixture.Select(q, []string{"result", "kind", "backing_kind", "backing_link", "array_element", "array_kind", "owners"}, []string{"BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "BOOL"}, [][]any{{true, "c", "c", true, true, "b", true}})
}

func TestReferenceDblinkGetPkeyInstalled(t *testing.T) {
	pkeyCase(t, false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		t.Run("absent_extension", func(t *testing.T) {
			pkeyAnalysisFailure(t, p, b, c, "SELECT * FROM pk_ext.dblink_get_pkey(NULL::text)", "function pk_ext.dblink_get_pkey(text) does not exist")
		})
		run(fixture.Exec("CREATE EXTENSION dblink SCHEMA pk_ext", 0))
		check := func(name string, s fixture.Step) {
			t.Run(name, func(t *testing.T) { pkeyRoutes(t, p, b, c, nil, s) })
		}
		// dblink--1.2.sql declares an unnamed text argument and a genuine
		// SETOF composite, STRICT and PARALLEL RESTRICTED. Other values are
		// the CREATE FUNCTION defaults for a C-language routine.
		q := "SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.proargnames::text AS names,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.pronargdefaults::integer AS defaults,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosecdef AS definer,p.proleakproof AS leakproof,p.procost,p.prorows,p.prosrc,p.probin,l.lanname::text AS language FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='pk_ext' AND p.proname='dblink_get_pkey' AND p.proargtypes::text='25'"
		check("declaration", fixture.Select(q,
			[]string{"nargs", "args", "names", "allargs", "modes", "defaults", "sets", "strict", "volatility", "parallel", "definer", "leakproof", "procost", "prorows", "prosrc", "probin", "language"},
			[]string{"INT4", "TEXT", "TEXT", "TEXT", "TEXT", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "BOOL", "BOOL", "FLOAT4", "FLOAT4", "TEXT", "TEXT", "TEXT"},
			[][]any{{int64(1), "25", nil, nil, nil, int64(0), true, true, "v", "r", false, false, float64(1), float64(1000), "dblink_get_pkey", "$libdir/dblink", "c"}}))
		check("nominal_links", pkeyNominalLinks())
		q = "SELECT a.attnum,a.attname::text AS name,a.atttypid::bigint AS type,a.atttypmod,a.attnotnull,a.attisdropped FROM pg_attribute a JOIN pg_type t ON t.typrelid=a.attrelid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE n.nspname='pk_ext' AND t.typname='dblink_pkey_results' AND a.attnum>0 ORDER BY a.attnum"
		check("declared_fields", fixture.Select(q, []string{"attnum", "name", "type", "atttypmod", "attnotnull", "attisdropped"}, []string{"INT2", "TEXT", "INT8", "INT4", "BOOL", "BOOL"}, [][]any{{int64(1), "position", int64(23), int64(-1), false, false}, {int64(2), "colname", int64(25), int64(-1), false, false}}))
		// Standalone composite and implicit array both belong to the extension;
		// backing relation is internally dependent on its type, not an e member.
		for _, edge := range []struct {
			name, query string
			count       int64
		}{
			{"function_member", "SELECT count(*) AS n FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='dblink' AND n.nspname='pk_ext' AND p.proname='dblink_get_pkey' AND p.proargtypes::text='25'", 1},
			{"type_members", "SELECT count(*) AS n FROM pg_depend d JOIN pg_type t ON t.oid=d.objid JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1247 AND d.refclassid=3079 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='e' AND e.extname='dblink' AND n.nspname='pk_ext' AND t.typname IN ('dblink_pkey_results','_dblink_pkey_results')", 2},
			{"backing_internal", "SELECT count(*) AS n FROM pg_depend d JOIN pg_type t ON t.oid=d.refobjid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE d.classid=1259 AND d.objid=t.typrelid AND d.refclassid=1247 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='i' AND n.nspname='pk_ext' AND t.typname='dblink_pkey_results'", 1},
			{"array_internal", "SELECT count(*) AS n FROM pg_depend d JOIN pg_type t ON t.oid=d.refobjid JOIN pg_namespace n ON n.oid=t.typnamespace WHERE d.classid=1247 AND d.objid=t.typarray AND d.refclassid=1247 AND d.objsubid=0 AND d.refobjsubid=0 AND d.deptype='i' AND n.nspname='pk_ext' AND t.typname='dblink_pkey_results'", 1},
			{"backing_not_member", "SELECT count(*) AS n FROM pg_depend d JOIN pg_type t ON t.typrelid=d.objid JOIN pg_namespace n ON n.oid=t.typnamespace JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1259 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='dblink' AND n.nspname='pk_ext' AND t.typname='dblink_pkey_results'", 0},
		} {
			check(edge.name, fixture.Select(edge.query, []string{"n"}, []string{"INT8"}, [][]any{{edge.count}}))
		}
		run(fixture.Exec("CREATE TABLE pkey_installed(id integer PRIMARY KEY)", 0))
		check("nominal_row_value", fixture.Select("SELECT pg_typeof(k)::oid='pk_ext.dblink_pkey_results'::regtype::oid AS nominal,k::text AS value FROM pk_ext.dblink_get_pkey('pkey_installed') AS k ORDER BY position", []string{"nominal", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, "(1,id)"}}))
		check("nominal_unaliased_row", fixture.Select("SELECT pg_typeof(dblink_get_pkey)::oid='pk_ext.dblink_pkey_results'::regtype::oid AS nominal,dblink_get_pkey::text AS value FROM pk_ext.dblink_get_pkey('pkey_installed') ORDER BY position", []string{"nominal", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, "(1,id)"}}))
		check("nominal_column_aliases", fixture.Select("SELECT pg_typeof(k)::oid='pk_ext.dblink_pkey_results'::regtype::oid AS nominal,k::text AS value FROM pk_ext.dblink_get_pkey('pkey_installed') AS k(ordinal,label) ORDER BY ordinal", []string{"nominal", "value"}, []string{"BOOL", "TEXT"}, [][]any{{true, "(1,id)"}}))
		check("nominal_array", fixture.Select("SELECT pg_typeof(ARRAY[ROW(1,'id')::pk_ext.dblink_pkey_results])::oid=t.typarray AS nominal FROM pg_type t WHERE t.oid='pk_ext.dblink_pkey_results'::regtype", []string{"nominal"}, []string{"BOOL"}, [][]any{{true}}))
		t.Run("unnamed_input_rejects_relname", func(t *testing.T) {
			pkeyAnalysisFailure(t, p, b, c, "SELECT * FROM pk_ext.dblink_get_pkey(relname => 'pkey_installed'::text)", "function pk_ext.dblink_get_pkey(relname => text) does not exist")
		})
		check("constructors_unchanged", fixture.Select("SELECT p.proname::text AS name,p.prorettype::bigint AS result,p.proretset AS sets,p.proisstrict AS strict FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pk_ext' AND p.proname IN ('dblink_build_sql_delete','dblink_build_sql_insert','dblink_build_sql_update') ORDER BY p.proname", []string{"name", "result", "sets", "strict"}, []string{"TEXT", "INT8", "BOOL", "BOOL"}, [][]any{{"dblink_build_sql_delete", int64(25), false, true}, {"dblink_build_sql_insert", int64(25), false, true}, {"dblink_build_sql_update", int64(25), false, true}}))
		check("constructors_callable", fixture.Select("SELECT pk_ext.dblink_build_sql_delete(NULL::text,NULL::int2vector,NULL::integer,NULL::text[]) AS del,pk_ext.dblink_build_sql_insert(NULL::text,NULL::int2vector,NULL::integer,NULL::text[],NULL::text[]) AS ins,pk_ext.dblink_build_sql_update(NULL::text,NULL::int2vector,NULL::integer,NULL::text[],NULL::text[]) AS upd", []string{"del", "ins", "upd"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil}}))
	})
}
