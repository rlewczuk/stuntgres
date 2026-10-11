package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferencePGSequencesPrivileges(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"pgsv_actor", "pgsv_writer", "pgsv_owner"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			a, admin := fixture.LockConnections(t, p.Ctx, b.DB)
			defer a.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, q := range []string{"CREATE ROLE pgsv_actor", "CREATE ROLE pgsv_writer", "CREATE ROLE pgsv_owner", "CREATE SCHEMA pgsv_private AUTHORIZATION pgsv_owner", "CREATE SEQUENCE pgsv_private.pgsv_priv", "ALTER SEQUENCE pgsv_private.pgsv_priv OWNER TO pgsv_owner"} {
				pgSequenceExec(t, p, b, admin, q)
			}
			defer admin.ExecContext(context.Background(), "DROP SCHEMA pgsv_private CASCADE")
			pgSequencesScalar(t, p, b, admin, "SELECT setval('pgsv_private.pgsv_priv',42,true)", "setval", 42)
			row := pgSequencesDefault("pgsv_owner", "pgsv_priv", nil)
			row[0] = "pgsv_private"
			pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION pgsv_actor")
			pgSequencesCheck(t, p, b, a, "pgsv_priv", row)
			stmt, err := a.PrepareContext(p.Ctx, pgSequencesProbe)
			if err != nil {
				t.Fatal(err)
			}
			defer stmt.Close()
			held := fixture.HeldStatement{Executor: a, Statement: stmt, SQL: pgSequencesProbe}
			check := func(last any) {
				row[10] = last
				fixture.PgDescriptionExecutorRows(t, p, b, held, pgSequencesSelect(b, pgSequencesProbe, [][]any{row}, "pgsv_priv"))
			}
			pgSequenceExec(t, p, b, admin, "GRANT pg_read_all_data TO pgsv_actor")
			check(int64(42))
			for _, q := range []string{"SELECT nextval('pgsv_private.pgsv_priv')", "SELECT setval('pgsv_private.pgsv_priv',99,true)"} {
				s := fixture.ImplicitError(q, fixture.Diagnostic{Code: "42501", Message: "permission denied for sequence pgsv_priv"})
				s.SelectRows = true
				fixture.RunStep(t, p.Ctx, a, b.Recorder, s)
			}
			pgSequenceExec(t, p, b, admin, "REVOKE pg_read_all_data FROM pgsv_actor")
			check(nil)
			pgSequenceExec(t, p, b, admin, "GRANT pg_read_all_data TO pgsv_actor WITH INHERIT FALSE, SET TRUE")
			check(nil)
			pgSequenceExec(t, p, b, a, "SET ROLE pg_read_all_data")
			check(int64(42))
			pgSequenceExec(t, p, b, a, "RESET ROLE")
			pgSequenceExec(t, p, b, admin, "GRANT pg_read_all_data TO pgsv_actor WITH INHERIT TRUE, SET FALSE")
			check(int64(42))
			pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
			pgSequenceExec(t, p, b, admin, "GRANT pg_write_all_data TO pgsv_writer")
			pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION pgsv_writer")
			pgSequencesScalar(t, p, b, a, "SELECT nextval('pgsv_private.pgsv_priv')", "nextval", 43)
			pgSequencesScalar(t, p, b, a, "SELECT setval('pgsv_private.pgsv_priv',50,true)", "setval", 50)
			row[10] = nil
			pgSequencesCheck(t, p, b, a, "pgsv_priv", row)
			pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
			row[10] = int64(50)
			pgSequencesCheck(t, p, b, a, "pgsv_priv", row)
			pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION pgsv_owner")
			pgSequencesCheck(t, p, b, a, "pgsv_priv", row)
			pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
			if b.PG {
				t.Run("UsageOnlyOracle", func(t *testing.T) {
					pgSequenceExec(t, p, b, admin, "GRANT USAGE ON SCHEMA pgsv_private TO pgsv_writer")
					pgSequenceExec(t, p, b, admin, "REVOKE pg_write_all_data FROM pgsv_writer")
					pgSequenceExec(t, p, b, admin, "GRANT USAGE ON SEQUENCE pgsv_private.pgsv_priv TO pgsv_writer")
					pgSequenceExec(t, p, b, a, "SET SESSION AUTHORIZATION pgsv_writer")
					pgSequencesCheck(t, p, b, a, "pgsv_priv", row)
					pgSequenceExec(t, p, b, a, "RESET SESSION AUTHORIZATION")
				})
			}
		})
	}
}
