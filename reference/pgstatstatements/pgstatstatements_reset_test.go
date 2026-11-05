package reference_test

import (
	"fmt"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgStatStatementsResetDeclaration(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		q := "SELECT p.pronargs::integer AS nargs,p.proargtypes::text AS args,p.prorettype::bigint AS result,p.proargnames::text AS names,p.pronargdefaults::integer AS defaults,p.proretset AS sets,p.proisstrict AS strict,p.provolatile::text AS volatility,p.proparallel::text AS parallel,p.prosrc AS source,p.probin AS binary,l.lanname::text AS language,p.proowner=e.extowner AS owned,p.pronamespace=e.extnamespace AS placed,d.deptype::text AS membership FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace JOIN pg_language l ON l.oid=p.prolang JOIN pg_depend d ON d.objid=p.oid AND d.classid='pg_proc'::regclass AND d.refclassid='pg_extension'::regclass JOIN pg_extension e ON e.oid=d.refobjid WHERE n.nspname='pgss_part' AND p.proname='pg_stat_statements_reset'"
		s.both(fixture.Select(q,
			[]string{"nargs", "args", "result", "names", "defaults", "sets", "strict", "volatility", "parallel", "source", "binary", "language", "owned", "placed", "membership"},
			[]string{"INT4", "TEXT", "INT8", "TEXT", "INT4", "BOOL", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL", "TEXT"},
			[][]any{{int64(4), "26 26 20 16", int64(1184), "{userid,dbid,queryid,minmax_only}", int64(4), false, true, "v", "s", "pg_stat_statements_reset_1_11", "$libdir/pg_stat_statements", "c", true, true, "e"}}))
		s.both(fixture.Select("SELECT count(*) AS n FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='pgss_part' AND p.proname='pg_stat_statements_reset'", []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		s.both(fixture.Select("SELECT pg_get_function_arguments('"+pgssResetSignature+"'::regprocedure) AS args", []string{"args"}, []string{"TEXT"}, [][]any{{"userid oid DEFAULT 0, dbid oid DEFAULT 0, queryid bigint DEFAULT 0, minmax_only boolean DEFAULT false"}}))
		s.both(fixture.Select("SELECT count(*) AS n FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pgss_part' AND c.relkind='v' AND c.relname IN ('pg_stat_statements','pg_stat_statements_info')", []string{"n"}, []string{"INT8"}, [][]any{{int64(2)}}))
		pgssResetSeed(s)
		before, info := pgssResetSnapshot(s), pgssResetInfo(s)
		held := pgssResetHeld(s, pgssResetCall)
		// Prepare/description is not invocation of this volatile scalar.
		pgssResetEqual(s.t, pgssResetSnapshot(s), before)
		pgssResetEqual(s.t, pgssResetInfo(s), info)
		pgssResetScalar(s, held, pgssResetCall, false)
		pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
	})
}

func TestReferencePgStatStatementsResetArguments(t *testing.T) {
	pgssCase(t, true, func(s *pgssScenario) {
		for _, args := range []string{
			"", "0", "0,0", "0,0,0", "0,0,0,false",
			"userid=>0", "dbid:=0", "queryid=>0", "minmax_only:=false",
			"minmax_only=>false,queryid=>0,dbid=>0,userid=>0", "0,minmax_only:=false,queryid:=0,dbid:=0",
			"0::smallint,0::integer,0::smallint,'off'", "0::bigint,0::bigint,0::integer,'no'",
			"'-1',0,0,'f'", "-1,0,0,false", "4294967295::bigint,0,0,false",
			"0,0,'-9223372036854775808',false", "0,0,'9223372036854775807',false",
		} {
			q := "SELECT pgss_part.pg_stat_statements_reset(" + args + ")"
			pgssResetScalar(s, s.observer, q, false)
			pgssResetScalar(s, pgssResetHeld(s, q), q, false)
		}
		pgssResetScalar(s, s.observer, "SELECT * FROM pgss_part.pg_stat_statements_reset()", false)
		pgssResetSeed(s)
		before, info := pgssResetSnapshot(s), pgssResetInfo(s)
		s.exec("CREATE TABLE pgss_part.pgss_reset_null(u oid,d oid,q bigint,m boolean)")
		s.run(fixture.Exec("INSERT INTO pgss_part.pgss_reset_null VALUES(NULL,0,0,false),(0,NULL,0,false),(0,0,NULL,false),(0,0,0,NULL)", 4))
		for slot, typ := range []string{"oid", "oid", "bigint", "boolean"} {
			args := []string{"0", "0", "0", "false"}
			args[slot] = "NULL::" + typ
			for _, prefix := range []string{"SELECT ", "SELECT * FROM "} {
				q := prefix + "pgss_part.pg_stat_statements_reset(" + strings.Join(args, ",") + ")"
				pgssResetScalar(s, s.observer, q, true)
				pgssResetScalar(s, pgssResetHeld(s, q), q, true)
			}
			args[slot] = "$1::" + typ
			q := "SELECT pgss_part.pg_stat_statements_reset(" + strings.Join(args, ",") + ")"
			held := pgssResetHeld(s, q)
			for range 2 {
				pgssResetScalar(s, held, q, true, nil)
			}
			column := []string{"u", "d", "q", "m"}[slot]
			q = fmt.Sprintf("SELECT pgss_part.pg_stat_statements_reset(u,d,q,m) FROM pgss_part.pgss_reset_null WHERE %s IS NULL", column)
			pgssResetScalar(s, s.observer, q, true)
			pgssResetScalar(s, pgssResetHeld(s, q), q, true)
			pgssResetEqual(s.t, pgssResetSnapshot(s), before)
			pgssResetEqual(s.t, pgssResetInfo(s), info)
		}
		q := "SELECT pgss_part.pg_stat_statements_reset($1::oid,$2::oid,$3::bigint,$4::boolean)"
		pgssResetScalar(s, pgssResetHeld(s, q), q, false, int64(0), int64(0), int64(0), false)
		pgssResetEqual(s.t, pgssResetSnapshot(s), [][]any(nil))
	})
}
