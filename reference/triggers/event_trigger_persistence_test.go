package reference_test

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	"stuntgres"
	"stuntgres/reference/fixture"
)

var eventTriggerSavedNames = []string{"et_saved_start", "et_saved_end", "et_saved_disabled", "et_saved_abort", "et_saved_demoted", "et_private"}

func TestReferenceEventTriggerPersistence(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"et_saved_demoted_owner", "et_saved_owner"})
	fixture.SchemaReferenceCleanup(t, "et_saved")
	eventTriggerScenario(t, eventTriggerSavedNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
		for _, q := range []string{
			"CREATE ROLE et_saved_owner SUPERUSER",
			"CREATE SCHEMA et_saved",
			"CREATE TABLE et_saved.identity(owner_name text NOT NULL)",
			"CREATE TABLE et_saved.audit(phase text,event text,tag text)",
			"CREATE TABLE et_saved.data(id integer NOT NULL DEFAULT 7,label text)",
			"COMMENT ON COLUMN et_saved.data.label IS 'replayed after data'",
			"CREATE FUNCTION et_saved.record(phase text,ev text,tag text) RETURNS integer LANGUAGE SQL AS $$INSERT INTO et_saved.audit VALUES(phase,ev,tag); SELECT 1$$",
			"CREATE FUNCTION et_saved.start() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_saved.record('start',TG_EVENT,TG_TAG) INTO n; END$$",
			"CREATE FUNCTION et_saved.finish() RETURNS event_trigger LANGUAGE plpgsql AS $$DECLARE n integer; BEGIN SELECT et_saved.record('end',TG_EVENT,TG_TAG) INTO n; END$$",
			"CREATE FUNCTION et_saved.block() RETURNS event_trigger LANGUAGE plpgsql AS $$BEGIN RAISE EXCEPTION 'restore callback ran'; END$$",
		} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_saved.identity VALUES(current_user)", 1))
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("INSERT INTO et_saved.data(label) VALUES('seed')", 1))
		defer c.ExecContext(context.Background(), "RESET ROLE")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("SET ROLE et_saved_owner", 0))
		eventTriggerCreate(t, p, b, c, "et_saved_start", "ddl_command_start", "'create table','ALTER TABLE'", "et_saved.start")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("RESET ROLE", 0))
		eventTriggerCreate(t, p, b, c, "et_saved_end", "ddl_command_end", "'CREATE TABLE','ALTER TABLE'", "et_saved.finish")
		eventTriggerCreate(t, p, b, c, "et_saved_disabled", "sql_drop", "", "et_saved.start")
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ALTER EVENT TRIGGER et_saved_disabled DISABLE", 0))
		// This last unfiltered end trigger would abort any subsequent restore
		// declaration or metadata replay, proving true final-section activation.
		eventTriggerCreate(t, p, b, c, "et_saved_abort", "ddl_command_end", "", "et_saved.block")
		check := func(conn *sql.Conn, backend fixture.Backend) {
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, conn, backend.Recorder, s) }
			run(fixture.Select("SELECT id,label FROM et_saved.data ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}))
			run(fixture.Select("SELECT phase,event,tag FROM et_saved.audit ORDER BY phase,event,tag", []string{"phase", "event", "tag"}, []string{"TEXT", "TEXT", "TEXT"}, nil))
			run(fixture.Select("SELECT evtname,evtevent,evtenabled,evttags FROM pg_event_trigger WHERE evtname LIKE 'et_saved_%' ORDER BY evtname", []string{"evtname", "evtevent", "evtenabled", "evttags"}, []string{"NAME", "NAME", "CHAR", "_TEXT"}, [][]any{
				{"et_saved_abort", "ddl_command_end", "O", nil}, {"et_saved_disabled", "sql_drop", "D", nil},
				{"et_saved_end", "ddl_command_end", "O", `{"CREATE TABLE","ALTER TABLE"}`}, {"et_saved_start", "ddl_command_start", "O", `{"CREATE TABLE","ALTER TABLE"}`},
			}))
			run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid JOIN pg_namespace s ON s.oid=f.pronamespace JOIN pg_roles r ON r.oid=e.evtowner JOIN pg_roles fr ON fr.oid=f.proowner JOIN et_saved.identity i ON fr.rolname=i.owner_name WHERE e.evtname LIKE 'et_saved_%' AND s.nspname='et_saved' AND r.rolname=CASE WHEN e.evtname='et_saved_start' THEN 'et_saved_owner' ELSE i.owner_name END AND e.oid>0", 4))
			run(fixture.Exec("BEGIN", 0))
			defer conn.ExecContext(context.Background(), "ROLLBACK")
			run(fixture.Exec("ALTER EVENT TRIGGER et_saved_abort DISABLE", 0))
			run(fixture.Prepared(fixture.Exec("CREATE TABLE et_saved.after_restore(id integer)", 0)))
			run(fixture.Select("SELECT phase,event,tag FROM et_saved.audit ORDER BY phase", []string{"phase", "event", "tag"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"end", "ddl_command_end", "CREATE TABLE"}, {"start", "ddl_command_start", "CREATE TABLE"}}))
			run(fixture.Exec("ROLLBACK", 0))
		}
		check(c, b)
		eventTriggerDemotedOwnerDump(t, p, b, c)
		if b.PG {
			return
		}
		if p.Postgres != nil {
			// Roles are intentionally external prerequisites of portable Dump.
			// This native-backend cleanup runs before the later oracle scenario.
			fixture.RoleReferenceCleanup(t, []string{"et_saved_owner"})
			if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE ROLE et_saved_owner SUPERUSER"); err != nil {
				t.Fatal(err)
			}
		}
		retained, err := c.QueryContext(p.Ctx, "SELECT id,label FROM et_saved.data ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer retained.Close()
		var saved []byte
		for range 2 {
			var dump bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &dump); err != nil {
				t.Fatal(err)
			}
			saved = append([]byte(nil), dump.Bytes()...)
			if err := stuntgres.Reset(p.Ctx, p.Native); err != nil {
				t.Fatal(err)
			}
			fixture.RunStep(t, p.Ctx, c, b.Recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname LIKE 'et_saved_%'", 0))
			if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved)); err != nil {
				t.Fatal(err)
			}
			check(c, b)
			if p.Postgres != nil {
				func() {
					pg, err := p.Postgres.Conn(p.Ctx)
					if err != nil {
						t.Fatal(err)
					}
					defer pg.Close()
					defer func() {
						ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
						defer cancel()
						if _, err := pg.ExecContext(ctx, "RESET SESSION AUTHORIZATION"); err != nil {
							t.Error(err)
						}
					}()
					defer eventTriggerCleanup(t, p.Postgres, eventTriggerSavedNames)
					defer func() {
						eventTriggerCleanup(t, p.Postgres, eventTriggerSavedNames)
						if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS et_saved CASCADE"); err != nil {
							t.Error(err)
						}
					}()
					eventTriggerCleanup(t, p.Postgres, eventTriggerSavedNames)
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS et_saved CASCADE"); err != nil {
						t.Fatal(err)
					}
					if _, err := pg.ExecContext(p.Ctx, string(saved)); err != nil {
						t.Fatal("unchanged event dump replay", err)
					}
					check(pg, fixture.Backend{PG: true, Recorder: p.PostgresRecord})
				}()
			}
		}
		// A private state change must not contaminate committed Dump capture.
		for _, q := range []string{"BEGIN", "ALTER EVENT TRIGGER et_saved_start DISABLE", "DROP EVENT TRIGGER et_saved_disabled", "CREATE EVENT TRIGGER et_private ON ddl_command_start EXECUTE FUNCTION et_saved.start()"} {
			fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec(q, 0))
		}
		var committed bytes.Buffer
		if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
			t.Fatal(err)
		}
		fixture.RunStep(t, p.Ctx, c, b.Recorder, fixture.Exec("ROLLBACK", 0))
		freshConnector, err := stuntgres.NewDriver().OpenConnector("event-trigger-fresh/" + t.Name())
		if err != nil {
			t.Fatal(err)
		}
		fresh := sql.OpenDB(freshConnector)
		defer fresh.Close()
		if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE et_saved_owner SUPERUSER"); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(committed.Bytes())); err != nil {
			t.Fatal(err)
		}
		freshRecorded, recorder := fixture.RecordedNativePool(t, freshConnector.(*stuntgres.Connector))
		fc, err := freshRecorded.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer fc.Close()
		check(fc, fixture.Backend{Recorder: recorder})
		fixture.RunStep(t, p.Ctx, fc, recorder, eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname='et_private'", 0))
		for _, reader := range []io.Reader{
			strings.NewReader(string(saved) + "; CREATE EVENT TRIGGER et_broken ON ddl_command_start EXECUTE FUNCTION absent();"),
			io.MultiReader(bytes.NewReader(saved), fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF}),
		} {
			if err := stuntgres.Restore(p.Ctx, p.Native, reader); err == nil {
				t.Fatal("invalid restore accepted")
			}
			check(c, b)
		}
		if err := stuntgres.Restore(p.Ctx, p.Native, io.MultiReader(bytes.NewReader(saved), fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF})); !errors.Is(err, io.ErrUnexpectedEOF) {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(p.Ctx)
		cancel()
		if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved)); !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
		check(c, b)
		fixture.ScalarRows(t, false, retained, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "seed"}}, false)
	})
}

