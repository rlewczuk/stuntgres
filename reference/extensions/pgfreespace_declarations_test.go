package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgFreespaceDeclarations(t *testing.T) {
	fsScenario(t, "fs_part", false, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		fsBoth(t, p, b, c, fsSignature("SELECT * FROM fs_part.pg_freespace(NULL::regclass)", "fs_part.pg_freespace(regclass)"))
		for _, q := range []string{"BEGIN", "CREATE EXTENSION pg_freespacemap SCHEMA fs_part", "ROLLBACK"} {
			fsRun(t, p, b, c, fixture.Exec(q, 0))
		}
		fsBoth(t, p, b, c, fsSignature("SELECT * FROM fs_part.pg_freespace(NULL::regclass)", "fs_part.pg_freespace(regclass)"))
		// A failed member collision must not publish either the extension or its
		// other member. Use a genuine existing SQL routine, not a forged catalog.
		fsRun(t, p, b, c, fixture.Exec("CREATE FUNCTION fs_part.pg_freespace(regclass,bigint) RETURNS smallint LANGUAGE SQL AS 'SELECT 7::smallint'", 0))
		s := fsError("CREATE EXTENSION pg_freespacemap SCHEMA fs_part", "42723", "function \"pg_freespace\" already exists with same argument types")
		s.SelectRows = false
		s.Diagnostic.Context = "SQL statement \"CREATE FUNCTION pg_freespace(regclass, bigint)\nRETURNS int2\nAS '$libdir/pg_freespacemap', 'pg_freespace'\nLANGUAGE C STRICT PARALLEL SAFE\"\nextension script file \"pg_freespacemap--1.1.sql\", near line 7"
		fsRun(t, p, b, c, s)
		fsBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_freespacemap'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fsBoth(t, p, b, c, fsSignature("SELECT * FROM fs_part.pg_freespace(NULL::regclass)", "fs_part.pg_freespace(regclass)"))
		fsRun(t, p, b, c, fixture.Exec("DROP FUNCTION fs_part.pg_freespace(regclass,bigint)", 0))
		// The wrapper collision occurs after scalar staging. Preserve the real
		// pre-existing SQL routine, including its OID, declaration and NULL result.
		fsRun(t, p, b, c, fixture.Exec("CREATE FUNCTION fs_part.pg_freespace(regclass) RETURNS smallint LANGUAGE SQL AS 'SELECT 7::smallint'", 0))
		var originalOID int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'fs_part.pg_freespace(regclass)'::regprocedure::oid::bigint").Scan(&originalOID); err != nil {
			t.Fatal(err)
		}
		if originalOID <= 0 {
			t.Fatal("invalid pre-existing routine identity", originalOID)
		}
		original := fixture.Select("SELECT p.oid::bigint AS oid,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.prosrc AS source,p.probin AS binary,p.prosqlbody IS NOT NULL AS body,l.lanname::text AS language,p.proisstrict AS strict,p.proretset AS sets,p.prosecdef AS definer FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='fs_part'", []string{"oid", "args", "result", "source", "binary", "body", "language", "strict", "sets", "definer"}, []string{"INT8", "TEXT", "INT8", "TEXT", "TEXT", "BOOL", "TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{originalOID, "2205", int64(21), "SELECT 7::smallint", nil, false, "sql", false, false, false}})
		output := fixture.Select("SELECT fs_part.pg_freespace(NULL::regclass) AS avail", []string{"avail"}, []string{"INT2"}, [][]any{{int64(7)}})
		fsBoth(t, p, b, c, original)
		fsBoth(t, p, b, c, output)
		s = fsError("CREATE EXTENSION pg_freespacemap SCHEMA fs_part", "42723", "function \"pg_freespace\" already exists with same argument types")
		s.SelectRows = false
		// Pinned 18.6 fails in the legacy 1.1 installation script, before the
		// update to the final 1.3 BEGIN ATOMIC member body.
		s.Diagnostic.Context = "SQL statement \"CREATE FUNCTION\n  pg_freespace(rel regclass, blkno OUT bigint, avail OUT int2)\nRETURNS SETOF RECORD\nAS $$\n  SELECT blkno, pg_freespace($1, blkno) AS avail\n  FROM generate_series(0, pg_relation_size($1) / current_setting('block_size')::bigint - 1) AS blkno;\n$$\nLANGUAGE SQL PARALLEL SAFE\"\nextension script file \"pg_freespacemap--1.1.sql\", near line 13"
		fsRun(t, p, b, c, s)
		fsBoth(t, p, b, c, fixture.Select("SELECT count(*) AS n FROM pg_extension WHERE extname='pg_freespacemap'", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		fsBoth(t, p, b, c, fsSignature("SELECT * FROM fs_part.pg_freespace(NULL::regclass,0::bigint)", "fs_part.pg_freespace(regclass, bigint)"))
		fsBoth(t, p, b, c, original)
		fsBoth(t, p, b, c, output)
		fsRun(t, p, b, c, fixture.Exec("DROP FUNCTION fs_part.pg_freespace(regclass)", 0))
		fsRun(t, p, b, c, fixture.Exec("CREATE EXTENSION pg_freespacemap SCHEMA fs_part", 0))
		s = fsError("CREATE EXTENSION pg_freespacemap SCHEMA fs_part", "42710", "extension \"pg_freespacemap\" already exists")
		s.SelectRows = false
		fsRun(t, p, b, c, s)
		fsBoth(t, p, b, c, fixture.Select("SELECT extversion,extrelocatable,obj_description(oid,'pg_extension') AS description FROM pg_extension WHERE extname='pg_freespacemap'", []string{"extversion", "extrelocatable", "description"}, []string{"TEXT", "BOOL", "TEXT"}, [][]any{{"1.3", true, "examine the free space map (FSM)"}}))
		fsBoth(t, p, b, c, fixture.Select("SELECT version,superuser,trusted,relocatable FROM pg_available_extension_versions WHERE name='pg_freespacemap' AND version='1.3'", []string{"version", "superuser", "trusted", "relocatable"}, []string{"TEXT", "BOOL", "BOOL", "BOOL"}, [][]any{{"1.3", true, false, true}}))
		q := "SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proallargtypes::text AS allargs,p.proargmodes::text AS modes,p.proargnames::text AS names,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,p.procost::bigint AS cost,p.prorows::bigint AS rows,p.prosqlbody IS NOT NULL AS body,p.prosecdef AS definer FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang WHERE n.nspname='fs_part' AND p.proname='pg_freespace' ORDER BY p.pronargs"
		fsBoth(t, p, b, c, fixture.Select(q, []string{"nargs", "args", "result", "allargs", "modes", "names", "sets", "strict", "volatility", "parallel", "source", "binary", "language", "cost", "rows", "body", "definer"}, []string{"INT4", "TEXT", "INT8", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "INT8", "BOOL", "BOOL"}, [][]any{
			{int64(1), "2205", int64(2249), "{2205,20,21}", "{i,o,o}", "{rel,blkno,avail}", true, false, "v", "s", "", nil, "sql", int64(100), int64(1000), true, false},
			{int64(2), "2205 20", int64(21), nil, nil, nil, false, true, "v", "s", "pg_freespace", "$libdir/pg_freespacemap", "c", int64(1), int64(0), false, false},
		}))
		fsDependency(t, p, b, c, "fs_part.pg_freespace(regclass,bigint)", "fs_part.pg_freespace(regclass)")
		q = "SELECT count(*) AS members,count(DISTINCT p.oid) AS identities,bool_and(p.oid>0 AND p.proacl=i.initprivs) AS initial_acl FROM pg_extension e JOIN pg_depend d ON d.refclassid='pg_extension'::regclass AND d.refobjid=e.oid AND d.classid='pg_proc'::regclass AND d.deptype='e' JOIN pg_proc p ON p.oid=d.objid JOIN pg_init_privs i ON i.classoid='pg_proc'::regclass AND i.objoid=p.oid AND i.objsubid=0 AND i.privtype='e' WHERE e.extname='pg_freespacemap'"
		fsBoth(t, p, b, c, fixture.Select(q, []string{"members", "identities", "initial_acl"}, []string{"INT8", "INT8", "BOOL"}, [][]any{{int64(2), int64(2), true}}))
		fsRun(t, p, b, c, fixture.Exec("CREATE TABLE fs_part.heap(k integer)", 0))
		fsProbe(t, p, b, c, "fs_part.heap", 0, int64(0))
		fsEmpty(t, p, b, c, "fs_part.heap")
		fsBoth(t, p, b, c, fsMetadata(fixture.Select("SELECT fs_part.pg_freespace(NULL::regclass,0)", []string{"pg_freespace"}, []string{"INT2"}, [][]any{{nil}}), b.PG))
		fsBoth(t, p, b, c, fixture.Select("SELECT * FROM fs_part.pg_freespace(rel=>'fs_part.heap'::regclass)", fsNames, fsTypes, nil))
		fsRun(t, p, b, c, fixture.Exec("SET search_path=fs_part,pg_catalog", 0))
		defer c.ExecContext(p.Ctx, "RESET search_path")
		fsBoth(t, p, b, c, fixture.Select("SELECT pg_freespace('heap',0)", []string{"pg_freespace"}, []string{"INT2"}, [][]any{{int64(0)}}))
		fsRun(t, p, b, c, fixture.Exec("CREATE SEQUENCE fs_part.eval_once", 0))
		// Both operands execute exactly once, and the wrapper evaluates its
		// relation argument once rather than repeating it for each page.
		fsBoth(t, p, b, c, fixture.Select("SELECT pg_freespace(CASE WHEN nextval('eval_once')>0 THEN 'heap'::regclass ELSE NULL::regclass END,CASE WHEN nextval('eval_once')>0 THEN 0::bigint ELSE 0::bigint END) AS avail", []string{"avail"}, []string{"INT2"}, [][]any{{int64(0)}}))
		fsBoth(t, p, b, c, fixture.Select("SELECT * FROM pg_freespace(CASE WHEN nextval('eval_once')>0 THEN 'heap'::regclass ELSE NULL::regclass END)", fsNames, fsTypes, nil))
		fsBoth(t, p, b, c, fixture.Select("SELECT currval('eval_once') AS n", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		for _, tc := range []struct{ call, signature string }{
			{"pg_freespace()", "fs_part.pg_freespace()"},
			{"pg_freespace(true)", "fs_part.pg_freespace(boolean)"},
			{"pg_freespace(NULL::regclass,0,0)", "fs_part.pg_freespace(regclass, integer, integer)"},
			{"pg_freespace(rel=>NULL::regclass,blkno=>0::bigint)", "fs_part.pg_freespace(rel => regclass, blkno => bigint)"},
			{"pg_freespace(NULL::regclass,blkno=>0::bigint)", "fs_part.pg_freespace(regclass, blkno => bigint)"},
			{"pg_freespace(VARIADIC ARRAY[1::bigint])", "fs_part.pg_freespace(bigint[])"},
		} {
			fsBoth(t, p, b, c, fsSignature("SELECT * FROM fs_part."+tc.call, tc.signature))
		}
		for _, tc := range []struct{ q, message string }{
			{"SELECT fs_part.pg_freespace('heap',0) FILTER (WHERE true)", "FILTER specified, but fs_part.pg_freespace is not an aggregate function"},
			{"SELECT fs_part.pg_freespace('heap',0) OVER ()", "OVER specified, but fs_part.pg_freespace is not a window function nor an aggregate function"},
		} {
			s := fsError(tc.q, "42809", tc.message)
			s.Diagnostic.Position = "8"
			fsBoth(t, p, b, c, s)
		}
	})
}
