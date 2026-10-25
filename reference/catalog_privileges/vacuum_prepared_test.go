package reference_test

import (
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceVacuumPrepared(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "vacuum_path_a")
	fixture.SchemaReferenceCleanup(t, "vacuum_path_b")
	vacuumScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend) {
		c := vacuumConn(t, p, b)
		run := func(s fixture.Step) { vacuumRun(t, p, b, c, s) }
		q := "VACUUM vacuum_held"
		h := vacuumHeld(t, p, c, q)
		vacuumRun(t, p, b, h, fixture.ImplicitError(q, fixture.Diagnostic{Code: "42P01", Message: `relation "vacuum_held" does not exist`}))
		run(fixture.Exec("CREATE TABLE vacuum_held(id INT)", 0))
		run(fixture.Exec("INSERT INTO vacuum_held VALUES(1),(2)", 2))
		vacuumCount(t, p.Ctx, c, "vacuum_held", 0)
		vacuumRun(t, p, b, h, fixture.Exec(q, 0))
		vacuumCount(t, p.Ctx, c, "vacuum_held", 1)
		for _, sql := range []string{"CREATE SCHEMA vacuum_path_a", "CREATE SCHEMA vacuum_path_b", "CREATE TABLE vacuum_path_a.vacuum_held(id INT)", "CREATE TABLE vacuum_path_b.vacuum_held(id INT)", "SET search_path=vacuum_path_a"} {
			run(fixture.Exec(sql, 0))
		}
		h2 := vacuumHeld(t, p, c, q)
		run(fixture.Exec("SET search_path=vacuum_path_b", 0))
		vacuumRun(t, p, b, h2, fixture.Exec(q, 0))
		vacuumCount(t, p.Ctx, c, "vacuum_path_a.vacuum_held", 0)
		vacuumCount(t, p.Ctx, c, "vacuum_path_b.vacuum_held", 1)
		run(fixture.Exec("SET search_path=vacuum_path_a", 0))
		vacuumRun(t, p, b, h2, fixture.Exec(q, 0))
		vacuumCount(t, p.Ctx, c, "vacuum_path_a.vacuum_held", 1)
		// Omitted selection is database-wide, not restricted to current path.
		run(fixture.Exec("VACUUM", 0))
		vacuumCount(t, p.Ctx, c, "vacuum_path_a.vacuum_held", 2)
		vacuumCount(t, p.Ctx, c, "vacuum_path_b.vacuum_held", 2)
		run(fixture.Exec("SET search_path=public", 0))
		run(fixture.Exec("SET default_transaction_read_only=on", 0))
		vacuumRun(t, p, b, h, fixture.Exec(q, 0))
		run(fixture.Exec("SET default_transaction_read_only=off", 0))
		run(fixture.Exec("BEGIN", 0))
		h3 := vacuumHeld(t, p, c, q)
		vacuumRun(t, p, b, h3, fixture.ImplicitError(q, fixture.Diagnostic{Code: "25001", Message: "VACUUM cannot run inside a transaction block"}))
		run(fixture.Exec("ROLLBACK", 0))
		vacuumRun(t, p, b, h3, fixture.Exec(q, 0))
		vacuumRows(t, p, b, c, "vacuum_held", 1, 2)
		// A utility query has no result descriptor, not a synthetic worker row.
		rows, err := h.Statement.QueryContext(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil {
			t.Fatal(err)
		}
		if len(cols) != 0 || rows.Next() {
			t.Fatalf("VACUUM returned schema/row: %v", cols)
		}
		if err := rows.Err(); err != nil {
			t.Fatal(err)
		}
	})
}
