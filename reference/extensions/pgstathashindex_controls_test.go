package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstathashindexControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ph_control_reader"})
	fixture.SchemaReferenceCleanup(t, "ph_moved")
	var emitted string
	pgstathashindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstathashindexSeed(t, p, b, c)
		// The emitted SQL recreates table/view definitions as their real owner.
		// Own both namespaces so that role has CREATE and callee USAGE during
		// replay, before final ACL metadata; scan-role EXECUTE remains separate.
		for _, q := range []string{
			"CREATE ROLE ph_control_reader", "GRANT pg_stat_scan_tables TO ph_control_reader", "ALTER SCHEMA ph_part OWNER TO ph_control_reader",
			"CREATE VIEW ph_part.ph_full_view AS SELECT * FROM ph_part.pgstathashindex('ph_part.ph_index'::regclass)",
			"CREATE VIEW ph_part.ph_expression_view AS SELECT (ph_part.pgstathashindex('ph_part.ph_expression'::regclass)).live_items AS n",
			"ALTER TABLE ph_part.ph_heap OWNER TO ph_control_reader", "ALTER VIEW ph_part.ph_full_view OWNER TO ph_control_reader",
			"CREATE SCHEMA ph_moved AUTHORIZATION ph_control_reader",
			"ALTER FUNCTION ph_part.pgstathashindex(regclass) RENAME TO ph_stats", "ALTER FUNCTION ph_part.ph_stats(regclass) SET SCHEMA ph_moved",
			"ALTER INDEX ph_part.ph_index RENAME TO ph_kept",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT live_items,dead_items FROM ph_part.ph_full_view", []string{"live_items", "dead_items"}, []string{"INT8", "INT8"}, [][]any{{int64(4), int64(0)}}))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT n FROM ph_part.ph_expression_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
			for _, rel := range []string{"ph_heap", "ph_full_view"} {
				fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='ph_part."+rel+"'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"ph_control_reader"}}))
			}
			for _, schema := range []string{"ph_part", "ph_moved"} {
				fixture.RunStep(t, p.Ctx, target, rec, fixture.Select("SELECT pg_get_userbyid(nspowner) AS owner FROM pg_namespace WHERE nspname=$1", []string{"owner"}, []string{"NAME"}, [][]any{{"ph_control_reader"}}, schema))
			}
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("SET SESSION AUTHORIZATION ph_control_reader", 0))
			fixture.RunStep(t, p.Ctx, target, rec, pgstathashindexCounts("SELECT live_items,dead_items FROM ph_moved.ph_stats('ph_part.ph_expression')", 5, 0))
			fixture.RunStep(t, p.Ctx, target, rec, fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		post(c, b.Recorder)
		if b.PG {
			// Replay the actual stream emitted by the preceding native backend.
			if emitted != "" {
				for _, q := range []string{"DROP EXTENSION pgstattuple CASCADE", "DROP SCHEMA ph_part CASCADE", "DROP SCHEMA ph_moved CASCADE"} {
					run(fixture.Exec(q, 0))
				}
				if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
					t.Fatal("PostgreSQL rejected actual native stream", err)
				}
				// SQL replay is not native owner-manifest replay; portable SQL
				// still must rebuild the bound stored call and hash memberships.
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				post(c, b.Recorder)
			}
			return
		}
		q := "SELECT live_items,dead_items FROM ph_moved.ph_stats($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		readHeld := func() { fixture.RunStep(t, p.Ctx, held, b.Recorder, pgstathashindexCounts(q, 4, 0, "ph_part.ph_kept")) }
		readHeld()
		old, err := c.QueryContext(p.Ctx, "SELECT n FROM ph_part.ph_expression_view")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		emitted = fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		if !old.Next() {
			t.Fatal("retained row missing", old.Err())
		}
		var n int64
		if err := old.Scan(&n); err != nil || n != 5 {
			t.Fatal("retained row", n, err)
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained tail", old.Err())
		}
		old.Close()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		post(c, b.Recorder)
		readHeld()
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
			t.Fatal("self replacement", err)
		}
		post(c, b.Recorder)
		readHeld()
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid-late stream accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("invalid-late replay published")
		}
		extensionControlsFailures(t, p, c, b.Recorder, "pgstattuple", before, fixture.Select("SELECT n FROM ph_part.ph_expression_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(5)}}))
		post(c, b.Recorder)
		other, dest, rec := pgcryptoControlDestination(t, p, "ph_destination")
		for _, q := range []string{"CREATE ROLE ph_control_reader", "GRANT pg_stat_scan_tables TO ph_control_reader", "CREATE TABLE collision(k integer)", "CREATE TEMP TABLE ph_keep(k integer)"} {
			fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO ph_keep VALUES(19)", 1))
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		post(dest, rec)
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k FROM ph_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(19)}}))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO ph_part.ph_heap VALUES(8)", 1))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT n FROM ph_part.ph_expression_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(6)}}))
		post(c, b.Recorder)
	})
}
