package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "ph_moved")
	pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstathashindexSeed(t, p, b, c)
		q := "SELECT count(*) AS n FROM pg_depend d JOIN pg_proc p ON p.oid=d.objid JOIN pg_extension e ON e.oid=d.refobjid WHERE d.classid=1255 AND d.refclassid=3079 AND d.deptype='e' AND e.extname='pgstattuple' AND p.proname='pgstathashindex'"
		pgstathashindexBoth(t, p, b, c, fixture.Select(q, []string{"n"}, []string{"INT8"}, [][]any{{int64(1)}}))
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'ph_part.ph_index'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q = "SELECT live_items,dead_items FROM ph_part.pgstathashindex($1::oid)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstathashindexCounts(q, 4, 0, oid))
		run(fixture.Exec("CREATE VIEW ph_part.ph_bound AS SELECT (ph_part.pgstathashindex('ph_part.ph_expression'::regclass)).live_items AS n", 0))
		run(fixture.Exec("ALTER INDEX ph_part.ph_index RENAME TO ph_kept", 0))
		run(fixture.Exec("CREATE TABLE ph_part.ph_replacement(k integer)", 0))
		run(fixture.Exec("INSERT INTO ph_part.ph_replacement VALUES(9)", 1))
		run(fixture.Exec("CREATE INDEX ph_index ON ph_part.ph_replacement USING hash(k)", 0))
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstathashindexCounts(q, 4, 0, oid))
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index')", 1, 0)
		checkView := func() { pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT n FROM ph_part.ph_bound", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}})) }
		checkView()
		for _, q := range []string{"CREATE SCHEMA ph_moved", "ALTER FUNCTION ph_part.pgstathashindex(regclass) RENAME TO ph_stats", "ALTER FUNCTION ph_part.ph_stats(regclass) SET SCHEMA ph_moved"} {
			run(fixture.Exec(q, 0))
		}
		checkView()
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_moved.ph_stats('ph_part.ph_kept')", 4, 0)
		d := fixture.ControlsReferenceDiagnostic("42883", "function ph_part.pgstathashindex(regclass) does not exist")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		d.Position = "15"
		s := fixture.ImplicitError("SELECT * FROM ph_part.pgstathashindex(NULL::regclass)", d)
		s.SelectRows = true
		run(s)
		run(fixture.Exec("CREATE FUNCTION ph_part.pgstathashindex(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", 0))
		pgstathashindexBoth(t, p, b, c, fixture.Select("SELECT ph_part.pgstathashindex(NULL::regclass) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
		checkView()
		run(fixture.Exec("DROP FUNCTION ph_part.pgstathashindex(regclass)", 0))
		run(fixture.Exec("ALTER EXTENSION pgstattuple DROP FUNCTION ph_moved.ph_stats(regclass)", 0))
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_moved.ph_stats('ph_part.ph_kept')", 4, 0)
		run(fixture.Exec("ALTER EXTENSION pgstattuple ADD FUNCTION ph_moved.ph_stats(regclass)", 0))
		run(fixture.Exec("ALTER FUNCTION ph_moved.ph_stats(regclass) SET SCHEMA ph_part", 0))
		run(fixture.Exec("ALTER FUNCTION ph_part.ph_stats(regclass) RENAME TO pgstathashindex", 0))
		run(fixture.Exec("ALTER EXTENSION pgstattuple SET SCHEMA ph_moved", 0))
		checkView()
		pgstathashindexStats(t, p, b, c, "SELECT * FROM ph_moved.pgstathashindex('ph_part.ph_kept')", 4, 0)
		run(fixture.Exec("ALTER TABLE ph_part.ph_heap RENAME TO ph_renamed_heap", 0))
		run(fixture.Exec("ALTER TABLE ph_part.ph_renamed_heap SET SCHEMA ph_moved", 0))
		checkView()
	})
}
