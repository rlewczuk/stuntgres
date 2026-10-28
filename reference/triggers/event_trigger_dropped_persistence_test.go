package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDroppedObjectsPersistence(t *testing.T) {
	fixture.SchemaReferenceCleanup(t, "et_do_saved")
	droppedScenario(t, func(d droppedCase) {
		d.setup()
		d.exec(
			"CREATE TABLE public.et_do_owner(name text)",
			"CREATE TABLE public.et_do_history(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)",
			"CREATE TABLE public.et_do_keep(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)",
			"CREATE TABLE public.et_do_child(i integer CONSTRAINT et_do_fk REFERENCES public.et_do_keep(f1))",
			"CREATE SCHEMA et_do_saved",
			"CREATE TABLE et_do_saved.t(i integer)",
			"CREATE FUNCTION public.et_do_disabled_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'disabled drop callback ran'; END$$",
		)
		d.run(fixture.Exec("INSERT INTO public.et_do_owner VALUES(current_user)", 1))
		d.run(fixture.Exec("INSERT INTO public.et_do_keep(f1) VALUES(11)", 1))
		d.run(fixture.Exec("INSERT INTO public.et_do_child VALUES(11)", 1))
		d.run(fixture.Exec("INSERT INTO et_do_saved.t VALUES(19)", 1))
		d.absenceCallback()
		d.install("'DROP TABLE','DROP SCHEMA','DROP FUNCTION','DROP PROCEDURE'")
		eventTriggerCreate(t, d.p, d.b, d.c, "et_do_disabled", "sql_drop", "", "public.et_do_disabled_fn")
		d.exec("ALTER EVENT TRIGGER et_do_disabled DISABLE")
		// Persist actual routine recipes separately, so the historical table's
		// complete bags and phase-count assertions remain unchanged.
		d.exec("CREATE TABLE public.et_addr_saved AS SELECT * FROM public.et_do_audit WHERE false")
		var savedRecipes []droppedRecord
		for _, kind := range []string{"function", "procedure"} {
			name := "et_addr_saved_f"
			ddl := "CREATE FUNCTION public.et_addr_saved_f(integer) RETURNS integer LANGUAGE SQL AS $$SELECT $1$$"
			if kind == "procedure" {
				name = "et_addr_saved_p"
				ddl = "CREATE PROCEDURE public.et_addr_saved_p(integer) LANGUAGE SQL AS $$SELECT $1$$"
			}
			d.exec(ddl)
			records := d.capture([]droppedRecord{droppedRoutine("public", name, kind, []string{"integer"})})
			d.drop("DROP "+strings.ToUpper(kind)+" public."+name+"(integer)", false)
			d.bag(1, records)
			d.bag(2, records)
			d.phases(1)
			d.run(fixture.Exec("INSERT INTO public.et_addr_saved SELECT * FROM public.et_do_audit WHERE pass=1 AND original", 1))
			savedRecipes = append(savedRecipes, records[0])
			d.exec(ddl)
			d.clear()
		}
		historic := d.capture(droppedTable("public", "et_do_history", true))
		d.drop("DROP TABLE public.et_do_history", false)
		d.bag(1, historic)
		d.bag(2, historic)
		d.phases(7)
		d.run(fixture.Exec("INSERT INTO public.et_addr_saved SELECT * FROM public.et_do_audit WHERE pass=1 AND original", 1))
		savedRecipes = append(savedRecipes, historic[0])
		d.exec("CREATE TABLE public.et_do_history(f1 integer PRIMARY KEY,f2 integer DEFAULT 42)")
		d.run(fixture.Exec("INSERT INTO public.et_do_history(f1) VALUES(23)", 1))

		check := func(c *sql.Conn, b fixture.Backend) {
			defer c.ExecContext(context.Background(), "ROLLBACK")
			x := droppedCase{t, d.p, b, c}
			x.bag(1, historic)
			x.bag(2, historic)
			x.phases(7)
			droppedReconstructionCheck(x, "public.et_addr_saved", savedRecipes)
			var savedRows [][]any
			for _, r := range savedRecipes {
				savedRows = append(savedRows, r.row(b.PG))
			}
			savedBag := fixture.Select("SELECT "+strings.Join(droppedColumns, ",")+" FROM public.et_addr_saved", droppedColumns, droppedTypes, savedRows)
			savedBag.Bag = true
			x.run(savedBag)
			x.run(fixture.Prepared(savedBag))
			x.run(fixture.Select("SELECT f1,f2 FROM public.et_do_history", []string{"f1", "f2"}, []string{"INT4", "INT4"}, [][]any{{int64(23), int64(42)}}))
			x.run(fixture.Select("SELECT f1,f2 FROM public.et_do_keep", []string{"f1", "f2"}, []string{"INT4", "INT4"}, [][]any{{int64(11), int64(42)}}))
			x.run(fixture.Select("SELECT i FROM public.et_do_child", []string{"i"}, []string{"INT4"}, [][]any{{int64(11)}}))
			x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_trigger WHERE tgconstraint=(SELECT oid FROM pg_constraint WHERE conname='et_do_fk') AND tgisinternal", 4))
			x.run(fixture.Select("SELECT i FROM et_do_saved.t", []string{"i"}, []string{"INT4"}, [][]any{{int64(19)}}))
			x.run(fixture.Select("SELECT evtname,evtevent,evtenabled FROM pg_event_trigger WHERE evtname IN ('et_do_drop','et_do_end','et_do_disabled') ORDER BY evtname", []string{"evtname", "evtevent", "evtenabled"}, []string{"NAME", "NAME", "CHAR"}, [][]any{{"et_do_disabled", "sql_drop", "D"}, {"et_do_drop", "sql_drop", "O"}, {"et_do_end", "ddl_command_end", "O"}}))
			x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid JOIN pg_namespace s ON s.oid=f.pronamespace JOIN pg_roles r ON r.oid=e.evtowner JOIN public.et_do_owner o ON o.name=r.rolname WHERE e.evtname IN ('et_do_drop','et_do_end','et_do_disabled') AND f.proowner=e.evtowner AND s.nspname='public' AND f.proname=CASE e.evtname WHEN 'et_do_drop' THEN 'et_do_drop_fn' WHEN 'et_do_end' THEN 'et_do_end_fn' ELSE 'et_do_disabled_fn' END", 3))
			for _, prepared := range []bool{false, true} {
				outside := fixture.ErrorDiagnostic("SELECT * FROM pg_event_trigger_dropped_objects()", droppedProtocolDiagnostic(""))
				outside.Prepared, outside.SelectRows, outside.ErrorStage = prepared, true, "execute"
				x.run(outside)
				x.exec("BEGIN")
				x.clear()
				// Resolve addresses anew: historic audit OIDs are user data, not
				// lookup authority for the restored live graph.
				keep := x.capture(append(droppedTable("public", "et_do_keep", true), droppedPayload("public", "et_do_child", "et_do_fk", "table constraint", "", false, true)))
				keep = append(keep, x.foreignKeyTriggers()...)
				x.drop("DROP TABLE public.et_do_keep CASCADE", prepared)
				x.bag(1, keep)
				x.bag(2, keep)
				x.phases(12)
				x.run(fixture.Select("SELECT i FROM public.et_do_child", []string{"i"}, []string{"INT4"}, [][]any{{int64(11)}}))
				x.clear()
				schema := x.capture(droppedSchema("et_do_saved", "t"))
				x.drop("DROP SCHEMA et_do_saved CASCADE", prepared)
				x.bag(1, schema)
				x.bag(2, schema)
				x.phases(4)
				x.exec("ROLLBACK")
				x.bag(1, historic)
				x.bag(2, historic)
			}
		}
		check(d.c, d.b)
		if d.b.PG {
			return
		}

		// Native controls are an explicit phase of this same portable scenario.
		// Neither active command records nor callback phase state is serialized.
		var saved []byte
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(d.p.Ctx, d.p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			saved = append([]byte(nil), dump.Bytes()...)
			if err := stuntgres.Reset(d.p.Ctx, d.p.Native); err != nil {
				t.Fatal(err)
			}
			d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname IN ('et_do_drop','et_do_end','et_do_disabled')", 0))
			if err := stuntgres.Restore(d.p.Ctx, d.p.Native, bytes.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			check(d.c, d.b)
			if d.p.Postgres != nil {
				func() {
					pg, err := d.p.Postgres.Conn(d.p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					defer func() {
						eventTriggerCleanup(t, d.p.Postgres, droppedTriggerNames)
						for _, q := range []string{"DROP SCHEMA IF EXISTS et_do_saved CASCADE", "DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
							if _, err := pg.ExecContext(d.p.Ctx, q); err != nil {
								t.Error(err)
							}
						}
					}()
					// Replay exactly the native stream, including saved owners and
					// terminal trigger activation, without rewriting SQL or OIDs.
					if _, err := pg.ExecContext(d.p.Ctx, string(saved)); err != nil {
						t.Fatal("unchanged dropped-record dump replay", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: d.p.PostgresRecord})
				}()
			}
		}
		// Failed staged replacement and interrupted controls must retain both
		// live identities and the unchanged historical user-data recipes.
		err := stuntgres.Restore(d.p.Ctx, d.p.Native, strings.NewReader("CREATE TABLE public.et_addr_staged(i integer); SELECT * FROM pg_get_object_address('function','{public,et_addr_missing}','{integer}');"))
		want := fixture.ControlsReferenceDiagnostic("0A000", "restore accepts only generated sequence or large object state")
		fixture.AssertDiagnostic(d.t, err, &want)
		check(d.c, d.b)
		d.run(eventTriggerCount("SELECT count(*) AS n FROM pg_class WHERE relname='et_addr_staged' AND relnamespace='public'::regnamespace", 0))
		if err := stuntgres.Restore(d.p.Ctx, d.p.Native, io.MultiReader(bytes.NewReader(saved), fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF})); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("saved-recipe Restore reader failure", err)
		}
		check(d.c, d.b)
		canceled, cancel := context.WithCancel(d.p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, d.p.Native, bytes.NewReader(saved)); !errors.Is(err, context.Canceled) {
			t.Fatal("saved-recipe Restore cancellation", err)
		}
		var interrupted bytes.Buffer
		if err := stuntgres.Dump(canceled, d.p.Native, &interrupted); !errors.Is(err, context.Canceled) {
			t.Fatal("saved-recipe Dump cancellation", err)
		}
		check(d.c, d.b)
		connector, err := stuntgres.NewDriver().OpenConnector("event-trigger-dropped-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(connector)
		defer fresh.Close()
		if err := stuntgres.Restore(d.p.Ctx, fresh, bytes.NewReader(saved)); err != nil {
			t.Fatal(err)
		}
		recorded, recorder := fixture.RecordedNativePool(t, connector.(*stuntgres.Connector))
		fc, err := recorded.Conn(d.p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		check(fc, fixture.Backend{Recorder: recorder})
	})
}
