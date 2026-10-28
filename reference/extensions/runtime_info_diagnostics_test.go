package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuntimeInfoDiagnosticsAndCatalog(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, value := range []string{"syslog", "STDERR", " stderr", "stderr ", "", "a\"b\\c\nλ"} {
			loadError(t, p, b, c, "SELECT pg_current_logfile("+loadSQLLiteral(value)+")", runtimeFormatError(value), false)
			for _, prepared := range []bool{false, true} {
				s := fixture.ErrorDiagnostic("SELECT pg_current_logfile($1::text)", runtimeFormatError(value), value)
				s.Prepared, s.ErrorStage = prepared, "execute"
				fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
			}
		}
		for _, tc := range []struct{ call, signature string }{
			{"pg_jit_available(NULL)", "pg_jit_available(unknown)"},
			{"pg_numa_available(1)", "pg_numa_available(integer)"},
			{"pg_get_loaded_modules(NULL)", "pg_get_loaded_modules(unknown)"},
			{"pg_current_logfile('stderr','csvlog')", "pg_current_logfile(unknown, unknown)"},
			{"pg_current_logfile(true)", "pg_current_logfile(boolean)"},
			{"pg_current_logfile(1)", "pg_current_logfile(integer)"},
			{"pg_current_logfile(format => 'stderr')", "pg_current_logfile(format => unknown)"},
			{"public.pg_current_logfile()", "public.pg_current_logfile()"},
			{"\"PG_CURRENT_LOGFILE\"()", "PG_CURRENT_LOGFILE()"},
			{"pg_jit_available(1,2)", "pg_jit_available(integer, integer)"},
			{"pg_numa_available(NULL)", "pg_numa_available(unknown)"},
			{"pg_get_loaded_modules(1,2)", "pg_get_loaded_modules(integer, integer)"},
			{"public.pg_jit_available()", "public.pg_jit_available()"},
			{"public.pg_numa_available()", "public.pg_numa_available()"},
			{"public.pg_get_loaded_modules()", "public.pg_get_loaded_modules()"},
			{"\"PG_JIT_AVAILABLE\"()", "PG_JIT_AVAILABLE()"},
			{"\"PG_NUMA_AVAILABLE\"()", "PG_NUMA_AVAILABLE()"},
			{"\"PG_GET_LOADED_MODULES\"()", "PG_GET_LOADED_MODULES()"},
		} {
			want := loadDiagnostic("42883", "function "+tc.signature+" does not exist")
			want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
			want.Position = fixture.Position(8)
			loadError(t, p, b, c, "SELECT "+tc.call, want, true)
		}
		want := loadDiagnostic("42883", "function pg_current_logfile(boolean) does not exist")
		want.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		want.Position = fixture.Position(13)
		loadError(t, p, b, c, "SELECT 'λ', pg_current_logfile(true)", want, true)
		query := `SELECT oid::bigint AS oid, proname::text AS name, pronargs::bigint AS nargs,
prorettype::bigint AS result, proisstrict AS strict, provolatile::text AS volatility,
proparallel::text AS parallel, proretset AS is_set, prorows::text AS rows,
procost::text AS cost, proargtypes::text AS inputs, proallargtypes::text AS alltypes,
proargmodes::text AS modes, proargnames::text AS names, pronargdefaults::bigint AS defaults,
prosecdef AS definer, proleakproof AS leakproof, provariadic::bigint AS variadic,
prosupport::oid::bigint AS support, prolang::bigint AS language, prosrc AS source
FROM pg_proc WHERE oid IN (315,3800,3801,4099,6353) ORDER BY oid`
		columns := []string{"oid", "name", "nargs", "result", "strict", "volatility", "parallel", "is_set", "rows", "cost", "inputs", "alltypes", "modes", "names", "defaults", "definer", "leakproof", "variadic", "support", "language", "source"}
		types := []string{"INT8", "TEXT", "INT8", "INT8", "BOOL", "TEXT", "TEXT", "BOOL", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "TEXT", "INT8", "BOOL", "BOOL", "INT8", "INT8", "INT8", "TEXT"}
		rows := [][]any{
			{int64(315), "pg_jit_available", int64(0), int64(16), true, "v", "s", false, "0", "1", "", nil, nil, nil, int64(0), false, false, int64(0), int64(0), int64(12), "pg_jit_available"},
			{int64(3800), "pg_current_logfile", int64(0), int64(25), false, "v", "s", false, "0", "1", "", nil, nil, nil, int64(0), false, false, int64(0), int64(0), int64(12), "pg_current_logfile"},
			{int64(3801), "pg_current_logfile", int64(1), int64(25), false, "v", "s", false, "0", "1", "25", nil, nil, nil, int64(0), false, false, int64(0), int64(0), int64(12), "pg_current_logfile_1arg"},
			{int64(4099), "pg_numa_available", int64(0), int64(16), true, "s", "s", false, "0", "1", "", nil, nil, nil, int64(0), false, false, int64(0), int64(0), int64(12), "pg_numa_available"},
			{int64(6353), "pg_get_loaded_modules", int64(0), int64(2249), true, "v", "r", true, "10", "1", "", "{25,25,25}", "{o,o,o}", "{module_name,version,file_name}", int64(0), false, false, int64(0), int64(0), int64(12), "pg_get_loaded_modules"},
		}
		runtimeSelect(t, p, b, c, query, columns, types, rows)
	})
}
