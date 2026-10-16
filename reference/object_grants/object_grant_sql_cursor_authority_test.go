package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceObjectGrantSQLCursorAuthority(t *testing.T) {
	for _, fetched := range []bool{false, true} {
		name := "before_first_fetch"
		if fetched {
			name = "after_first_fetch"
		}
		t.Run(name, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				admin, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer admin.Close()
				adminRun := func(s fixture.Step) {
					s.Prepared = prepared
					if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }) {
						t.FailNow()
					}
				}
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
						t.Error("cursor cleanup rollback", err)
					}
					objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
				}()
				errorStep := func(q, code, message string) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
					s.ErrorStage = "execute"
					return s
				}
				batch := func(q string, rows [][]any) {
					run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
				}
				adminRun(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_cursor_input(id integer,n integer)", 0))
				run(fixture.Exec("INSERT INTO og_cursor_input VALUES(1,-1),(2,-2)", 2))
				run(fixture.Exec("CREATE TABLE og_cursor_marker(id integer)", 0))
				adminRun(fixture.Exec("GRANT SELECT ON og_cursor_input TO og_object_reader", 0))
				adminRun(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
				query := "SELECT id,pg_catalog.abs(n) AS v FROM (SELECT id,n FROM og_cursor_input ORDER BY id OFFSET 0) AS ordered"
				declare := "DECLARE og_cursor NO SCROLL CURSOR FOR " + query

				// Row dependence prevents immutable constant folding. DECLARE starts
				// the executor even though it has not yet demanded a result row.
				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("INSERT INTO og_cursor_marker VALUES(1)", 1))
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				run(errorStep(declare, "42501", "permission denied for function abs"))
				// Probe the aborted transaction with a direct call: a fresh
				// extended-protocol Prepare can itself reject an aborted block.
				fixture.RunStep(t, p.Ctx, c, b.Recorder, errorStep("SELECT 1", "25P02", "current transaction is aborted, commands ignored until end of transaction block"))
				run(fixture.Exec("ROLLBACK", 0))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT id FROM og_cursor_marker", []string{"id"}, []string{"INT4"}, nil))
				run(errorStep("FETCH ALL FROM og_cursor", "34000", `cursor "og_cursor" does not exist`))
				adminRun(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", 0))

				run(fixture.Exec("BEGIN", 0))
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				run(fixture.Exec(declare, 0))
				if fetched {
					batch("FETCH 1 FROM og_cursor", [][]any{{int64(1), int64(1)}})
				}
				adminRun(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader", 0))
				if !fetched {
					batch("FETCH 1 FROM og_cursor", [][]any{{int64(1), int64(1)}})
				}
				batch("FETCH ALL FROM og_cursor", [][]any{{int64(2), int64(2)}})
				// Exec observes the actual exhausted FETCH completion count.
				run(fixture.Exec("FETCH ALL FROM og_cursor", 0))
				batch("FETCH ALL FROM og_cursor", nil)
				// The old initialized portal is not authority for a new one.
				run(fixture.Exec("SAVEPOINT og_cursor_denied", 0))
				run(errorStep("DECLARE og_cursor_new NO SCROLL CURSOR FOR "+query, "42501", "permission denied for function abs"))
				run(fixture.Exec("ROLLBACK TO og_cursor_denied", 0))
				run(fixture.Exec("RELEASE og_cursor_denied", 0))
				run(fixture.Exec("CLOSE og_cursor", 0))
				run(fixture.Exec("COMMIT", 0))
				run(fixture.Exec("RESET ROLE", 0))
				run(errorStep("FETCH ALL FROM og_cursor", "34000", `cursor "og_cursor" does not exist`))
				run(errorStep("FETCH ALL FROM og_cursor_new", "34000", `cursor "og_cursor_new" does not exist`))
			})
		})
	}
}

