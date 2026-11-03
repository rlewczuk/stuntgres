package reference_test

import (
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferencePgstatginindexControls(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"gg_control_reader"})
	fixture.SchemaReferenceCleanup(t, "gg_moved")
	var emitted string
	pgstatginindexCase(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		pgstatginindexSeed(t, p, b, c)
		for _, q := range []string{
			"CREATE ROLE gg_control_reader", "GRANT pg_stat_scan_tables TO gg_control_reader", "ALTER SCHEMA gg_part OWNER TO gg_control_reader",
			"CREATE VIEW gg_part.gg_full_view AS SELECT * FROM gg_part.pgstatginindex('gg_part.gg_array'::regclass)",
			"CREATE VIEW gg_part.gg_field_view AS SELECT (gg_part.pgstatginindex('gg_part.gg_vector'::regclass)).pending_tuples AS n",
			"ALTER TABLE gg_part.gg_heap OWNER TO gg_control_reader", "ALTER VIEW gg_part.gg_full_view OWNER TO gg_control_reader",
			"CREATE SCHEMA gg_moved AUTHORIZATION gg_control_reader", "ALTER FUNCTION gg_part.pgstatginindex(regclass) RENAME TO gg_stats", "ALTER FUNCTION gg_part.gg_stats(regclass) SET SCHEMA gg_moved", "ALTER INDEX gg_part.gg_array RENAME TO gg_kept",
		} {
			run(fixture.Exec(q, 0))
		}
		defer c.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
		replayed := false
		post := func(target *sql.Conn, rec *fixture.Recorder) {
			step := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, target, rec, s) }
			// The native stream creates indexes before loading its four heap
			// tuples, then replays fastupdate=off. PostgreSQL retains that
			// physical pending list; native keeps its (2,0,0) physical model
			// independently of genuine logical pending postings and cleanup.
			pending := int64(0)
			stats := func(query string) fixture.Step {
				if b.PG && replayed {
					return fixture.Select("SELECT version,pending_pages>0 AS has_pending,pending_tuples FROM ("+query+") s", []string{"version", "has_pending", "pending_tuples"}, []string{"INT4", "BOOL", "INT8"}, [][]any{{int64(2), true, int64(4)}})
				}
				return pgstatginindexStats(query)
			}
			if b.PG && replayed {
				pending = 4
			}
			step(stats("SELECT * FROM gg_part.gg_full_view"))
			step(fixture.Select("SELECT n FROM gg_part.gg_field_view", []string{"n"}, []string{"INT8"}, [][]any{{pending}}))
			step(pgstatginindexIDs(ggArrayQuery, 1, 2))
			step(pgstatginindexIDs(ggVectorQuery, 1))
			for _, rel := range []string{"gg_heap", "gg_full_view"} {
				step(fixture.Select("SELECT pg_get_userbyid(relowner) AS owner FROM pg_class WHERE oid='gg_part."+rel+"'::regclass", []string{"owner"}, []string{"NAME"}, [][]any{{"gg_control_reader"}}))
			}
			for _, schema := range []string{"gg_part", "gg_moved"} {
				step(fixture.Select("SELECT pg_get_userbyid(nspowner) AS owner FROM pg_namespace WHERE nspname=$1", []string{"owner"}, []string{"NAME"}, [][]any{{"gg_control_reader"}}, schema))
			}
			step(fixture.Select("SELECT p.proargtypes::text AS args,p.prosrc AS source,n.nspname::text AS namespace FROM pg_proc p JOIN pg_namespace n ON n.oid=p.pronamespace WHERE n.nspname='gg_moved' AND p.proname='gg_stats'", []string{"args", "source", "namespace"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"2205", "pgstatginindex_v1_5", "gg_moved"}}))
			step(fixture.Select("SELECT c.relname::text AS name,c.reloptions::text AS options FROM pg_class c JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='gg_part' AND c.relname IN ('gg_kept','gg_vector') ORDER BY c.relname", []string{"name", "options"}, []string{"TEXT", "TEXT"}, [][]any{{"gg_kept", "{fastupdate=off}"}, {"gg_vector", "{fastupdate=off}"}}))
			step(fixture.Exec("SET SESSION AUTHORIZATION gg_control_reader", 0))
			step(stats("SELECT * FROM gg_moved.gg_stats('gg_part.gg_kept')"))
			step(stats("SELECT * FROM gg_moved.gg_stats('gg_part.gg_vector')"))
			step(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		post(c, b.Recorder)
		if b.PG {
			if emitted != "" {
				for _, q := range []string{"DROP EXTENSION pgstattuple CASCADE", "DROP SCHEMA gg_part CASCADE", "DROP SCHEMA gg_moved CASCADE"} {
					run(fixture.Exec(q, 0))
				}
				if _, err := c.ExecContext(p.Ctx, emitted); err != nil {
					t.Fatal("PostgreSQL rejected actual native stream", err)
				}
				run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
				replayed = true
				post(c, b.Recorder)
			}
			run(fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(5,'{2}','cat:1 rat:2')", 1))
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(ggArrayQuery, 1, 2, 5))
			pgstatginindexBoth(t, p, b, c, pgstatginindexIDs(ggVectorQuery, 1, 5))
			return
		}
		q := "SELECT * FROM gg_moved.gg_stats($1::regclass)"
		st, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		held := fixture.HeldStatement{Executor: c, Statement: st, SQL: q}
		readHeld := func() {
			for _, arg := range []any{"gg_part.gg_kept", nil, "gg_part.gg_vector"} {
				s := pgstatginindexStats(q, arg)
				if arg == nil {
					s = pgstatginindexNull(q, arg)
				}
				fixture.RunStep(t, p.Ctx, held, b.Recorder, s)
			}
		}
		readHeld()
		literalSQL := "SELECT * FROM gg_moved.gg_stats('gg_part.gg_kept'::regclass)"
		literal, err := c.PrepareContext(p.Ctx, literalSQL)
		if err != nil {
			t.Fatal(err)
		}
		defer literal.Close()
		fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: c, Statement: literal, SQL: literalSQL}, b.Recorder, pgstatginindexStats(literalSQL))
		old, err := c.QueryContext(p.Ctx, "SELECT id FROM gg_part.gg_heap WHERE a @> '{2}'::integer[] ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer old.Close()
		emitted = fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
			t.Fatal(err)
		}
		for _, want := range []int64{1, 2} {
			if !old.Next() {
				t.Fatal("retained row missing", old.Err())
			}
			var id int64
			if err := old.Scan(&id); err != nil || id != want {
				t.Fatal("retained row", id, err)
			}
		}
		if old.Next() || old.Err() != nil {
			t.Fatal("retained tail", old.Err())
		}
		old.Close()
		for range 2 {
			if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted)); err != nil {
				t.Fatal("replacement", err)
			}
			post(c, b.Recorder)
			readHeld()
		}
		// The captured literal is not a parameter that may name-resolve into a
		// new catalog incarnation. Preserve the existing invalidation boundary.
		rows, err := literal.QueryContext(p.Ctx)
		if rows != nil {
			rows.Close()
		}
		if err == nil {
			t.Fatal("persistent regclass literal silently retargeted after replacement")
		}
		before := fixture.ExtensionDump(t, p.Ctx, p.Native)
		if err := stuntgres.Restore(p.Ctx, p.Native, strings.NewReader(emitted+"\nINVALID SQL;")); err == nil {
			t.Fatal("invalid-late stream accepted")
		}
		if fixture.ExtensionDump(t, p.Ctx, p.Native) != before {
			t.Fatal("invalid-late replay published")
		}
		extensionControlsFailures(t, p, c, b.Recorder, "pgstattuple", before, fixture.Select("SELECT n FROM gg_part.gg_field_view", []string{"n"}, []string{"INT8"}, [][]any{{int64(0)}}))
		post(c, b.Recorder)
		other, dest, rec := pgcryptoControlDestination(t, p, "gg_destination")
		for _, q := range []string{"CREATE ROLE gg_control_reader", "GRANT pg_stat_scan_tables TO gg_control_reader", "CREATE TABLE collision(k integer)", "CREATE TEMP TABLE gg_keep(k integer)"} {
			fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO gg_keep VALUES(19)", 1))
		if err := stuntgres.Restore(p.Ctx, other, strings.NewReader(emitted)); err != nil {
			t.Fatal(err)
		}
		post(dest, rec)
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Select("SELECT k FROM gg_keep", []string{"k"}, []string{"INT4"}, [][]any{{int64(19)}}))
		fixture.RunStep(t, p.Ctx, dest, rec, fixture.Exec("INSERT INTO gg_part.gg_heap VALUES(5,'{2}','cat:1 rat:2')", 1))
		fixture.RunStep(t, p.Ctx, dest, rec, pgstatginindexIDs(ggArrayQuery, 1, 2, 5))
		fixture.RunStep(t, p.Ctx, dest, rec, pgstatginindexIDs(ggVectorQuery, 1, 5))
		fixture.RunStep(t, p.Ctx, dest, rec, pgstatginindexStats("SELECT * FROM gg_moved.gg_stats('gg_part.gg_kept')"))
		post(c, b.Recorder)
	})
}
