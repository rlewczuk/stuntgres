package reference_test

import (
	"context"
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceTriggerDDLDiagnostics(t *testing.T) {
	cases := []struct{ name, q, code, message string }{
		{"rename-missing", `ALTER TRIGGER absent ON public.tr_data RENAME TO next`, "42704", `trigger "absent" for table "tr_data" does not exist`},
		{"drop-missing", `DROP TRIGGER absent ON public.tr_data`, "42704", `trigger "absent" for table "tr_data" does not exist`},
		{"depends-missing", `ALTER TRIGGER absent ON tr_data DEPENDS ON EXTENSION unaccent`, "42704", `trigger "absent" for table "tr_data" does not exist`},
		{"rename-table", `ALTER TRIGGER a ON no_table RENAME TO next`, "42P01", `relation "no_table" does not exist`},
		{"drop-table", `DROP TRIGGER a ON public.no_table`, "42P01", `relation "public.no_table" does not exist`},
		{"drop-schema", `DROP TRIGGER a ON no_schema.no_table`, "3F000", `schema "no_schema" does not exist`},
		{"add-extension", `ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION no_extension`, "42704", `extension "no_extension" does not exist`},
		{"remove-extension", `ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION no_extension`, "42704", `extension "no_extension" does not exist`},
		{"view-missing", `DROP TRIGGER a ON tr_view`, "42704", `trigger "a" for table "tr_view" does not exist`},
	}
	for _, tc := range cases {
		for _, prepared := range []bool{false, true} {
			route := "direct"
			if prepared {
				route = "prepared"
			}
			t.Run(tc.name+"/"+route, func(t *testing.T) {
				triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
					triggerSetup(t, p, b, c)
					userOperatorExec(t, p, b, c, "CREATE VIEW tr_view AS SELECT id,v FROM tr_data", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
					fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerDDLError(tc.q, tc.code, tc.message, prepared))
					fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
				})
			})
		}
	}
	t.Run("notices", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, _ *sql.Conn) {
			records := new([]fixture.Diagnostic)
			_, c, rec := fixture.NoticeReferencePool(t, p, b.PG, records)
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, rec, s) }
			userOperatorExec(t, p, b, c, "CREATE TABLE tr_data(id integer,v integer)", "CREATE VIEW tr_view AS SELECT id,v FROM tr_data")
			for _, tc := range []struct{ q, message string }{
				{`DROP TRIGGER IF EXISTS "a 名" ON public.tr_data`, `trigger "a 名" for relation "public.tr_data" does not exist, skipping`},
				{`DROP TRIGGER IF EXISTS a ON tr_data`, `trigger "a" for relation "tr_data" does not exist, skipping`},
				{`DROP TRIGGER IF EXISTS a ON tr_view`, `trigger "a" for relation "tr_view" does not exist, skipping`},
				{`DROP TRIGGER IF EXISTS a ON public.no_table`, `relation "public.no_table" does not exist, skipping`},
				{`DROP TRIGGER IF EXISTS a ON no_schema.no_table`, `schema "no_schema" does not exist, skipping`},
			} {
				for _, prepared := range []bool{false, true} {
					*records = nil
					s := fixture.Exec(tc.q, 0)
					if prepared {
						s = fixture.Prepared(s)
					}
					run(s)
					want := fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: tc.message}
					if len(*records) != 1 || (*records)[0] != want {
						t.Errorf("notices for %s: got %#v want %#v", tc.q, *records, want)
					}
				}
			}
			userOperatorExec(t, p, b, c, "CREATE FUNCTION tr_notice() RETURNS trigger LANGUAGE plpgsql AS $$BEGIN RETURN NEW; END$$", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_notice()")
			*records = nil
			run(fixture.Prepared(fixture.Exec("DROP TRIGGER IF EXISTS a ON tr_data", 0)))
			if len(*records) != 0 {
				t.Errorf("successful present DROP notices %#v", *records)
			}
		})
	})
	t.Run("ownership", func(t *testing.T) {
		fixture.RoleReferenceCleanup(t, []string{"tr_ddl_actor", "tr_ddl_owner"})
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			defer c.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE ROLE tr_ddl_actor", "CREATE ROLE tr_ddl_owner", "GRANT USAGE ON SCHEMA public TO tr_ddl_actor,tr_ddl_owner", "GRANT TRIGGER,INSERT,SELECT ON tr_data TO tr_ddl_actor", "GRANT EXECUTE ON FUNCTION tr_double() TO tr_ddl_actor", "CREATE EXTENSION unaccent", "SET SESSION AUTHORIZATION tr_ddl_actor", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()", "RESET SESSION AUTHORIZATION", "ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", "SET SESSION AUTHORIZATION tr_ddl_actor")
			run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, b.Recorder, s) }
			for _, tc := range []struct{ q, message string }{
				{"ALTER TRIGGER a ON tr_data RENAME TO renamed", "must be owner of table tr_data"},
				{"ALTER TRIGGER absent ON tr_data RENAME TO renamed", "must be owner of table tr_data"},
				{"ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION unaccent", "must be owner of relation tr_data"},
				{"ALTER TRIGGER a ON tr_data NO DEPENDS ON EXTENSION unaccent", "must be owner of relation tr_data"},
				{"ALTER TRIGGER a ON tr_data DEPENDS ON EXTENSION missing", "must be owner of relation tr_data"},
				{"DROP TRIGGER a ON tr_data", "must be owner of relation tr_data"},
				{"DROP TRIGGER IF EXISTS a ON tr_data", "must be owner of relation tr_data"},
			} {
				for _, prepared := range []bool{false, true} {
					run(triggerDDLError(tc.q, "42501", tc.message, prepared))
				}
			}
			run(triggerDDLError("DROP TRIGGER absent ON tr_data", "42704", `trigger "absent" for table "tr_data" does not exist`, true))
			run(triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(10)}))
			run(triggerDDLX("tr_data", "unaccent", 1))
			userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "ALTER TABLE tr_data OWNER TO tr_ddl_owner", "SET SESSION AUTHORIZATION tr_ddl_owner")
			run(fixture.Exec("ALTER TRIGGER a ON tr_data RENAME TO renamed", 0))
			run(fixture.Prepared(fixture.Exec("ALTER TRIGGER renamed ON tr_data NO DEPENDS ON EXTENSION unaccent", 0)))
			run(fixture.Exec("ALTER TRIGGER renamed ON tr_data DEPENDS ON EXTENSION unaccent", 0))
			run(fixture.Exec("DROP TRIGGER renamed ON tr_data", 0))
			userOperatorExec(t, p, b, c, "RESET SESSION AUTHORIZATION", "DROP EXTENSION unaccent RESTRICT")
		})
	})
	t.Run("held-utility", func(t *testing.T) {
		triggerDDLScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn) {
			triggerSetup(t, p, b, c)
			userOperatorExec(t, p, b, c, "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_double()")
			q := "ALTER TRIGGER a ON tr_data RENAME TO renamed"
			held := triggerDDLHeld(t, p, b, c, q)
			held(fixture.Exec(q, 0))
			held(triggerDDLError(q, "42704", `trigger "a" for table "tr_data" does not exist`, false))
			userOperatorExec(t, p, b, c, "DROP TRIGGER renamed ON tr_data", "CREATE TRIGGER a BEFORE INSERT ON tr_data FOR EACH ROW EXECUTE FUNCTION tr_add(3)")
			held(fixture.Exec(q, 0))
			fixture.RunStep(t, p.Ctx, c, b.Recorder, triggerRows("INSERT INTO tr_data VALUES(1,5) RETURNING id,v", []any{int64(1), int64(8)}))
		})
	})
}
