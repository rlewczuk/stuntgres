package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceEventTriggerDDLCommandsSmoke(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		t := d.t
		d.install("'CREATE TABLE','ALTER TABLE'")
		d.exec("CREATE TABLE et_dc_smoke(i integer)")
		d.run(fixture.Prepared(fixture.Exec("ALTER TABLE et_dc_smoke ADD COLUMN label text", 0)))
		d.run(fixture.Exec("INSERT INTO et_dc_smoke VALUES(7,'ok')", 1))
		check := func() {
			d.rows(append(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_smoke"), ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_smoke")...))
			d.run(fixture.Select("SELECT i,label FROM et_dc_smoke", []string{"i", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "ok"}}))
		}
		check()
		if !d.b.PG {
			var saved bytes.Buffer
			if err := stuntgres.Dump(d.p.Ctx, d.p.Native, &saved); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Reset(d.p.Ctx, d.p.Native); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(d.p.Ctx, d.p.Native, bytes.NewReader(saved.Bytes())); err != nil {
				t.Fatal(err)
			}
			check()
		}
		d.exec("BEGIN")
		d.clear()
		d.run(fixture.Prepared(fixture.Exec("CREATE TABLE et_dc_smoke_fresh(i integer)", 0)))
		d.rows(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_smoke_fresh"))
		d.clear()
		d.run(fixture.Prepared(fixture.Exec("ALTER TABLE et_dc_smoke_fresh ADD COLUMN label text", 0)))
		d.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_smoke_fresh"))
		d.exec("ROLLBACK")
		check()
	})
}

func TestReferenceEventTriggerDDLCommandsPersistence(t *testing.T) {
	ddlCommandScenario(t, func(d ddlCommandCase) {
		t := d.t
		d.install("'CREATE TABLE','ALTER TABLE'")
		d.exec("CREATE FUNCTION et_dc_disabled_fn() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'disabled inspection trigger ran'; END$$")
		eventTriggerCreate(t, d.p, d.b, d.c, "et_dc_disabled", "ddl_command_end", "'CREATE TABLE'", "et_dc_disabled_fn")
		d.exec("ALTER EVENT TRIGGER et_dc_disabled DISABLE")
		d.exec("CREATE TABLE et_dc_persist(i integer)")
		d.run(fixture.Prepared(fixture.Exec("ALTER TABLE et_dc_persist ADD COLUMN label text", 0)))
		d.run(fixture.Exec("INSERT INTO et_dc_persist VALUES(7,'saved')", 1))
		var historical [][]any
		rows, err := d.c.QueryContext(d.p.Ctx, "SELECT pass,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_audit ORDER BY pass,command_tag")
		if err != nil {
			t.Fatal(err)
		}
		for rows.Next() {
			cells := make([]any, 10)
			destinations := make([]any, 10)
			for i := range cells {
				destinations[i] = &cells[i]
			}
			if err := rows.Scan(destinations...); err != nil {
				rows.Close()
				t.Fatal(err)
			}
			types := []string{"INT4", "OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}
			for i := range cells {
				cells[i] = fixture.OidReferenceValue(t, d.b.PG, types[i], cells[i])
			}
			historical = append(historical, cells)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			t.Fatal(err)
		}
		rows.Close()
		if len(historical) != 4 {
			t.Fatalf("historical audit rows=%d, want4", len(historical))
		}
		check := func(c *sql.Conn, b fixture.Backend) {
			x := ddlCommandCase{t, d.p, b, c}
			x.run(fixture.Select("SELECT pass,classid,objid,objsubid,command_tag,object_type,schema_name,object_identity,in_extension,present FROM et_dc_audit ORDER BY pass,command_tag", []string{"pass", "classid", "objid", "objsubid", "command_tag", "object_type", "schema_name", "object_identity", "in_extension", "present"}, []string{"INT4", "OID", "OID", "INT4", "TEXT", "TEXT", "TEXT", "TEXT", "BOOL", "BOOL"}, historical))
			x.run(fixture.Select("SELECT i,label FROM et_dc_persist", []string{"i", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "saved"}}))
			x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc p ON p.oid=e.evtfoid WHERE e.evtname='et_dc_end' AND p.proname='et_dc_end_fn' AND p.proowner=e.evtowner AND e.evtenabled='O'", 1))
			x.run(fixture.Select("SELECT evtname,evtenabled FROM pg_event_trigger WHERE evtname IN ('et_dc_end','et_dc_disabled') ORDER BY evtname", []string{"evtname", "evtenabled"}, []string{"NAME", "CHAR"}, [][]any{{"et_dc_disabled", "D"}, {"et_dc_end", "O"}}))
			x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc p ON p.oid=e.evtfoid WHERE e.evtname='et_dc_disabled' AND p.proname='et_dc_disabled_fn' AND p.proowner=e.evtowner", 1))
			x.exec("BEGIN")
			x.clear()
			x.run(fixture.Prepared(fixture.Exec("CREATE TABLE et_dc_after_restore(i integer)", 0)))
			x.rows(ddlCommandRows("1259", "CREATE TABLE", "table", "public", "public.et_dc_after_restore"))
			x.run(eventTriggerCount("SELECT count(*) AS n FROM et_dc_audit a JOIN pg_class c ON a.objid=c.oid WHERE c.relname='et_dc_after_restore' AND a.classid='pg_class'::regclass", 2))
			x.clear()
			x.run(fixture.Prepared(fixture.Exec("ALTER TABLE et_dc_after_restore ADD COLUMN label text", 0)))
			x.rows(ddlCommandRows("1259", "ALTER TABLE", "table", "public", "public.et_dc_after_restore"))
			x.exec("ROLLBACK")
		}
		check(d.c, d.b)
		if d.b.PG {
			return
		}
		retained, err := d.c.QueryContext(d.p.Ctx, "SELECT i,label FROM et_dc_persist")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
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
						eventTriggerCleanup(t, d.p.Postgres, []string{"et_dc_end", "et_dc_disabled"})
						for _, q := range []string{"DROP SCHEMA public CASCADE", "CREATE SCHEMA public"} {
							if _, err := pg.ExecContext(context.WithoutCancel(d.p.Ctx), q); err != nil {
								t.Error(err)
							}
						}
					}()
					if _, err := pg.ExecContext(d.p.Ctx, string(saved)); err != nil {
						t.Fatal("unchanged command-audit dump replay", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: d.p.PostgresRecord})
				}()
			}
		}
		if err := stuntgres.Restore(d.p.Ctx, d.p.Native, io.MultiReader(bytes.NewReader(saved), fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF})); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal("failed reader replacement", err)
		}
		check(d.c, d.b)
		canceled, cancel := context.WithCancel(d.p.Ctx)
		cancel()
		if err := stuntgres.Restore(canceled, d.p.Native, bytes.NewReader(saved)); !errors.Is(err, context.Canceled) {
			t.Fatal("canceled replacement", err)
		}
		check(d.c, d.b)
		if !retained.Next() {
			t.Fatal("replacement invalidated retained snapshot", retained.Err())
		}
		var kept int64
		var label string
		if err := retained.Scan(&kept, &label); err != nil || kept != 7 || label != "saved" {
			t.Fatal("retained snapshot changed", kept, label, err)
		}
		if retained.Next() || retained.Err() != nil {
			t.Fatal("retained snapshot suffix", retained.Err())
		}
		retained.Close()
		connector, err := stuntgres.NewDriver().OpenConnector("event-trigger-ddl-command-fresh/" + t.Name())
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
