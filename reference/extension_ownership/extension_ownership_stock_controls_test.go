package reference_test

import (
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func ownershipStockAbsenceControls(t *testing.T) {
	for _, scenario := range []struct {
		name                                   string
		commands                               []string
		extension, language, handlers, members int64
		survivor                               string
	}{
		{"absent_no_survivors", []string{`DROP EXTENSION plpgsql CASCADE`}, 0, 0, 0, 0, ""},
		{"absent_detached_validator", []string{`ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_validator(oid)`, `DROP EXTENSION plpgsql CASCADE`}, 0, 0, 1, 0, "plpgsql_validator"},
		{"present_language_and_handler_deleted", []string{`ALTER EXTENSION plpgsql DROP LANGUAGE plpgsql`, `ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()`, `DROP FUNCTION pg_catalog.plpgsql_call_handler() CASCADE`}, 1, 0, 2, 2, ""},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			fixture.OwnershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
				run := fixture.OwnershipRun(t, p, b, c)
				defer ownershipStockCleanup(t, c)()
				check := func(conn *sql.Conn, recorder *fixture.Recorder) {
					r := fixture.OwnershipRunWithRecorder(t, p, conn, recorder)
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_extension WHERE extname='plpgsql'`, scenario.extension))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_language WHERE lanname='plpgsql'`, scenario.language))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_proc WHERE pronamespace='pg_catalog'::regnamespace AND proname IN ('plpgsql_call_handler','plpgsql_inline_handler','plpgsql_validator')`, scenario.handlers))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_depend WHERE refclassid='pg_extension'::regclass AND refobjid=(SELECT oid FROM pg_extension WHERE extname='plpgsql') AND deptype='e'`, scenario.members))
					r(fixture.OwnershipCount(`SELECT count(*) FROM pg_namespace WHERE nspname LIKE '__stuntgres_retire%' OR nspname LIKE '__stuntgres_install%'`, 0))
					if scenario.survivor != "" {
						r(fixture.Select(`SELECT p.proname,p.prosrc,p.prolang=l.oid AS c_language,p.prorettype='void'::regtype AS void_result,p.proargtypes::text AS arguments FROM pg_proc p JOIN pg_language l ON l.lanname='c' WHERE p.pronamespace='pg_catalog'::regnamespace AND p.proname='plpgsql_validator'`, []string{"proname", "prosrc", "c_language", "void_result", "arguments"}, []string{"NAME", "TEXT", "BOOL", "BOOL", "TEXT"}, [][]any{{"plpgsql_validator", "plpgsql_validator", true, true, "26"}}))
					}
				}
				// The independently expected PostgreSQL phase uses real SQL and rolls
				// back global stock changes even on failure. Native controls below
				// repeat the same mutation committed, because Dump excludes private DDL.
				run(fixture.Exec("BEGIN", 0))
				for _, q := range scenario.commands {
					run(fixture.Exec(q, 0))
				}
				check(c, b.Recorder)
				run(fixture.Exec("ROLLBACK", 0))
				if b.PG {
					return
				}
				defer func() {
					if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
						t.Error("restoring stock bootstrap after absence controls", err)
					}
				}()
				for _, q := range scenario.commands {
					run(fixture.Exec(q, 0))
				}
				check(c, b.Recorder)
				saved := fixture.ExtensionDump(t, p.Ctx, p.Native)
				if strings.Contains(saved, `CREATE SCHEMA "pg_`) || strings.Contains(saved, `DROP SCHEMA "pg_`) {
					t.Fatal("stock cleanup attempted reserved namespace lifecycle", saved)
				}
				connector, err := stuntgres.NewDriver().OpenConnector("eo_stock_" + scenario.name)
				if err != nil {
					t.Fatal(err)
				}
				recorded, recorder := fixture.RecordedNativePool(t, connector)
				other := sql.OpenDB(connector)
				defer other.Close()
				for _, destination := range []struct {
					control, recorded *sql.DB
					recorder          *fixture.Recorder
				}{
					{p.Native, b.DB, b.Recorder},
					{other, recorded, recorder},
				} {
					db := destination.control
					if err := stuntgres.Reset(p.Ctx, db); err != nil {
						t.Fatal(err)
					}
					if err := stuntgres.Restore(p.Ctx, db, strings.NewReader(saved)); err != nil {
						t.Fatal(err)
					}
					conn, err := destination.recorded.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					check(conn, destination.recorder)
					conn.Close()
				}
			})
		})
	}
}