func TestReferenceObjectGrantSQLCursorHoldAuthority(t *testing.T) {
	for _, route := range []string{"explicit_before_first", "explicit_after_first", "implicit"} {
		t.Run(route, func(t *testing.T) {
			objectGrantScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step), prepared bool) {
				admin, err := b.DB.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer admin.Close()
				adminRun := func(s fixture.Step) {
					s.Prepared = prepared
					if !t.Run(s.Query, func(t *testing.T) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }) {
						t.FailNow()
					}
				}
				defer func() {
					if _, err := c.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
						t.Error("hold cleanup rollback", err)
					}
					if _, err := c.ExecContext(p.Ctx, "CLOSE ALL"); err != nil {
						t.Error("hold cleanup portals", err)
					}
					objectGrantRoutineCleanup(t, p, c, []string{"pg_catalog.abs(integer)"})
				}()
				errorStep := func(q, code, message string) fixture.Step {
					s := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message})
					s.ErrorStage = "execute"
					return s
				}
				batch := func(q string, rows [][]any) {
					run(fixture.Select(q, []string{"id", "v"}, []string{"INT4", "INT4"}, rows))
				}
				adminRun(fixture.Exec("GRANT USAGE ON SCHEMA public TO og_object_reader", 0))
				run(fixture.Exec("CREATE TABLE og_hold_input(id integer,n integer)", 0))
				run(fixture.Exec("INSERT INTO og_hold_input VALUES(1,-1),(2,-2)", 2))
				run(fixture.Exec("CREATE TABLE og_hold_marker(id integer)", 0))
				adminRun(fixture.Exec("GRANT SELECT ON og_hold_input TO og_object_reader", 0))
				adminRun(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM PUBLIC", 0))
				query := "SELECT id,pg_catalog.abs(n) AS v FROM (SELECT id,n FROM og_hold_input ORDER BY id OFFSET 0) AS ordered"
				declare := "DECLARE og_hold NO SCROLL CURSOR WITH HOLD FOR " + query
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				run(errorStep(declare, "42501", "permission denied for function abs"))
				run(errorStep("FETCH ALL FROM og_hold", "34000", `cursor "og_hold" does not exist`))
				run(fixture.Exec("RESET ROLE", 0))
				adminRun(fixture.Exec("GRANT EXECUTE ON FUNCTION pg_catalog.abs(integer) TO og_object_reader", 0))

				// Real provider failure during remaining-row materialization, not an
				// invented fresh EXECUTE check at COMMIT. Preserve an older export.
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				run(fixture.Exec("DECLARE og_hold_old NO SCROLL CURSOR WITH HOLD FOR "+query, 0))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("UPDATE og_hold_input SET n='-2147483648'::integer WHERE id=2", 1))
				broken := "DECLARE og_hold_broken NO SCROLL CURSOR WITH HOLD FOR " + query
				if route != "implicit" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO og_hold_marker VALUES(9)", 1))
				}
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				if route == "implicit" {
					run(errorStep(broken, "22003", "integer out of range"))
				} else {
					run(fixture.Exec(broken, 0))
					if route == "explicit_after_first" {
						batch("FETCH 1 FROM og_hold_broken", [][]any{{int64(1), int64(1)}})
					}
					run(errorStep("COMMIT", "22003", "integer out of range"))
				}
				// COMMIT failure has ended the transaction, not left a 25P02 block.
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT id FROM og_hold_marker", []string{"id"}, []string{"INT4"}, nil))
				run(errorStep("FETCH ALL FROM og_hold_broken", "34000", `cursor "og_hold_broken" does not exist`))
				batch("FETCH ALL FROM og_hold_old", [][]any{{int64(1), int64(1)}, {int64(2), int64(2)}})
				run(fixture.Exec("CLOSE og_hold_old", 0))
				run(fixture.Exec("UPDATE og_hold_input SET n=-2 WHERE id=2", 1))

				if route != "implicit" {
					run(fixture.Exec("BEGIN", 0))
					run(fixture.Exec("INSERT INTO og_hold_marker VALUES(7)", 1))
				}
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				run(fixture.Exec(declare, 0))
				if route == "explicit_after_first" {
					batch("FETCH 1 FROM og_hold", [][]any{{int64(1), int64(1)}})
				}
				adminRun(fixture.Exec("REVOKE EXECUTE ON FUNCTION pg_catalog.abs(integer) FROM og_object_reader", 0))
				if route != "implicit" {
					run(fixture.Exec("COMMIT", 0))
				}
				// Successful promotion owns values. Changing the original source and
				// denying every fresh invocation cannot change those exported cells.
				run(errorStep("DECLARE og_hold_new NO SCROLL CURSOR WITH HOLD FOR "+query, "42501", "permission denied for function abs"))
				run(fixture.Exec("RESET ROLE", 0))
				run(fixture.Exec("UPDATE og_hold_input SET n=-99", 2))
				markerRows := [][]any(nil)
				if route != "implicit" {
					markerRows = [][]any{{int64(7)}}
				}
				run(fixture.Select("SELECT id FROM og_hold_marker", []string{"id"}, []string{"INT4"}, markerRows))
				run(fixture.Exec("SET ROLE og_object_reader", 0))
				if route != "explicit_after_first" {
					batch("FETCH 1 FROM og_hold", [][]any{{int64(1), int64(1)}})
				}
				batch("FETCH ALL FROM og_hold", [][]any{{int64(2), int64(2)}})
				run(fixture.Exec("FETCH ALL FROM og_hold", 0))
				batch("FETCH ALL FROM og_hold", nil)
				run(fixture.Exec("CLOSE og_hold", 0))
				run(errorStep("FETCH ALL FROM og_hold", "34000", `cursor "og_hold" does not exist`))
				run(errorStep("FETCH ALL FROM og_hold_new", "34000", `cursor "og_hold_new" does not exist`))
				run(fixture.Exec("RESET ROLE", 0))
			})
		})
	}
}
