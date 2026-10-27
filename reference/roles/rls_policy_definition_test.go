package reference_test

import (
	"database/sql"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"stuntgres/reference/fixture"
)

// Expectations come from PostgreSQL REL_18_0 policy.c and gram.y, not native
// output. Fixture failures print the complete diagnostic for oracle discovery.
func policyError(s *fixture.RoleMembershipScenario, q, code, message, detail string, position int) {
	s.T.Helper()
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail, Position: fixture.Position(position)})
	step.ErrorStage = "execute"
	s.Run(step)
}

func policySetup(s *fixture.RoleMembershipScenario) {
	s.Exec("CREATE TABLE t(id INTEGER, allowed BOOLEAN)")
	s.Exec("CREATE TABLE u(id INTEGER, allowed BOOLEAN)")
	s.Run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE)", 2))
}

func policyRows(s *fixture.RoleMembershipScenario) {
	s.T.Helper()
	s.Run(fixture.Select("SELECT id,allowed FROM t ORDER BY id", []string{"id", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
}

func policyNotice(s *fixture.RoleMembershipScenario, want fixture.Diagnostic) {
	s.T.Helper()
	if !reflect.DeepEqual(*s.Notices, []fixture.Diagnostic{want}) {
		s.T.Fatalf("notices = %#v; want %#v", *s.Notices, want)
	}
	s.ClearNotices()
}

func policyHeld(s *fixture.RoleMembershipScenario, q string, body func(*sql.Stmt)) {
	s.T.Helper()
	stmt, err := s.C.PrepareContext(s.P.Ctx, q)
	if err != nil {
		s.T.Fatal(err)
	}
	defer stmt.Close()
	body(stmt)
}

func TestReferenceRLSPolicyDefinition(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"policy_target"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE POLICY p ON t USING(id>0) WITH CHECK(allowed)")
		s.Exec("CREATE POLICY p ON u")
		policyError(s, "CREATE POLICY p ON t", "42710", `policy "p" for table "t" already exists`, "", 0)
		for _, mode := range []string{"PERMISSIVE", "RESTRICTIVE"} {
			for _, command := range []string{"ALL", "SELECT", "INSERT", "UPDATE", "DELETE"} {
				name := strings.ToLower(mode + "_" + command)
				clauses := " USING(allowed)"
				if command == "INSERT" {
					clauses = " WITH CHECK(allowed)"
				}
				if command == "ALL" || command == "UPDATE" {
					clauses += " WITH CHECK(id>0)"
				}
				s.Exec(fmt.Sprintf("CREATE POLICY %s ON t AS %s FOR %s TO policy_target%s", name, mode, command, clauses))
				s.Exec("ALTER POLICY " + name + " ON t")
				s.Exec("DROP POLICY " + name + " ON t RESTRICT")
			}
		}
		for _, name := range []string{"MiXeD", `"Quoted"`, `"策略"`, "policy", "permissive", "restrictive"} {
			s.Exec("CREATE POLICY " + name + " ON t")
			s.Exec("DROP POLICY " + name + " ON t CASCADE")
		}
		long := strings.Repeat("x", 64)
		s.Exec("CREATE POLICY " + long + " ON t")
		policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, long, long[:63])})
		s.Exec("DROP POLICY " + long[:63] + " ON t")
		policyHeld(s, "ALTER POLICY p ON t USING(id>1)", func(stmt *sql.Stmt) {
			for range 2 {
				fixture.RunStep(s.T, s.P.Ctx, fixture.HeldStatement{Executor: s.C, Statement: stmt, SQL: "ALTER POLICY p ON t USING(id>1)"}, s.Record, fixture.Exec("ALTER POLICY p ON t USING(id>1)", 0))
			}
		})
		// Query utility results also have no result columns or rows.
		var rows *sql.Rows
		var err error
		if s.Prepared {
			stmt, e := s.C.PrepareContext(s.P.Ctx, "ALTER POLICY p ON t")
			if e != nil {
				s.T.Fatal(e)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(s.P.Ctx)
		} else {
			rows, err = s.C.QueryContext(s.P.Ctx, "ALTER POLICY p ON t")
		}
		if err != nil {
			s.T.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil || len(cols) != 0 || rows.Next() || rows.Err() != nil {
			s.T.Fatal("utility cursor", cols, err, rows.Err())
		}
		rows.Close()
		s.Exec("DROP POLICY p ON t")
		s.Exec("DROP POLICY p ON u")
		policyRows(s)
	})
}

func TestReferenceRLSPolicyFeatureSmoke(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"policy_smoke_target"}, func(s *fixture.RoleMembershipScenario) {
		policySetup(s)
		s.Exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_smoke_target USING(id>0) WITH CHECK(allowed)")
		policyError(s, "CREATE POLICY p ON t", "42710", `policy "p" for table "t" already exists`, "", 0)
		s.Exec("ALTER POLICY p ON t USING(id>1)")
		s.Exec("ALTER POLICY p ON t RENAME TO renamed")
		policyError(s, "DROP ROLE policy_smoke_target", "2BP01", `role "policy_smoke_target" cannot be dropped because some objects depend on it`, "target of policy renamed on table t", 0)
		s.Exec("DROP POLICY renamed ON t")
		s.Exec("DROP ROLE policy_smoke_target")
		policyError(s, "DROP POLICY renamed ON t", "42704", `policy "renamed" for table "t" does not exist`, "", 0)
		s.Exec("DROP POLICY IF EXISTS renamed ON t")
		policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `policy "renamed" for relation "t" does not exist, skipping`})
		policyRows(s)
	})
}
