package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceExtensionOwnershipBootstrap(t *testing.T) {
	ownershipScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := ownershipRun(t, p, b, c)
		run(fixture.Exec("CREATE SCHEMA eo_new", 0))
		run(fixture.Select(`SELECT extversion,extrelocatable,extnamespace='pg_catalog'::regnamespace AS stock FROM pg_extension WHERE extname='plpgsql'`, []string{"extversion", "extrelocatable", "stock"}, []string{"TEXT", "BOOL", "BOOL"}, [][]any{{"1.0", false, true}}))
		q := `SELECT d.classid::bigint,d.objid::bigint FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='plpgsql' AND d.deptype='e' ORDER BY d.classid,d.objid`
		before := ownershipSnapshot(t, p, c, q)
		run(fixture.Select(`SELECT d.classid::bigint,count(*) FROM pg_depend d JOIN pg_extension e ON e.oid=d.refobjid WHERE d.refclassid='pg_extension'::regclass AND e.extname='plpgsql' AND d.deptype='e' GROUP BY d.classid ORDER BY d.classid`, []string{"classid", "count"}, []string{"INT8", "INT8"}, [][]any{{int64(1255), int64(3)}, {int64(2612), int64(1)}}))
		run(fixture.Exec("ALTER EXTENSION plpgsql SET SCHEMA pg_catalog", 0))
		run(ownershipError("ALTER EXTENSION plpgsql SET SCHEMA eo_new", "0A000", `extension "plpgsql" does not support SET SCHEMA`))
		// Procedure creation has an independent explicit-transaction admission
		// boundary. Bind the consumer first; each detached state must still execute
		// that genuine retained routine inside the membership transaction.
		run(fixture.Exec(`CREATE OR REPLACE PROCEDURE public.eo_boot(INOUT n integer) LANGUAGE plpgsql AS 'BEGIN n:=n+1; END'`, 0))
		run(fixture.Exec("BEGIN", 0))
		for _, target := range []string{"LANGUAGE plpgsql", "FUNCTION pg_catalog.plpgsql_call_handler()", "FUNCTION pg_catalog.plpgsql_inline_handler(internal)", "FUNCTION pg_catalog.plpgsql_validator(oid)"} {
			run(fixture.Exec("ALTER EXTENSION plpgsql DROP "+target, 0))
			run(fixture.Select(`CALL public.eo_boot(4)`, []string{"n"}, []string{"INT4"}, [][]any{{int64(5)}}))
			run(fixture.Exec("ALTER EXTENSION plpgsql ADD "+target, 0))
			ownershipUnchanged(t, p, c, q, before)
		}
		// Global language identity has handlers, not a fabricated schema field.
		run(ownershipCount(`SELECT count(*) FROM pg_language WHERE lanname='plpgsql' AND lanpltrusted AND lanplcallfoid='pg_catalog.plpgsql_call_handler()'::regprocedure AND laninline='pg_catalog.plpgsql_inline_handler(internal)'::regprocedure AND lanvalidator='pg_catalog.plpgsql_validator(oid)'::regprocedure`, 1))
		run(fixture.Exec(`DO LANGUAGE plpgsql 'DECLARE n integer := 1; BEGIN n := n + 1; END'`, 0))
		run(fixture.Exec("ALTER EXTENSION plpgsql DROP FUNCTION pg_catalog.plpgsql_call_handler()", 0))
		s := ownershipError("DROP FUNCTION pg_catalog.plpgsql_call_handler()", "2BP01", `cannot drop function plpgsql_call_handler() because other objects depend on it`)
		s.Diagnostic.Detail = "extension plpgsql depends on function plpgsql_call_handler()\nfunction eo_boot(integer) depends on language plpgsql"
		s.Diagnostic.Hint = "Use DROP ... CASCADE to drop the dependent objects too."
		run(s)
		run(fixture.Exec("ROLLBACK", 0))
		ownershipUnchanged(t, p, c, q, before)
	})
}
