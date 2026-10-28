package reference_test

import (
	"database/sql"
	"fmt"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceLogicalDecodingAuthorization(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		admin, err := db.Conn(s.P.Ctx)
		if err != nil { s.T.Fatal(err) }
		defer admin.Close()
		alter := func(q string) {
			if _, err := admin.ExecContext(s.P.Ctx, q); err != nil { s.T.Fatal(q, err) }
		}
		s.Exec("ALTER ROLE ld_repl REPLICATION")
		s.Exec("GRANT ld_repl TO ld_member WITH INHERIT TRUE")
		for i, sig := range logicalDecodingSignatures {
			q := "SELECT * FROM " + sig.name + "($1::name,NULL,NULL,VARIADIC $2::text[])"
			held := predefinedHeld(s, q)
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent", "{}")
			s.Exec("SET ROLE ld_actor")
			logicalDecodingHeldFailure(s, held, logicalDecodingDenied(), nil, nil)
			logicalDecodingHeldFailure(s, held, logicalDecodingDenied(), "ld_absent", "{{k,NULL}}")
			logicalDecodingFailure(s, "SELECT * FROM " + sig.name + "(NULL,NULL,NULL,'k')", logicalDecodingDenied())
			alter("ALTER ROLE ld_actor REPLICATION")
			logicalDecodingHeldFailure(s, held, fixture.Diagnostic{Code: "22004", Message: "slot name must not be null"}, nil, nil)
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent", "{}")
			alter("ALTER ROLE ld_actor NOREPLICATION")
			logicalDecodingHeldFailure(s, held, logicalDecodingDenied(), "ld_absent", "{}")
			s.Exec("RESET ROLE")
			s.Exec("SET ROLE ld_member")
			logicalDecodingHeldFailure(s, held, logicalDecodingDenied(), "ld_absent", "{}")
			s.Exec("RESET ROLE")
			s.Exec("SET ROLE ld_repl")
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent", "{}")
			s.Exec("RESET ROLE")
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent", "{}")
			name := fmt.Sprintf("ld_sql_%d", i)
			s.Exec("PREPARE " + name + "(name) AS SELECT * FROM " + sig.name + "($1,NULL,NULL)")
			s.Exec("SET ROLE ld_actor")
			predefinedFailure(s, "EXECUTE " + name + "('ld_absent')", logicalDecodingDenied(), true, "execute")
			s.Exec("RESET ROLE")
			predefinedFailure(s, "EXECUTE " + name + "('ld_absent')", logicalDecodingMissing("ld_absent"), true, "execute")
			s.Exec("DEALLOCATE " + name)
			// Roll back the builtin ACL change even when an assertion aborts.
			// This keeps the package-owned PostgreSQL catalog pristine.
			func() {
				s.Exec("BEGIN")
				defer func() {
					if _, err := s.C.ExecContext(s.P.Ctx, "ROLLBACK"); err != nil {
						s.T.Error(err)
					}
				}()
				s.Exec("REVOKE EXECUTE ON FUNCTION " + sig.name + "(name,pg_lsn,integer,text[]) FROM PUBLIC")
				s.Exec("SET ROLE ld_repl")
				logicalDecodingHeldFailure(s, held, fixture.Diagnostic{Code: "42501", Message: "permission denied for function " + sig.name}, "ld_absent", "{}")
			}()
			logicalDecodingHeldFailure(s, held, logicalDecodingMissing("ld_absent"), "ld_absent", "{}")
		}
	})
}

func TestReferenceLogicalDecodingDynamicAuthorization(t *testing.T) {
	logicalDecodingCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		admin, err := db.Conn(s.P.Ctx)
		if err != nil { s.T.Fatal(err) }
		defer admin.Close()
		alter := func(q string) {
			if _, err := admin.ExecContext(s.P.Ctx, q); err != nil { s.T.Fatal(q, err) }
		}
		s.Exec("ALTER ROLE ld_repl REPLICATION")
		for i, sig := range logicalDecodingSignatures {
			for _, definer := range []bool{false, true} {
				name := fmt.Sprintf("ld_dynamic_%d_%v", i, definer)
				security := " SECURITY INVOKER"
				if definer { security = " SECURITY DEFINER" }
				query := "SELECT * FROM " + sig.name + "(NULL,NULL,NULL,'k')"
				s.Exec("CREATE FUNCTION " + name + "() RETURNS text LANGUAGE plpgsql" + security + " AS $$DECLARE c text; m text; d text; x text; BEGIN EXECUTE " + fileListLiteral(query) + "; RETURN 'unexpected success'; EXCEPTION WHEN OTHERS THEN GET STACKED DIAGNOSTICS c=RETURNED_SQLSTATE,m=MESSAGE_TEXT,d=PG_EXCEPTION_DETAIL,x=PG_EXCEPTION_CONTEXT; RETURN c||'|'||m||'|'||d||'|'||x; END$$")
				if definer { s.Exec("ALTER FUNCTION " + name + "() OWNER TO ld_repl") }
				q := "SELECT " + name + "() AS v"
				held := predefinedHeld(s, q)
				check := func(denied bool) {
					d := fixture.Diagnostic{Code: "22004", Message: "slot name must not be null"}
					if denied { d = logicalDecodingDenied() }
					context := "SQL statement \"" + query + "\"\nPL/pgSQL function " + name + "() line 1 at EXECUTE"
					want := d.Code + "|" + d.Message + "|" + d.Detail + "|" + context
					fixture.RunStep(s.T, s.P.Ctx, held, s.Record, fixture.Select(q, []string{"v"}, []string{"TEXT"}, [][]any{{want}}))
				}
				s.Exec("SET ROLE ld_actor")
				check(!definer)
				alter("ALTER ROLE ld_actor REPLICATION")
				check(false)
				alter("ALTER ROLE ld_actor NOREPLICATION")
				check(!definer)
				if definer {
					alter("ALTER ROLE ld_repl NOREPLICATION")
					check(true)
					alter("ALTER ROLE ld_repl REPLICATION")
					check(false)
				}
				s.Exec("RESET ROLE")
				check(false)
				s.Exec("DROP FUNCTION " + name + "()")
			}
		}
	})
}
