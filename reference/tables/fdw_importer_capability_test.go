package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceFDWImporterCapability(t *testing.T) {
	fdwScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fdwSQLReader, run func(fixture.Step)) {
		defer func() {
			if _, err := c.ExecContext(p.Ctx, "DROP FOREIGN DATA WRAPPER IF EXISTS fdw_scan_only CASCADE"); err != nil {
				t.Error(err)
			}
		}()
		fdwLive(t, p, b, r, run)
		handler := "fdw_scan_only_handler"
		if b.PG {
			run(fixture.Exec("CREATE EXTENSION IF NOT EXISTS file_fdw", 0))
			handler = "file_fdw_handler"
		}
		run(fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_scan_only HANDLER "+handler+" NO VALIDATOR", 0))
		run(fixture.Exec("CREATE SERVER fdw_scan_only_server FOREIGN DATA WRAPPER fdw_scan_only", 0))
		before := r.snapshot()
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA actual_remote FROM SERVER fdw_scan_only_server INTO public", fdwDiagnostic("HV00P", `foreign-data wrapper "fdw_scan_only" does not support IMPORT FOREIGN SCHEMA`)))
		run(fixture.ErrorDiagnostic("CREATE FOREIGN DATA WRAPPER fdw_unknown HANDLER fdw_uninstalled_handler", fdwDiagnostic("42883", "function fdw_uninstalled_handler() does not exist")))
		if !b.PG && r.snapshot() != before {
			t.Fatal("absent capability attempted source I/O", before, r.snapshot())
		}
		run(fixture.Exec("CREATE SCHEMA fdw_import", 0))
		run(fixture.ErrorDiagnostic("IMPORT FOREIGN SCHEMA fdw_absent_source FROM SERVER fdw_live INTO fdw_import", fdwDiagnostic("HV00Q", `schema "fdw_absent_source" is not present on foreign server "fdw_live"`)))
		fdwSourceExec(t, p, b, r, run, "CREATE SCHEMA fdw_empty", 0)
		run(fixture.Exec("IMPORT FOREIGN SCHEMA fdw_empty FROM SERVER fdw_live INTO fdw_import", 0))
		run(fixture.Select("SELECT count(*) FROM pg_foreign_table f JOIN pg_class c ON c.oid=f.ftrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='fdw_import'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
		if !b.PG {
			state := r.snapshot()
			if state.openPools != 0 || state.openRows != 0 || state.openCursors != 0 {
				t.Fatal("import retained source resources", state)
			}
		}
	})
}
