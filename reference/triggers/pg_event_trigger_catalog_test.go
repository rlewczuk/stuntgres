package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGEventTriggerCatalogShape(t *testing.T) {
	eventTriggerScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		empty := func() {
			for _, q := range []string{"SELECT * FROM pg_catalog.pg_event_trigger", "SELECT * FROM pg_event_trigger"} {
				pgEventTriggerCatalogBoth(t, p, b, c, pgEventTriggerCatalogRows(q, nil))
			}
			pgEventTriggerCatalogBoth(t, p, b, c, eventTriggerCount("SELECT count(*) AS n FROM pg_catalog.pg_event_trigger", 0))
		}
		empty()
		pgEventTriggerCatalogBoth(t, p, b, c, fixture.Select("SELECT oid,relname,relnamespace,relkind,relnatts FROM pg_catalog.pg_class WHERE oid=3466", []string{"oid", "relname", "relnamespace", "relkind", "relnatts"}, []string{"OID", "NAME", "OID", "CHAR", "INT2"}, [][]any{{int64(3466), "pg_event_trigger", int64(11), "r", int64(7)}}))
		pgEventTriggerCatalogBoth(t, p, b, c, fixture.Select("SELECT attnum,attname,atttypid::integer AS typ,atttypmod,attnotnull,attndims FROM pg_catalog.pg_attribute WHERE attrelid=3466 AND attnum>0 ORDER BY attnum", []string{"attnum", "attname", "typ", "atttypmod", "attnotnull", "attndims"}, []string{"INT2", "NAME", "INT4", "INT4", "BOOL", "INT2"}, [][]any{
			{int64(1), "oid", int64(26), int64(-1), true, int64(0)},
			{int64(2), "evtname", int64(19), int64(-1), true, int64(0)},
			{int64(3), "evtevent", int64(19), int64(-1), true, int64(0)},
			{int64(4), "evtowner", int64(26), int64(-1), true, int64(0)},
			{int64(5), "evtfoid", int64(26), int64(-1), true, int64(0)},
			{int64(6), "evtenabled", int64(18), int64(-1), true, int64(0)},
			{int64(7), "evttags", int64(1009), int64(-1), false, int64(1)},
		}))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("CREATE FUNCTION pget_catalog_unattached() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'catalog function executed'; END$$", 0))
		empty()
	})
}

func TestReferencePGEventTriggerCatalogDefinitions(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pget_catalog_reader"})
	fixture.SchemaReferenceCleanup(t, "pget_catalog_private")
	eventTriggerScenario(t, []string{"pget_catalog_all", "pget_catalog_filtered"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		// Complete unrelated DDL before attaching the unfiltered raising body.
		for _, q := range []string{
			"CREATE ROLE pget_catalog_reader NOSUPERUSER",
			"CREATE SCHEMA pget_catalog_private",
			"REVOKE ALL ON SCHEMA pget_catalog_private FROM PUBLIC",
			"CREATE FUNCTION public.pget_catalog_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$",
			"CREATE FUNCTION pget_catalog_private.pget_catalog_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'catalog function executed'; END$$",
			"REVOKE EXECUTE ON FUNCTION pget_catalog_private.pget_catalog_fn() FROM PUBLIC",
		} {
			run(fixture.Exec(q, 0))
		}
		var function, decoy, owner int64
		functionQuery := "SELECT f.oid FROM pg_catalog.pg_proc f JOIN pg_catalog.pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname=$1 AND f.proname='pget_catalog_fn' AND f.pronargs=0"
		if err := c.QueryRowContext(p.Ctx, functionQuery, "pget_catalog_private").Scan(&function); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, functionQuery, "public").Scan(&decoy); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_roles WHERE rolname=current_user").Scan(&owner); err != nil {
			t.Fatal(err)
		}
		if function <= 0 || decoy <= 0 || function == decoy || owner <= 0 {
			t.Fatalf("invalid private function/decoy/owner identities: %d/%d/%d", function, decoy, owner)
		}
		eventTriggerCreate(t, p, b, c, "pget_catalog_all", "ddl_command_start", "", "pget_catalog_private.pget_catalog_fn")
		eventTriggerCreate(t, p, b, c, "pget_catalog_filtered", "ddl_command_end", "'create table','aLtEr TaBlE'", "pget_catalog_private.pget_catalog_fn")
		run(fixture.Exec("ALTER EVENT TRIGGER pget_catalog_filtered DISABLE", 0))
		var all, filtered int64
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_event_trigger WHERE evtname='pget_catalog_all'").Scan(&all); err != nil {
			t.Fatal(err)
		}
		if err := c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_catalog.pg_event_trigger WHERE evtname='pget_catalog_filtered'").Scan(&filtered); err != nil {
			t.Fatal(err)
		}
		if all <= 0 || filtered <= 0 || all == filtered {
			t.Fatalf("invalid trigger identities: %d/%d", all, filtered)
		}
		s := pgEventTriggerCatalogRows("SELECT * FROM pg_catalog.pg_event_trigger ORDER BY evtname", [][]any{
			{all, "pget_catalog_all", "ddl_command_start", owner, function, "O", nil},
			{filtered, "pget_catalog_filtered", "ddl_command_end", owner, function, "D", `{"CREATE TABLE","ALTER TABLE"}`},
		})
		pgEventTriggerCatalogBoth(t, p, b, c, s)
		defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
		run(fixture.Exec("SET SESSION AUTHORIZATION pget_catalog_reader", 0))
		pgEventTriggerCatalogBoth(t, p, b, c, s)
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
	})
}

