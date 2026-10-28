package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRuntimeInfoValues(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		runtimeSelect(t, p, b, c, "SHOW logging_collector", []string{"logging_collector"}, []string{"TEXT"}, [][]any{{"off"}})
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile() AS a, pg_current_logfile(NULL::text) AS b, pg_current_logfile('stderr') AS c, pg_current_logfile('csvlog') AS d, pg_current_logfile('jsonlog') AS e", []string{"a", "b", "c", "d", "e"}, []string{"TEXT", "TEXT", "TEXT", "TEXT", "TEXT"}, [][]any{{nil, nil, nil, nil, nil}})
		for _, query := range []string{"SELECT pg_current_logfile(NULL) AS logfile", "SELECT pg_current_logfile('stderr'::varchar) AS logfile", "SELECT pg_current_logfile('std' || 'err') AS logfile"} {
			runtimeSelect(t, p, b, c, query, []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
		}
		for _, value := range []any{nil, "stderr", "csvlog", "jsonlog"} {
			runtimeSelect(t, p, b, c, "SELECT pg_current_logfile($1::varchar) AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}}, value)
		}
		loadExec(t, p, b, c, "CREATE SEQUENCE runtime_format_seq")
		runtimeSelect(t, p, b, c, "SELECT pg_current_logfile(CASE WHEN nextval('runtime_format_seq') > 0 THEN 'stderr' ELSE 'syslog' END) AS logfile", []string{"logfile"}, []string{"TEXT"}, [][]any{{nil}})
		runtimeSelect(t, p, b, c, "SELECT last_value FROM runtime_format_seq", []string{"last_value"}, []string{"INT8"}, [][]any{{int64(2)}})
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT pg_current_logfile((1/$1::integer)::text)", loadDiagnostic("22012", "division by zero"), 0)
			s.Prepared, s.ErrorStage = prepared, "execute"
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		loadExec(t, p, b, c, "SET jit = off")
		defer c.ExecContext(p.Ctx, "RESET jit")
		runtimeSelect(t, p, b, c, "SELECT pg_jit_available() AS jit, pg_numa_available() AS numa", []string{"jit", "numa"}, []string{"BOOL", "BOOL"}, [][]any{{false, false}})
	})
}

func TestReferenceRuntimeInfoCapabilities(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		query := "SELECT pg_jit_available() AS jit, pg_numa_available() AS numa"
		stmt, err := c.PrepareContext(p.Ctx, query); if err != nil { t.Fatal(err) }; defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: query}
		defer c.ExecContext(p.Ctx, "RESET jit")
		// Provisional independent pinned-image expectations: LLVM provider is
		// installed, whereas container NUMA initialization is unavailable.
		// Root must resolve both literals with real fixture evidence.
		for _, setting := range []string{"off", "on", "off"} {
			loadExec(t, p, b, c, "SET jit = "+setting)
			wantJIT := b.PG && setting == "on"
			want := [][]any{{wantJIT, false}}
			runtimeSelect(t, p, b, c, query, []string{"jit", "numa"}, []string{"BOOL", "BOOL"}, want)
			runtimeHeldSelect(t, p, b, held, []string{"jit", "numa"}, []string{"BOOL", "BOOL"}, want)
		}
	})
}

func TestReferenceRuntimeInfoModules(t *testing.T) {
	loadScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		// Constrain the candidate bag on PG: pooled sessions may already have
		// genuine libraries, and DISCARD ALL does not unload them.
		filter := ""; if b.PG { filter = " WHERE module_name = 'runtime_contract_absent'" }
		runtimeSelect(t, p, b, c, "SELECT * FROM pg_get_loaded_modules()"+filter, []string{"module_name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
		runtimeSelect(t, p, b, c, "SELECT m.module_name, m.version, m.file_name, m.ordinality FROM pg_get_loaded_modules() WITH ORDINALITY AS m"+filter, []string{"module_name", "version", "file_name", "ordinality"}, []string{"TEXT", "TEXT", "TEXT", "INT8"}, nil)
		runtimeSelect(t, p, b, c, "SELECT name, version, file_name FROM pg_get_loaded_modules() AS m(name) WHERE name = 'runtime_contract_absent'", []string{"name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
		targetQuery := "SELECT pg_get_loaded_modules() AS module"
		paddingQuery := "SELECT * FROM ROWS FROM(pg_get_loaded_modules(), generate_series(1, 2)) AS r(module_name, version, file_name, n)"
		padding := [][]any{{nil, nil, nil, int64(1)}, {nil, nil, nil, int64(2)}}
		if b.PG {
			// Physical inventory is backend-local and can be nonempty: these
			// bounded lanes still independently assert all output descriptors.
			targetQuery += " WHERE false"
			paddingQuery += " WHERE false"
			padding = nil
		}
		runtimeSelect(t, p, b, c, targetQuery, []string{"module"}, []string{"RECORD"}, nil)
		runtimeSelect(t, p, b, c, paddingQuery, []string{"module_name", "version", "file_name", "n"}, []string{"TEXT", "TEXT", "TEXT", "INT4"}, padding)
		runtimeSelect(t, p, b, c, "SELECT name, release, file FROM pg_get_loaded_modules() AS m(name, release, file) WHERE name = 'runtime_contract_absent'", []string{"name", "release", "file"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
		runtimeModules(t, p, b, c)
		loadExec(t, p, b, c, "CREATE EXTENSION hstore")
		defer c.ExecContext(p.Ctx, "DROP EXTENSION hstore")
		runtimeSelect(t, p, b, c, "SELECT ('k=>v'::hstore)->'k' AS value", []string{"value"}, []string{"TEXT"}, [][]any{{"v"}})
		runtimeSelect(t, p, b, c, "SELECT * FROM pg_get_loaded_modules()"+filter, []string{"module_name", "version", "file_name"}, []string{"TEXT", "TEXT", "TEXT"}, nil)
	})
}
