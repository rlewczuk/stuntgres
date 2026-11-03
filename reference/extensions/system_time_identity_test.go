package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceSystemTimeIdentity(t *testing.T) {
	stCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		stDeclaration(t, p, b, c)
		srRun(t, p, b, c, fixture.Exec("CREATE VIEW st_identity AS SELECT id,payload FROM sr TABLESAMPLE system_time('Infinity')", 0))
		srBoth(t, p, b, c, fixture.ImplicitError("DROP EXTENSION tsm_system_time RESTRICT", fixture.Diagnostic{
			Code:"2BP01", Message:"cannot drop extension tsm_system_time because other objects depend on it",
			Detail:"view st_identity depends on function system_time(internal)",
			Hint:"Use DROP ... CASCADE to drop the dependent objects too.",
		}))
		var original int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'system_time(internal)'::regprocedure::oid").Scan(&original); err != nil || original <= 0 { t.Fatal("handler identity",original,err) }
		for _, q := range []string{
			"CREATE SCHEMA st_moved",
			"ALTER EXTENSION tsm_system_time SET SCHEMA st_moved",
			"ALTER FUNCTION st_moved.system_time(internal) RENAME TO genuine_time",
			"SET search_path TO public",
		} { srRun(t, p, b, c, fixture.Exec(q,0)) }
		srBoth(t, p, b, c, srError("SELECT * FROM sr TABLESAMPLE system_time(1)", "42704", "tablesample method system_time does not exist",30))
		srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM sr TABLESAMPLE st_moved.genuine_time('Infinity') ORDER BY id", srNames,srTypes,srRows()))
		stSampleBoth(t, p, c, "SELECT id,payload FROM sr TABLESAMPLE st_moved.genuine_time(0.25)",srRows())
		srBoth(t, p, b, c, fixture.Select("SELECT id,payload FROM st_identity ORDER BY id", srNames,srTypes,srRows()))
		srBoth(t, p, b, c, fixture.Select("SELECT 'st_moved.genuine_time(internal)'::regprocedure::oid::bigint AS identity", []string{"identity"},[]string{"INT8"},[][]any{{original}}))
		srRun(t,p,b,c,fixture.Exec("CREATE FUNCTION system_time(integer) RETURNS integer LANGUAGE SQL AS 'SELECT 77'",0))
		srBoth(t,p,b,c,fixture.Select("SELECT system_time(1) AS value",[]string{"value"},[]string{"INT4"},[][]any{{int64(77)}}))
		srBoth(t,p,b,c,srError("SELECT * FROM sr TABLESAMPLE system_time(1)","42704","tablesample method system_time does not exist",30))
		srRun(t,p,b,c,fixture.Exec("DROP FUNCTION system_time(integer)",0))
		t.Run("external_internal_signature",func(t *testing.T){
			// Arbitrary PostgreSQL internal/C ABI declarations are external;
			// runtime-phase native binder tests supply a genuine typed descriptor.
			if !b.PG { return }
			srRun(t,p,b,c,fixture.Exec("CREATE FUNCTION system_time(internal) RETURNS integer LANGUAGE internal AS 'int4abs'",0))
			srBoth(t,p,b,c,srError("SELECT * FROM sr TABLESAMPLE system_time(1)","42809","function system_time must return type tsm_handler",30))
			srRun(t,p,b,c,fixture.Exec("DROP FUNCTION system_time(internal)",0))
		})
		srRun(t,p,b,c,fixture.Exec("ALTER FUNCTION st_moved.genuine_time(internal) RENAME TO system_time",0))
		srRun(t,p,b,c,fixture.Exec("ALTER EXTENSION tsm_system_time SET SCHEMA public",0))
		// Savepoint and whole-transaction rollback must restore the same real
		// installation and dependent view, not a name-compatible replacement.
		for _, q := range []string{"BEGIN","SAVEPOINT st_install","DROP EXTENSION tsm_system_time CASCADE","ROLLBACK TO SAVEPOINT st_install"} { srRun(t,p,b,c,fixture.Exec(q,0)) }
		srBoth(t,p,b,c,fixture.Select("SELECT id,payload FROM st_identity ORDER BY id",srNames,srTypes,srRows()))
		srRun(t,p,b,c,fixture.Exec("ROLLBACK",0))
		srBoth(t,p,b,c,fixture.Select("SELECT 'system_time(internal)'::regprocedure::oid::bigint AS identity",[]string{"identity"},[]string{"INT8"},[][]any{{original}}))
		srRun(t,p,b,c,fixture.Exec("DROP EXTENSION tsm_system_time CASCADE",0))
		srBoth(t,p,b,c,srCount("SELECT count(*) AS n FROM pg_class WHERE relname='st_identity' AND relnamespace='public'::regnamespace",0))
		for _, q := range []string{"BEGIN","CREATE EXTENSION tsm_system_time","ROLLBACK"} { srRun(t,p,b,c,fixture.Exec(q,0)) }
		srBoth(t,p,b,c,srCount("SELECT count(*) AS n FROM pg_extension WHERE extname='tsm_system_time'",0))
		srRun(t,p,b,c,fixture.Exec("CREATE EXTENSION tsm_system_time",0))
		stDeclaration(t,p,b,c)
	})
}

func stDeclaration(t *testing.T,p *fixture.Pair,b fixture.Backend,c fixture.Executor) {
	t.Helper()
	srBoth(t,p,b,c,fixture.Select("SELECT extversion,extrelocatable,extnamespace='public'::regnamespace AS namespace,extconfig IS NULL AS no_config,extcondition IS NULL AS no_condition,obj_description(oid,'pg_extension') AS comment FROM pg_extension WHERE extname='tsm_system_time'",[]string{"extversion","extrelocatable","namespace","no_config","no_condition","comment"},[]string{"TEXT","BOOL","BOOL","BOOL","BOOL","TEXT"},[][]any{{"1.0",true,true,true,true,"TABLESAMPLE method which accepts time in milliseconds as a limit"}}))
	srBoth(t,p,b,c,fixture.Select("SELECT version,superuser,trusted,relocatable,schema,requires FROM pg_available_extension_versions WHERE name='tsm_system_time' AND version='1.0'",[]string{"version","superuser","trusted","relocatable","schema","requires"},[]string{"TEXT","BOOL","BOOL","BOOL","NAME","_NAME"},[][]any{{"1.0",true,true,true,nil,nil}}))
	srBoth(t,p,b,c,fixture.Select("SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS arguments,p.prorettype::bigint AS result,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language FROM pg_proc p JOIN pg_language l ON l.oid=p.prolang WHERE p.oid='system_time(internal)'::regprocedure",[]string{"nargs","arguments","result","strict","volatility","parallel","source","binary","language"},[]string{"INT4","TEXT","INT8","BOOL","TEXT","TEXT","TEXT","TEXT","TEXT"},[][]any{{int64(1),"2281",int64(3310),true,"v","u","tsm_system_time_handler","$libdir/tsm_system_time","c"}}))
	srBoth(t,p,b,c,srCount("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='tsm_system_time' AND d.deptype='e' AND d.classid='pg_proc'::regclass AND d.objid='system_time(internal)'::regprocedure",1))
	srBoth(t,p,b,c,srCount("SELECT count(*) AS n FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='tsm_system_time' AND d.deptype='e'",1))
}