func TestReferencePGEventTriggerCatalogLifecycle(t *testing.T) {
	eventTriggerScenario(t, []string{"pget_catalog_life"}, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
		run(fixture.Exec("CREATE FUNCTION pget_catalog_life_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN END$$", 0))
		const q = "SELECT evtname,evtevent,evtenabled,evttags FROM pg_catalog.pg_event_trigger WHERE evtname='pget_catalog_life'"
		stmt, err := c.PrepareContext(p.Ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		defer stmt.Close()
		held := fixture.HeldStatement{Executor: c, Statement: stmt, SQL: q}
		peer, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer peer.Close()
		check := func(db fixture.Executor, state string) {
			var rows [][]any
			if state != "" {
				rows = [][]any{{"pget_catalog_life", "ddl_command_start", state, `{"CREATE TABLE"}`}}
			}
			s := fixture.Select(q, []string{"evtname", "evtevent", "evtenabled", "evttags"}, []string{"NAME", "NAME", "CHAR", "_TEXT"}, rows)
			fixture.RunStep(t, p.Ctx, db, b.Recorder, s)
			if db == peer {
				fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Prepared(s))
			}
		}
		check(held, "")
		run(fixture.Exec("BEGIN", 0))
		// Registered immediately on the pinned transaction owner; unwind before
		// closing the retained statement or returning the connection.
		defer c.ExecContext(context.Background(), "ROLLBACK")
		eventTriggerCreate(t, p, b, c, "pget_catalog_life", "ddl_command_start", "'CREATE TABLE'", "pget_catalog_life_fn")
		check(held, "O")
		check(peer, "")
		run(fixture.Exec("COMMIT", 0))
		check(held, "O")
		check(peer, "O")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("ALTER EVENT TRIGGER pget_catalog_life DISABLE", 0))
		check(held, "D")
		run(fixture.Exec("ROLLBACK", 0))
		check(held, "O")
		run(fixture.Exec("BEGIN", 0))
		run(fixture.Exec("DROP EVENT TRIGGER pget_catalog_life", 0))
		check(held, "")
		run(fixture.Exec("COMMIT", 0))
		check(held, "")
		check(peer, "")
		pgEventTriggerCatalogBoth(t, p, b, c, eventTriggerCount("SELECT count(*) AS n FROM pg_catalog.pg_event_trigger", 0))
	})
}

func TestReferencePGEventTriggerCatalogDiagnostics(t *testing.T) {
	eventTriggerScenario(t, nil, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		pgEventTriggerCatalogBoth(t, p, b, c, eventTriggerCount("SELECT count(*) AS n FROM pg_catalog.pg_event_trigger", 0))
		d := eventTriggerDiagnostic("42703", `column "missing" does not exist`)
		d.Position = "8"
		for _, prepared := range []bool{false, true} {
			s := fixture.ErrorDiagnostic("SELECT missing FROM pg_catalog.pg_event_trigger WHERE FALSE", d)
			s.SelectRows, s.Prepared, s.ErrorStage = true, prepared, "execute"
			if prepared {
				s.ErrorStage = "prepare"
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, s)
		}
		pgEventTriggerCatalogBoth(t, p, b, c, eventTriggerCount("SELECT count(*) AS n FROM pg_catalog.pg_event_trigger", 0))
	})
}

func pgEventTriggerCatalogRows(query string, rows [][]any) fixture.Step {
	return fixture.Select(query, []string{"oid", "evtname", "evtevent", "evtowner", "evtfoid", "evtenabled", "evttags"}, []string{"OID", "NAME", "NAME", "OID", "OID", "CHAR", "_TEXT"}, rows)
}

func pgEventTriggerCatalogBoth(t *testing.T, p *fixture.Pair, b fixture.Backend, db fixture.Executor, s fixture.Step) {
	t.Helper()
	fixture.PgDescriptionExecutorRows(t, p, b, db, s)
	fixture.PgDescriptionExecutorRows(t, p, b, db, fixture.Prepared(s))
}
