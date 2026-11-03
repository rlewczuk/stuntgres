package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexIdentity(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "gg_moved")
	fixture.RoleReferenceCleanup(t, []string{"gg_identity_reader"})
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstatginindexSeed(t, p, b, c)
		var oid int64
		if err := c.QueryRowContext(p.Ctx, "SELECT 'gg_part.gg_array'::regclass::oid").Scan(&oid); err != nil {
			t.Fatal(err)
		}
		q := "SELECT * FROM gg_part.pgstatginindex($1::oid)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		for _, q := range []string{
			"CREATE VIEW gg_part.gg_full AS SELECT * FROM gg_part.pgstatginindex('gg_part.gg_array'::regclass)",
			"CREATE VIEW gg_part.gg_field AS SELECT (gg_part.pgstatginindex('gg_part.gg_vector'::regclass)).pending_tuples AS n",
			"CREATE SCHEMA gg_moved", "ALTER INDEX gg_part.gg_array RENAME TO gg_kept",
			"CREATE TABLE gg_part.gg_replacement(id integer,a integer[])",
		} {
			run(fixture.Exec(q, 0))
		}
		run(fixture.Exec("INSERT INTO gg_part.gg_replacement VALUES(9,'{9}')", 1))
		run(fixture.Exec("CREATE INDEX gg_array ON gg_part.gg_replacement USING gin(a) WITH(fastupdate=off)", 0))
		check := func() {
			pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.gg_full"))
			pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT n FROM gg_part.gg_field", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(ggArrayQuery, 1, 2))
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(ggVectorQuery, 1))
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_part.gg_replacement WHERE a @> '{9}'::integer[] ORDER BY id", 9))
			pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT 'gg_part.gg_kept'::regclass::oid=$1::oid AS retained,'gg_part.gg_array'::regclass::oid<>$1::oid AS replaced", []string{"retained", "replaced"}, []string{"BOOL", "BOOL"}, [][]any{{true, true}}, oid))
		}
		fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstatginindexStats(q, oid))
		check()
		// A real conflicting index rename must not redirect a captured regclass.
		run(pgstatindexError("ALTER INDEX gg_part.gg_kept RENAME TO gg_array", "42P07", "relation \"gg_array\" already exists"))
		check()
		for _, q := range []string{"ALTER FUNCTION gg_part.pgstatginindex(regclass) RENAME TO gg_stats", "ALTER FUNCTION gg_part.gg_stats(regclass) SET SCHEMA gg_moved"} {
			run(fixture.Exec(q, 0))
		}
		check()
		pgstatginindexMissing(t, p, b, c, "SELECT * FROM gg_part.pgstatginindex(NULL::regclass)", "gg_part.pgstatginindex(regclass)")
		run(fixture.Exec("CREATE FUNCTION gg_part.pgstatginindex(regclass) RETURNS integer LANGUAGE SQL AS 'SELECT 77'", 0))
		pgstatginindexBoth(t, p, b, c, fixture.Select("SELECT gg_part.pgstatginindex(NULL::regclass) AS n", []string{"n"}, []string{"INT4"}, [][]any{{int64(77)}}))
		check()
		for _, q := range []string{"DROP FUNCTION gg_part.pgstatginindex(regclass)", "ALTER EXTENSION pgstattuple DROP FUNCTION gg_moved.gg_stats(regclass)"} {
			run(fixture.Exec(q, 0))
		}
		pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_moved.gg_stats('gg_part.gg_kept')"))
		for _, q := range []string{"ALTER EXTENSION pgstattuple ADD FUNCTION gg_moved.gg_stats(regclass)", "ALTER FUNCTION gg_moved.gg_stats(regclass) SET SCHEMA gg_part", "ALTER FUNCTION gg_part.gg_stats(regclass) RENAME TO pgstatginindex"} {
			run(fixture.Exec(q, 0))
		}
		// Bind under a genuine ordinary role, then relocate the selected provider.
		// A held call re-resolves its old qualified spelling after invalidation.
		for _, q := range []string{"CREATE ROLE gg_identity_reader", "GRANT USAGE ON SCHEMA gg_part TO gg_identity_reader", "GRANT EXECUTE ON FUNCTION gg_part.pgstatginindex(regclass) TO gg_identity_reader"} {
			run(fixture.Exec(q, 0))
		}
		q2 := "SELECT * FROM gg_part.pgstatginindex($1::regclass)"
		st2, err := c.PrepareContext(p.Ctx, q2)
		if err != nil {
			t.Fatal(err)
		}
		defer st2.Close()
		held2 := fixture.HeldStatement{Executor: c, Statement: st2, SQL: q2}
		run(fixture.Exec("SET SESSION AUTHORIZATION gg_identity_reader", 0))
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		fixture.RunStep(t, p.Ctx, held2, b.Recorder, pgstatginindexStats(q2, "gg_part.gg_kept"))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER EXTENSION pgstattuple SET SCHEMA gg_moved", 0))
		check()
		run(fixture.Exec("SET SESSION AUTHORIZATION gg_identity_reader", 0))
		d := fixture.ControlsReferenceDiagnostic("42883", "function gg_part.pgstatginindex(regclass) does not exist")
		d.Hint = "No function matches the given name and argument types. You might need to add explicit type casts."
		d.Position = "15"
		s := fixture.ImplicitError(q2, d, "gg_part.gg_kept")
		s.SelectRows = true
		fixture.RunStep(t, p.Ctx, held2, b.Recorder, s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		run(fixture.Exec("ALTER TABLE gg_part.gg_heap RENAME TO gg_renamed_heap", 0))
		run(fixture.Exec("ALTER TABLE gg_part.gg_renamed_heap SET SCHEMA gg_moved", 0))
		pgstatginindexBoth(t, p, b, c, pgstatginindexStats("SELECT * FROM gg_part.gg_full"))
		pgstatginindexBoth(t, p, b, c, pgstatginindexIDs("SELECT id FROM gg_moved.gg_renamed_heap WHERE a @> '{2}'::integer[] ORDER BY id", 1, 2))
	})
}
