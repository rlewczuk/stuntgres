package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferencePGRulesVisibility(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgr_owner", "pgr_reader"})
	fixture.SchemaReferenceCleanup(t, "pgr_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			owner, reader := fixture.LockConnections(t, p.Ctx, b.DB)
			defer owner.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			defer reader.ExecContext(p.Ctx, "RESET SESSION AUTHORIZATION")
			run := func(db fixture.Executor, q string) { fixture.RunStep(t, p.Ctx, db, b.Recorder, fixture.Exec(q, 0)) }
			for _, q := range []string{"CREATE ROLE pgr_owner", "CREATE ROLE pgr_reader", "CREATE SCHEMA pgr_private AUTHORIZATION pgr_owner", "SET SESSION AUTHORIZATION pgr_owner", "CREATE TABLE pgr_private.src(id INT)", "CREATE TABLE pgr_private.log(id INT)", "CREATE RULE r AS ON INSERT TO pgr_private.src DO INSERT INTO pgr_private.log VALUES(NEW.id)", "CREATE VIEW pgr_private.v AS SELECT id FROM pgr_private.src", "CREATE RULE dml AS ON DELETE TO pgr_private.v DO INSTEAD NOTHING", "REVOKE ALL ON SCHEMA pgr_private FROM PUBLIC", "RESET SESSION AUTHORIZATION"} {
				run(owner, q)
			}
			query := "SELECT * FROM pg_catalog.pg_rules WHERE schemaname='pgr_private' ORDER BY tablename,rulename"
			stmt, err := reader.PrepareContext(p.Ctx, query)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: reader, Statement: stmt, SQL: query}
			want := [][]any{
				{"pgr_private", "src", "r", "CREATE RULE r AS\n    ON INSERT TO pgr_private.src DO  INSERT INTO pgr_private.log (id)\n  VALUES (new.id);"},
				{"pgr_private", "v", "dml", "CREATE RULE dml AS\n    ON DELETE TO pgr_private.v DO INSTEAD NOTHING;"},
			}
			check := func(db fixture.Executor) { pgRulesCheck(t, p, b, db, query, want) }
			check(owner)
			run(reader, "SET SESSION AUTHORIZATION pgr_reader")
			run(reader, "SET search_path TO public")
			denied := fixture.ImplicitError("SELECT id FROM pgr_private.src", fixture.Diagnostic{Code: "42501", Message: "permission denied for schema pgr_private", Position: "16"})
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, denied)
			check(reader)
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTableViewSelect(b, query, pgRulesColumns, pgRulesTypes, want))
			// Independently establish the SELECT implementation rule that pg_rules omits.
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT r.rulename,r.ev_type FROM pg_rewrite r JOIN pg_class c ON c.oid=r.ev_class JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='pgr_private' AND c.relname='v' ORDER BY r.rulename", []string{"rulename", "ev_type"}, []string{"NAME", "CHAR"}, [][]any{{"_RETURN", "1"}, {"dml", "4"}}))
			run(owner, "GRANT USAGE ON SCHEMA pgr_private TO pgr_reader")
			denied = fixture.ImplicitError("SELECT id FROM pgr_private.src", fixture.Diagnostic{Code: "42501", Message: "permission denied for table src"})
			denied.SelectRows = true
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, denied)
			for _, prepared := range []bool{false, true} {
				s := fixture.ImplicitError("ALTER RULE r ON pgr_private.src RENAME TO denied", fixture.Diagnostic{Code: "42501", Message: "must be owner of table src"})
				if prepared {
					s = fixture.Prepared(s)
				}
				fixture.RunStep(t, p.Ctx, reader, b.Recorder, s)
				check(reader)
			}
			run(owner, "GRANT SELECT ON pgr_private.src TO pgr_reader")
			fixture.RunStep(t, p.Ctx, reader, b.Recorder, fixture.Select("SELECT id FROM pgr_private.src", []string{"id"}, []string{"INT4"}, nil))
			check(reader)
			run(owner, "REVOKE SELECT ON pgr_private.src FROM pgr_reader")
			run(owner, "REVOKE USAGE ON SCHEMA pgr_private FROM pgr_reader")
			fixture.RunStep(t, p.Ctx, held, b.Recorder, pgTableViewSelect(b, query, pgRulesColumns, pgRulesTypes, want))
			check(reader)
		})
	}
}
