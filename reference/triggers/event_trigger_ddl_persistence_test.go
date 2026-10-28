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

var eventDDLSavedNames = []string{"et_ddl_old_o", "et_ddl_old_d", "et_ddl_old_r", "et_ddl_old_a", "et_ddl_saved_o", "et_ddl_saved_d", "et_ddl_saved_r", "et_ddl_saved_a", "et_ddl_private"}

func TestReferenceEventTriggerDDLPersistence(t *testing.T) {
	eventDDLRoutes(t, func(t *testing.T, route eventDDLRoute) {
		fixture.RoleReferenceCleanup(t, []string{"et_ddl_saved_owner"})
		fixture.SchemaReferenceCleanup(t, "et_ddl_saved")
		eventTriggerScenario(t, eventDDLSavedNames, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			d := eventDDLCase{t, p, b, c, route}
			d.exec("CREATE ROLE et_ddl_saved_owner SUPERUSER", "CREATE SCHEMA et_ddl_saved")
			eventTriggerAuditSetup(t, p, b, c)
			d.exec("CREATE TABLE et_ddl_saved.data(id integer,label text)", "CREATE TABLE et_ddl_saved.creator(name text)", "CREATE TABLE et_ddl_saved.drops(kind text,identity text)")
			d.run(fixture.Exec("INSERT INTO et_ddl_saved.data VALUES(7,'persistent')", 1))
			d.run(fixture.Exec("INSERT INTO et_ddl_saved.creator VALUES(current_user)", 1))
			for n, code := range []string{"o", "d", "r", "a"} {
				body := "RETURN;"
				if code == "r" {
					body = "INSERT INTO et_ddl_saved.drops SELECT object_type,object_identity FROM pg_event_trigger_dropped_objects() WHERE object_type='table';"
				}
				eventTriggerFunction(t, p, b, c, "et_ddl_saved.fn_"+code, n+1, body)
			}
			for _, code := range []string{"o", "d", "r", "a"} {
				event, tags := "ddl_command_end", ""
				if code == "o" {
					tags = "'DROP TABLE'"
				}
				if code == "r" {
					event = "sql_drop"
				}
				eventTriggerCreate(t, p, b, c, "et_ddl_old_"+code, event, tags, "et_ddl_saved.fn_"+code)
				d.utility("ALTER EVENT TRIGGER et_ddl_old_" + code + " RENAME TO et_ddl_saved_" + code)
				d.utility("ALTER EVENT TRIGGER et_ddl_saved_" + code + " OWNER TO et_ddl_saved_owner")
				mode := map[string]string{"o": "ENABLE", "d": "DISABLE", "r": "ENABLE REPLICA", "a": "ENABLE ALWAYS"}[code]
				d.utility("ALTER EVENT TRIGGER et_ddl_saved_" + code + " " + mode)
			}
			check := func(x eventDDLCase) {
				x.t.Helper()
				x.run(fixture.Select("SELECT id,label FROM et_ddl_saved.data ORDER BY id", []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "persistent"}}))
				x.audit()
				x.run(fixture.Select("SELECT kind,identity FROM et_ddl_saved.drops ORDER BY kind,identity", []string{"kind", "identity"}, []string{"TEXT", "TEXT"}, nil))
				var identities []eventDDLIdentity
				owner := x.role("et_ddl_saved_owner")
				for _, code := range []string{"a", "d", "o", "r"} {
					i := eventDDLIdentity{name: "et_ddl_saved_" + code, event: "ddl_command_end", mode: strings.ToUpper(code), owner: owner}
					if code == "r" {
						i.event = "sql_drop"
					}
					if code == "o" {
						i.tags = `{"DROP TABLE"}`
					}
					if err := x.c.QueryRowContext(p.Ctx, "SELECT oid FROM pg_event_trigger WHERE evtname=$1", i.name).Scan(&i.oid); err != nil {
						t.Fatal(err)
					}
					if err := x.c.QueryRowContext(p.Ctx, "SELECT f.oid FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname='et_ddl_saved' AND f.proname=$1", "fn_"+code).Scan(&i.function); err != nil {
						t.Fatal(err)
					}
					if i.oid <= 0 || i.function <= 0 {
						t.Fatal("invalid restored identities", i)
					}
					identities = append(identities, i)
					x.dependencies(i, true)
				}
				x.catalog(x.c, identities...)
				x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger e JOIN pg_proc f ON f.oid=e.evtfoid JOIN pg_roles r ON r.oid=f.proowner JOIN et_ddl_saved.creator c ON c.name=r.rolname WHERE e.evtname LIKE 'et_ddl_saved_%'", 4))
				for _, role := range []string{"origin", "local", "replica"} {
					x.exec("BEGIN", "SET LOCAL session_replication_role="+role)
					x.utility("DROP TABLE et_ddl_saved.data")
					if role == "replica" {
						x.audit(eventTriggerRow(3, "et_ddl_saved.fn_r", "sql_drop", "DROP TABLE"), eventTriggerRow(4, "et_ddl_saved.fn_a", "ddl_command_end", "DROP TABLE"))
						x.run(fixture.Select("SELECT kind,identity FROM et_ddl_saved.drops ORDER BY kind,identity", []string{"kind", "identity"}, []string{"TEXT", "TEXT"}, [][]any{{"table", "et_ddl_saved.data"}}))
					} else {
						x.audit(eventTriggerRow(1, "et_ddl_saved.fn_o", "ddl_command_end", "DROP TABLE"), eventTriggerRow(4, "et_ddl_saved.fn_a", "ddl_command_end", "DROP TABLE"))
					}
					x.exec("ROLLBACK")
				}
				x.exec("BEGIN")
				x.failure("DROP FUNCTION et_ddl_saved.fn_o() RESTRICT", eventDDLFunctionDependency("et_ddl_saved.fn_o", "et_ddl_saved_o"), false)
				x.exec("ROLLBACK", "BEGIN")
				x.utility("DROP EVENT TRIGGER et_ddl_saved_o")
				x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_proc f JOIN pg_namespace n ON n.oid=f.pronamespace WHERE n.nspname='et_ddl_saved' AND f.proname='fn_o'", 1))
				x.utility("DROP FUNCTION et_ddl_saved.fn_r() CASCADE")
				x.run(eventTriggerCount("SELECT count(*) AS n FROM pg_event_trigger WHERE evtname IN ('et_ddl_saved_o','et_ddl_saved_r')", 0))
				x.exec("ROLLBACK")
				// Reduce to a single genuine owner edge, avoiding allocator-dependent
				// dependency-detail ordering after remapping/replay.
				x.exec("BEGIN", "ALTER EVENT TRIGGER et_ddl_saved_a OWNER TO CURRENT_USER", "ALTER EVENT TRIGGER et_ddl_saved_d OWNER TO CURRENT_USER", "ALTER EVENT TRIGGER et_ddl_saved_r OWNER TO CURRENT_USER")
				x.failure("DROP ROLE et_ddl_saved_owner", eventDDLRoleDependency("et_ddl_saved_owner", "et_ddl_saved_o"), false)
				x.exec("ROLLBACK")
				x.audit()
			}
			check(d)
			if b.PG {
				return
			}
			retained, err := c.QueryContext(p.Ctx, "SELECT id,label FROM et_ddl_saved.data ORDER BY id")
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
				d.catalog(c)
				if err := stuntgres.Restore(p.Ctx, p.Native, bytes.NewReader(saved)); err != nil {
					t.Fatal(err)
				}
				check(d)
			}
			d.exec("BEGIN", "ALTER EVENT TRIGGER et_ddl_saved_o RENAME TO et_ddl_private", "ALTER EVENT TRIGGER et_ddl_saved_a DISABLE", "ALTER EVENT TRIGGER et_ddl_saved_r OWNER TO CURRENT_USER", "DROP EVENT TRIGGER et_ddl_saved_d")
			var committed bytes.Buffer
			if err := stuntgres.Dump(p.Ctx, p.Native, &committed); err != nil {
				t.Fatal(err)
			}
			d.exec("ROLLBACK")
			connector, err := stuntgres.NewDriver().OpenConnector("event-ddl-fresh/" + t.Name())
			if err != nil {
				t.Fatal(err)
			}
			fresh := sql.OpenDB(connector)
			defer fresh.Close()
			if _, err := fresh.ExecContext(p.Ctx, "CREATE ROLE et_ddl_saved_owner SUPERUSER"); err != nil {
				t.Fatal(err)
			}
			if err := stuntgres.Restore(p.Ctx, fresh, bytes.NewReader(committed.Bytes())); err != nil {
				t.Fatal(err)
			}
			recorded, recorder := fixture.RecordedNativePool(t, connector.(*stuntgres.Connector))
			fc, err := recorded.Conn(p.Ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer fc.Close()
			check(eventDDLCase{t, p, fixture.Backend{Recorder: recorder}, fc, route})
			for _, failure := range []struct {
				reader io.Reader
				eof    bool
			}{
				{strings.NewReader(string(saved) + "; ALTER EVENT TRIGGER et_ddl_missing DISABLE;"), false},
				{io.MultiReader(bytes.NewReader(saved), fixture.CatalogControlFailingReader{Err: io.ErrUnexpectedEOF}), true},
			} {
				err := stuntgres.Restore(p.Ctx, p.Native, failure.reader)
				if failure.eof {
					if !errors.Is(err, io.ErrUnexpectedEOF) {
						t.Fatalf("restore reader failure: %v", err)
					}
				} else {
					want := eventTriggerDiagnostic("42704", `event trigger "et_ddl_missing" does not exist`)
					fixture.AssertDiagnostic(t, err, &want)
				}
				check(d)
				var after bytes.Buffer
				if err := stuntgres.Dump(p.Ctx, p.Native, &after); err != nil {
					t.Fatal(err)
				}
				if !bytes.Equal(after.Bytes(), saved) {
					t.Fatal("failed restore changed deterministic dump")
				}
			}
			ctx, cancel := context.WithCancel(p.Ctx)
			cancel()
			if err := stuntgres.Restore(ctx, p.Native, bytes.NewReader(saved)); !errors.Is(err, context.Canceled) {
				t.Fatal(err)
			}
			check(d)
			fixture.ScalarRows(t, false, retained, []string{"id", "label"}, []string{"INT4", "TEXT"}, [][]any{{int64(7), "persistent"}}, false)
			if p.Postgres != nil {
				fixture.RoleReferenceCleanup(t, []string{"et_ddl_saved_owner"})
				if _, err := p.Postgres.ExecContext(p.Ctx, "CREATE ROLE et_ddl_saved_owner SUPERUSER"); err != nil {
					t.Fatal(err)
				}
				pg, err := p.Postgres.Conn(p.Ctx)
				if err != nil {
					t.Fatal(err)
				}
				defer pg.Close()
				defer pg.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
				defer func() {
					if _, err := pg.ExecContext(p.Ctx, "ROLLBACK"); err != nil {
						t.Error(err)
					}
					if _, err := pg.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
						t.Error(err)
					}
					eventTriggerCleanup(t, p.Postgres, eventDDLSavedNames)
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA IF EXISTS et_ddl_saved CASCADE"); err != nil {
						t.Error(err)
					}
					if _, err := pg.ExecContext(p.Ctx, "DROP SCHEMA public CASCADE; CREATE SCHEMA public"); err != nil {
						t.Error(err)
					}
				}()
				if _, err := pg.ExecContext(p.Ctx, string(saved)); err != nil {
					t.Fatal("unchanged stream replay", err)
				}
				// Replay preserves real ownership directives and may leave the
				// connection authorized as its final object owner. Restore the
				// fixture principal before testing that owner's dependency.
				if _, err := pg.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION"); err != nil {
					t.Fatal(err)
				}
				check(eventDDLCase{t, p, fixture.Backend{PG: true, Recorder: p.PostgresRecord}, pg, route})
			}
		})
	})
}
