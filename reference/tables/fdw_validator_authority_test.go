package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

// Catalog-selected invocation bypasses only explicit-call EXECUTE admission.
// Body identity, table ACLs and routine configuration must remain effective.
func TestReferenceFDWSQLValidatorAuthority(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		mode := "direct"
		if prepared {
			mode = "prepared"
		}
		t.Run(mode, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"fdw_validator_actor"})
			fdwValidatorScenario(t, func(t *testing.T, p *fixture.Pair, b fixture.Backend, c *sql.Conn, run func(fixture.Step)) {
				execute := func(step fixture.Step) { step.Prepared = prepared; run(step) }
				for _, step := range []fixture.Step{
					fixture.Exec("CREATE ROLE fdw_validator_actor", 0),
					fixture.Exec("GRANT USAGE ON SCHEMA public TO fdw_validator_actor", 0),
					fixture.Exec("CREATE TABLE fdw_validator_audit(actor boolean, owner_identity boolean, setting text)", 0),
					fixture.Exec("GRANT INSERT ON fdw_validator_audit TO fdw_validator_actor", 0),
					fixture.Exec("SET application_name TO 'fdw-outside'", 0),
					fixture.Exec("CREATE FUNCTION public.fdw_accept(text[],oid) RETURNS integer LANGUAGE SQL SET application_name TO 'fdw-inside' AS 'INSERT INTO public.fdw_validator_audit SELECT current_user=''fdw_validator_actor'',current_user=session_user,current_setting(''application_name''); SELECT 1'", 0),
					fixture.Exec("REVOKE ALL ON FUNCTION public.fdw_accept(text[],oid) FROM PUBLIC", 0),
					fixture.Exec("CREATE FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept", 0),
					fixture.Exec("GRANT USAGE ON FOREIGN DATA WRAPPER fdw_v TO fdw_validator_actor", 0),
					fixture.Exec("TRUNCATE fdw_validator_audit", 0),
					fixture.Exec("SET ROLE fdw_validator_actor", 0),
					fixture.Exec("CREATE SERVER fdw_vs FOREIGN DATA WRAPPER fdw_v OPTIONS(seed 'server')", 0),
					fixture.Exec("ALTER SERVER fdw_vs OPTIONS(SET seed 'altered')", 0),
					fixture.Exec("CREATE USER MAPPING FOR CURRENT_USER SERVER fdw_vs OPTIONS(seed 'mapping')", 0),
					fixture.Exec("ALTER USER MAPPING FOR CURRENT_USER SERVER fdw_vs OPTIONS(SET seed 'altered')", 0),
				} {
					execute(step)
				}
				denied := fixture.ErrorDiagnostic("SELECT public.fdw_accept(ARRAY[]::text[],1417::oid)", fdwDiagnostic("42501", "permission denied for function fdw_accept"))
				denied.SelectRows = true
				execute(denied)
				run(fixture.Select("SELECT current_user::text AS actor,current_setting('application_name') AS setting", []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"fdw_validator_actor", "fdw-outside"}}))
				execute(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT actor,owner_identity,setting,count(*) FROM fdw_validator_audit GROUP BY actor,owner_identity,setting", []string{"actor", "owner_identity", "setting", "count"}, []string{"BOOL", "BOOL", "TEXT", "INT8"}, [][]any{{true, false, "fdw-inside", int64(4)}}))

				// Revoked function EXECUTE must not turn into owner impersonation:
				// an invoker body still fails its independent table ACL admission.
				execute(fixture.Exec("REVOKE INSERT ON fdw_validator_audit FROM fdw_validator_actor", 0))
				execute(fixture.Exec("SET ROLE fdw_validator_actor", 0))
				bodyDenied := fdwDiagnostic("42501", "permission denied for table fdw_validator_audit")
				bodyDenied.Context = `SQL function "fdw_accept" statement 1`
				execute(fixture.ErrorDiagnostic("ALTER SERVER fdw_vs OPTIONS(SET seed 'denied')", bodyDenied))
				run(fdwValidatorServerOptions("{seed=altered}"))
				run(fixture.Select("SELECT current_user::text AS actor,current_setting('application_name') AS setting", []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"fdw_validator_actor", "fdw-outside"}}))
				execute(fixture.Exec("RESET ROLE", 0))

				// The same body under SECURITY DEFINER can insert despite that ACL,
				// records a non-actor identity, and restores both identity and SET.
				execute(fixture.Exec("ALTER FUNCTION public.fdw_accept(text[],oid) SECURITY DEFINER", 0))
				execute(fixture.Exec("TRUNCATE fdw_validator_audit", 0))
				execute(fixture.Exec("SET ROLE fdw_validator_actor", 0))
				execute(fixture.Exec("ALTER SERVER fdw_vs OPTIONS(SET seed 'definer')", 0))
				run(fixture.Select("SELECT current_user::text AS actor,current_setting('application_name') AS setting", []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"fdw_validator_actor", "fdw-outside"}}))
				execute(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT actor,owner_identity,setting FROM fdw_validator_audit", []string{"actor", "owner_identity", "setting"}, []string{"BOOL", "BOOL", "TEXT"}, [][]any{{false, true, "fdw-inside"}}))
				execute(fixture.Exec("CREATE FUNCTION public.fdw_guard(text[],oid) RETURNS integer LANGUAGE SQL SECURITY DEFINER SET application_name TO 'fdw-failing' AS 'INSERT INTO public.fdw_validator_audit SELECT current_user=''fdw_validator_actor'',current_user=session_user,current_setting(''application_name''); SELECT 1 / (cardinality($1)-cardinality($1))'", 0))
				execute(fixture.Exec("REVOKE ALL ON FUNCTION public.fdw_guard(text[],oid) FROM PUBLIC", 0))
				execute(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_guard", 0))
				execute(fixture.Exec("SET ROLE fdw_validator_actor", 0))
				bodyFailure := fdwValidatorDivision()
				bodyFailure.Context = `SQL function "fdw_guard" statement 2`
				execute(fixture.ErrorDiagnostic("ALTER SERVER fdw_vs OPTIONS(SET seed 'rejected')", bodyFailure))
				run(fdwValidatorServerOptions("{seed=definer}"))
				execute(fixture.ErrorDiagnostic("CREATE SERVER fdw_null_target FOREIGN DATA WRAPPER fdw_v", bodyFailure))
				run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_null_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Select("SELECT current_user::text AS actor,current_setting('application_name') AS setting", []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"fdw_validator_actor", "fdw-outside"}}))
				execute(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT count(*) FROM fdw_validator_audit", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))

				// A completed body is still private until its caller accepts the
				// result. Rejecting NULL must undo its preceding successful INSERT.
				execute(fixture.Exec("CREATE OR REPLACE FUNCTION public.fdw_guard(text[],oid) RETURNS integer LANGUAGE SQL SECURITY DEFINER SET application_name TO 'fdw-null' AS 'INSERT INTO public.fdw_validator_audit SELECT current_user=''fdw_validator_actor'',current_user=session_user,current_setting(''application_name''); SELECT NULL::integer'", 0))
				var oid int64
				if err := c.QueryRowContext(p.Ctx, "SELECT oid::bigint FROM pg_proc WHERE proname='fdw_guard' AND pronamespace='public'::regnamespace").Scan(&oid); err != nil {
					t.Fatal(err)
				}
				nullResult := fdwDiagnostic("XX000", fmt.Sprintf("function %d returned NULL", oid))
				execute(fixture.Exec("SET ROLE fdw_validator_actor", 0))
				execute(fixture.ErrorDiagnostic("ALTER SERVER fdw_vs OPTIONS(SET seed 'null')", nullResult))
				run(fdwValidatorServerOptions("{seed=definer}"))
				execute(fixture.ErrorDiagnostic("CREATE SERVER fdw_null_target FOREIGN DATA WRAPPER fdw_v", nullResult))
				run(fixture.Select("SELECT count(*) FROM pg_foreign_server WHERE srvname='fdw_null_target'", []string{"count"}, []string{"INT8"}, [][]any{{int64(0)}}))
				run(fixture.Select("SELECT current_user::text AS actor,current_setting('application_name') AS setting", []string{"actor", "setting"}, []string{"TEXT", "TEXT"}, [][]any{{"fdw_validator_actor", "fdw-outside"}}))
				execute(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT count(*) FROM fdw_validator_audit", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))

				// Successful validation and metadata must also share explicit
				// transaction rollback, rather than publishing the audit early.
				execute(fixture.Exec("ALTER FOREIGN DATA WRAPPER fdw_v VALIDATOR public.fdw_accept", 0))
				execute(fixture.Exec("SET ROLE fdw_validator_actor", 0))
				execute(fixture.Exec("BEGIN", 0))
				execute(fixture.Exec("ALTER SERVER fdw_vs OPTIONS(SET seed 'rolled-back')", 0))
				run(fdwValidatorServerOptions("{seed=rolled-back}"))
				execute(fixture.Exec("RESET ROLE", 0))
				run(fixture.Select("SELECT count(*) FROM fdw_validator_audit", []string{"count"}, []string{"INT8"}, [][]any{{int64(2)}}))
				execute(fixture.Exec("ROLLBACK", 0))
				execute(fixture.Exec("RESET ROLE", 0))
				run(fdwValidatorServerOptions("{seed=definer}"))
				run(fixture.Select("SELECT count(*) FROM fdw_validator_audit", []string{"count"}, []string{"INT8"}, [][]any{{int64(1)}}))
			})
		})
	}
}
