package reference_test

import (
	"context"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceMaterializedRefreshAuthorization(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"refresh_owner", "refresh_caller", "refresh_base_owner", "refresh_member"})
	fixture.SchemaReferenceCleanup(t, "refresh_private")
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			for _, name := range []string{"refresh_owner", "refresh_caller", "refresh_base_owner", "refresh_member"} {
				run(fixture.Exec("CREATE USER "+name, 0))
			}
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_base_owner", 0))
			run(fixture.Exec("CREATE TABLE refresh_acl_base(i INT)", 0))
			run(fixture.Exec("INSERT INTO refresh_acl_base VALUES(1)", 1))
			run(fixture.Exec("GRANT SELECT ON refresh_acl_base TO refresh_owner", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_owner", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_acl AS SELECT i FROM refresh_acl_base", 1))
			run(fixture.Exec("GRANT SELECT ON refresh_acl TO refresh_caller", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_caller", 0))
			q := "REFRESH MATERIALIZED VIEW refresh_acl"
			held, err := conn.PrepareContext(p.Ctx, q)
			if err != nil {
				t.Fatal("Prepare must not require MAINTAIN", err)
			}
			defer held.Close()
			denied := fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for materialized view refresh_acl"))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, denied)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE SELECT ON refresh_acl FROM refresh_caller", 0))
			run(fixture.Exec("GRANT MAINTAIN ON refresh_acl TO refresh_caller", 0))
			run(fixture.Exec("INSERT INTO refresh_acl_base VALUES(2)", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_caller", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, fixture.Exec(q, 0))
			s := fixture.ErrorDiagnostic("SELECT * FROM refresh_acl", fixture.ControlsReferenceDiagnostic("42501", "permission denied for materialized view refresh_acl"))
			s.SelectRows = true
			run(s)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i FROM refresh_acl ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Exec("REVOKE SELECT ON refresh_acl_base FROM refresh_owner", 0))
			run(fixture.Exec("GRANT SELECT ON refresh_acl_base TO refresh_caller", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_caller", 0))
			run(fixture.ErrorDiagnostic(q, fixture.ControlsReferenceDiagnostic("42501", "permission denied for table refresh_acl_base")))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i FROM refresh_acl ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_caller", 0))
			run(fixture.Exec(q+" WITH NO DATA", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.MaterializedViewUnpopulated("refresh_acl"))
			run(fixture.Exec("GRANT SELECT ON refresh_acl_base TO refresh_owner", 0))
			run(fixture.Exec("REVOKE MAINTAIN ON refresh_acl FROM refresh_caller", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_caller", 0))
			fixture.RunStep(t, p.Ctx, fixture.HeldStatement{Executor: conn, Statement: held, SQL: q}, b.Recorder, denied)
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("GRANT MAINTAIN ON refresh_acl TO PUBLIC", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_member", 0))
			run(fixture.Exec(q, 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("REVOKE MAINTAIN ON refresh_acl FROM PUBLIC", 0))
			run(fixture.Exec("GRANT MAINTAIN ON refresh_acl TO refresh_caller", 0))
			run(fixture.Exec("GRANT refresh_caller TO refresh_member", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_member", 0))
			run(fixture.Prepared(fixture.Exec(q, 0)))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT i FROM refresh_acl ORDER BY i", []string{"i"}, []string{"INT4"}, [][]any{{int64(1)}, {int64(2)}}))
			// An ordinary table denied MAINTAIN fails authorization before wrong-kind.
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_owner", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_acl_base", fixture.ControlsReferenceDiagnostic("42501", "permission denied for table refresh_acl_base")))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("CREATE SCHEMA refresh_private AUTHORIZATION refresh_owner", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_private.refresh_other AS SELECT 7 AS i", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_owner", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_private.refresh_other", fixture.ControlsReferenceDiagnostic("42501", "permission denied for materialized view refresh_other")))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_caller", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_private.refresh_other", fixture.ControlsReferenceDiagnostic("42501", "permission denied for schema refresh_private")))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
		})
	}
}

func TestReferenceMaterializedRefreshOwnerContext(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"refresh_expr_owner", "refresh_expr_caller"})
	p := fixture.NewScenario(t)
	for _, b := range p.Backends() {
		t.Run(b.Name, func(t *testing.T) {
			conn, run := fixture.QueryTableCreateSession(t, p, b)
			defer conn.ExecContext(context.Background(), "RESET SESSION AUTHORIZATION")
			run(fixture.Exec("CREATE USER refresh_expr_owner", 0))
			run(fixture.Exec("CREATE USER refresh_expr_caller", 0))
			if b.PG {
				run(fixture.Exec("GRANT USAGE,CREATE ON SCHEMA public TO PUBLIC", 0))
			}
			run(fixture.Exec("SET search_path=public", 0))
			run(fixture.Exec("CREATE SEQUENCE refresh_seq", 0))
			run(fixture.Exec("ALTER SEQUENCE refresh_seq OWNER TO refresh_expr_owner", 0))
			run(fixture.Exec("CREATE TABLE refresh_expr_base(i INT)", 0))
			run(fixture.Exec("INSERT INTO refresh_expr_base VALUES(1)", 1))
			run(fixture.Exec("GRANT SELECT ON refresh_expr_base TO refresh_expr_owner", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_expr_owner", 0))
			run(fixture.Exec("CREATE VIEW refresh_invoker WITH(security_invoker=true) AS SELECT i FROM refresh_expr_base", 0))
			run(fixture.Exec("CREATE MATERIALIZED VIEW refresh_identity AS SELECT CURRENT_USER AS who,SESSION_USER AS session_who,CURRENT_SCHEMA AS ns,nextval('refresh_seq'::regclass) AS n,1/i AS x FROM refresh_invoker WITH NO DATA", 0))
			run(fixture.Exec("GRANT MAINTAIN ON refresh_identity TO refresh_expr_caller", 0))
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_expr_caller", 0))
			checkCaller := func() {
				run(fixture.Select("SELECT CURRENT_USER AS who,SESSION_USER AS session_who,CURRENT_SCHEMA AS ns", []string{"who", "session_who", "ns"}, []string{"NAME", "NAME", "NAME"}, [][]any{{"refresh_expr_caller", "refresh_expr_caller", "public"}}))
				run(fixture.Select("SHOW search_path", []string{"search_path"}, []string{"TEXT"}, [][]any{{"public"}}))
			}
			run(fixture.Prepared(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_identity", 0)))
			checkCaller()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT * FROM refresh_identity", []string{"who", "session_who", "ns", "n", "x"}, []string{"NAME", "NAME", "NAME", "INT8", "INT4"}, [][]any{{"refresh_expr_owner", "refresh_expr_caller", "pg_catalog", int64(1), int64(1)}}))
			run(fixture.Exec("ALTER SEQUENCE refresh_seq OWNER TO refresh_expr_caller", 0))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_expr_caller", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_identity", fixture.ControlsReferenceDiagnostic("42501", "permission denied for sequence refresh_seq")))
			checkCaller()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT * FROM refresh_identity", []string{"who", "session_who", "ns", "n", "x"}, []string{"NAME", "NAME", "NAME", "INT8", "INT4"}, [][]any{{"refresh_expr_owner", "refresh_expr_caller", "pg_catalog", int64(1), int64(1)}}))
			fixture.MaterializedViewPopulation(run, "refresh_identity", true)
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_expr_caller", 0))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_identity WITH NO DATA", 0))
			checkCaller()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.Select("SELECT last_value,is_called FROM refresh_seq", []string{"last_value", "is_called"}, []string{"INT8", "BOOL"}, [][]any{{int64(1), true}}))
			run(fixture.MaterializedViewUnpopulated("refresh_identity"))
			run(fixture.Exec("ALTER SEQUENCE refresh_seq OWNER TO refresh_expr_owner", 0))
			run(fixture.Exec("UPDATE refresh_expr_base SET i=0", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_expr_caller", 0))
			run(fixture.ErrorDiagnostic("REFRESH MATERIALIZED VIEW refresh_identity", fixture.ControlsReferenceDiagnostic("22012", "division by zero")))
			checkCaller()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			run(fixture.MaterializedViewUnpopulated("refresh_identity"))
			run(fixture.Exec("UPDATE refresh_expr_base SET i=1", 1))
			run(fixture.Exec("SET SESSION AUTHORIZATION refresh_expr_caller", 0))
			run(fixture.Exec("REFRESH MATERIALIZED VIEW refresh_identity", 0))
			checkCaller()
			run(fixture.Exec("RESET SESSION AUTHORIZATION", 0))
			fixture.MaterializedViewPopulation(run, "refresh_identity", true)
			run(fixture.Select("SELECT * FROM refresh_identity", []string{"who", "session_who", "ns", "n", "x"}, []string{"NAME", "NAME", "NAME", "INT8", "INT4"}, [][]any{{"refresh_expr_owner", "refresh_expr_caller", "pg_catalog", int64(3), int64(1)}}))
		})
	}
}
