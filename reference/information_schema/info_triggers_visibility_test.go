package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceInfoTriggersVisibility(t *testing.T) {
	itrScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, r *fixture.Recorder, database string) {
		run := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, c, r, s) }
		itSeed(run)
		for _, q := range []string{
			"CREATE ROLE it_owner", "CREATE ROLE it_reader", "CREATE ROLE it_inherit", "CREATE ROLE it_noinherit NOINHERIT",
			"GRANT it_owner TO it_inherit WITH INHERIT TRUE, SET TRUE", "GRANT it_owner TO it_noinherit",
			"ALTER TABLE it_scope.it_data OWNER TO it_owner", "ALTER TABLE it_scope.it_second OWNER TO it_owner",
			"REVOKE ALL ON SCHEMA it_scope FROM PUBLIC",
			"REVOKE USAGE ON SCHEMA public FROM PUBLIC",
		} {
			run(fixture.Exec(q, 0))
		}
		where := "trigger_schema='it_scope'"
		owner, redacted := itSeedRows(database, true), itSeedRows(database, false)
		for _, rows := range [][][]any{owner, redacted} {
			for _, row := range rows {
				row[9] = "EXECUTE FUNCTION public.it_identity()"
			}
		}
		actor := func(role string, want [][]any) {
			run(fixture.Exec("SET SESSION AUTHORIZATION "+role, 0))
			s := itProjection(b.PG, where, want)
			run(s)
			run(fixture.Prepared(s))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		}
		actor("it_owner", owner)
		actor("it_inherit", owner)
		actor("it_noinherit", nil)
		actor("it_reader", nil)
		run(fixture.Exec("SET SESSION AUTHORIZATION it_noinherit", 0))
		run(fixture.Exec("SET ROLE it_owner", 0))
		run(itProjection(b.PG, where, owner))
		run(fixture.Exec("RESET ROLE", 0))
		run(itProjection(b.PG, where, nil))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// A held query uses the current ACL and role frontier on every execution.
		admin, err := b.DB.Conn(p.Ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer admin.Close()
		adminRun := func(s fixture.Step) { fixture.RunStep(t, p.Ctx, admin, b.Recorder, s) }
		run(fixture.Exec("SET SESSION AUTHORIZATION it_reader", 0))
		held := itHeld(t, p, c, r, itProjection(b.PG, where, nil).Query)
		held(itProjection(b.PG, where, nil))
		for _, privilege := range []string{"SELECT", "MAINTAIN", "INSERT", "UPDATE", "DELETE", "TRUNCATE", "REFERENCES", "TRIGGER"} {
			adminRun(fixture.Exec("GRANT "+privilege+" ON it_scope.it_data,it_scope.it_second TO it_reader", 0))
			want := redacted
			if privilege == "SELECT" || privilege == "MAINTAIN" {
				want = nil
			}
			held(itProjection(b.PG, where, want))
			adminRun(fixture.Exec("REVOKE "+privilege+" ON it_scope.it_data,it_scope.it_second FROM it_reader", 0))
			held(itProjection(b.PG, where, nil))
		}
		for _, privilege := range []string{"SELECT", "INSERT", "UPDATE", "REFERENCES"} {
			adminRun(fixture.Exec("GRANT "+privilege+"(other) ON it_scope.it_data TO it_reader", 0))
			want := redacted[:4]
			if privilege == "SELECT" {
				want = nil
			}
			held(itProjection(b.PG, where, want))
			adminRun(fixture.Exec("REVOKE "+privilege+"(other) ON it_scope.it_data FROM it_reader", 0))
			held(itProjection(b.PG, where, nil))
		}
		adminRun(fixture.Exec("GRANT TRIGGER ON it_scope.it_data,it_scope.it_second TO it_reader", 0))
		held(itProjection(b.PG, where, redacted)) // No target-schema USAGE required.
		adminRun(fixture.Exec("GRANT USAGE ON SCHEMA public TO it_reader", 0))
		held(itProjection(b.PG, where, itSeedRows(database, false)))
		adminRun(fixture.Exec("REVOKE USAGE ON SCHEMA public FROM it_reader", 0))
		held(itProjection(b.PG, where, redacted))
		denied := fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema it_scope")
		denied.Position = "15"
		s := fixture.ErrorDiagnostic("SELECT * FROM it_scope.it_data", denied)
		s.SelectRows = true
		s.ErrorStage = "execute"
		run(s)
		adminRun(fixture.Exec("GRANT it_owner TO it_reader WITH INHERIT TRUE, SET TRUE", 0))
		held(itProjection(b.PG, where, owner))
		adminRun(fixture.Exec("REVOKE it_owner FROM it_reader", 0))
		held(itProjection(b.PG, where, redacted))
		run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		// Real session-local temp namespaces, not hard-coded namespace labels.
		for _, target := range []*sql.Conn{c, admin} {
			for _, q := range []string{"CREATE TEMP TABLE it_temp(v integer)", "CREATE TRIGGER temp_t BEFORE INSERT ON it_temp FOR EACH ROW EXECUTE FUNCTION public.it_identity()"} {
				fixture.RunStep(t, p.Ctx, target, b.Recorder, fixture.Exec(q, 0))
			}
		}
		var own, other string
		if err := c.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&own); err != nil {
			t.Fatal(err)
		}
		if err := admin.QueryRowContext(p.Ctx, "SELECT nspname FROM pg_catalog.pg_namespace WHERE oid=pg_my_temp_schema()").Scan(&other); err != nil {
			t.Fatal(err)
		}
		if own == other {
			t.Fatal("distinct physical sessions share temp namespace")
		}
		run(itProjection(b.PG, "event_object_table='it_temp'", [][]any{itrRow(database, own, "it_temp", "temp_t", "INSERT", 1, nil, "EXECUTE FUNCTION it_identity()")}))
	})
}