// PostgreSQL permits demoting an existing owner, but unchanged portable replay
// cannot CREATE an event trigger as that non-superuser. Reject before writing,
// rather than changing role capabilities or emitting a falsely portable script.
func eventTriggerDemotedOwnerDump(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
	t.Helper()
	run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
	for _, q := range []string{
		"CREATE ROLE et_saved_demoted_owner SUPERUSER",
		"SET SESSION AUTHORIZATION et_saved_demoted_owner",
		"CREATE EVENT TRIGGER et_saved_demoted ON ddl_command_start WHEN TAG IN ('CREATE TABLE') EXECUTE FUNCTION et_saved.start()",
		"RESET SESSION AUTHORIZATION",
		"ALTER ROLE et_saved_demoted_owner NOSUPERUSER",
	} {
		run(fixture.Exec(q, 0))
	}
	run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_roles r ON r.oid=e.evtowner JOIN pg_proc f ON f.oid=e.evtfoid WHERE e.evtname='et_saved_demoted' AND r.rolname='et_saved_demoted_owner' AND NOT r.rolsuper AND f.proname='start' AND e.evtenabled='O'", 1))
	if !b.PG {
		var output bytes.Buffer
		output.WriteString("untouched")
		if err := stuntgres.Dump(p.Ctx, p.Native, &output); err == nil {
			t.Fatal("dump accepted unrepresentable event-trigger owner")
		}
		if output.String() != "untouched" {
			t.Fatal("failed dump wrote an output prefix", output.String())
		}
		run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_roles r ON r.oid=e.evtowner JOIN pg_proc f ON f.oid=e.evtfoid WHERE e.evtname='et_saved_demoted' AND r.rolname='et_saved_demoted_owner' AND NOT r.rolsuper AND f.proname='start' AND e.evtenabled='O'", 1))
	}
	run(fixture.Exec("DROP EVENT TRIGGER et_saved_demoted", 0))
	run(fixture.Exec("DROP ROLE et_saved_demoted_owner", 0))
}
