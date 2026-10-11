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
func policyError(s *membershipScenario, q, code, message, detail string, position int) {
	s.t.Helper()
	step := fixture.ImplicitError(q, fixture.Diagnostic{Code: code, Message: message, Detail: detail, Position: fixture.Position(position)})
	step.ErrorStage = "execute"
	s.run(step)
}

func policySetup(s *membershipScenario) {
	s.exec("CREATE TABLE t(id INTEGER, allowed BOOLEAN)")
	s.exec("CREATE TABLE u(id INTEGER, allowed BOOLEAN)")
	s.run(fixture.Exec("INSERT INTO t VALUES(1,TRUE),(2,FALSE)", 2))
}

func policyRows(s *membershipScenario) {
	s.t.Helper()
	s.run(fixture.Select("SELECT id,allowed FROM t ORDER BY id", []string{"id", "allowed"}, []string{"INT4", "BOOL"}, [][]any{{int64(1), true}, {int64(2), false}}))
}

func policyNotice(s *membershipScenario, want fixture.Diagnostic) {
	s.t.Helper()
	if !reflect.DeepEqual(*s.notices, []fixture.Diagnostic{want}) {
		s.t.Fatalf("notices = %#v; want %#v", *s.notices, want)
	}
	s.clearNotices()
}

func policyHeld(s *membershipScenario, q string, body func(*sql.Stmt)) {
	s.t.Helper()
	stmt, err := s.c.PrepareContext(s.p.Ctx, q)
	if err != nil {
		s.t.Fatal(err)
	}
	defer stmt.Close()
	body(stmt)
}

func TestReferenceRLSPolicyDefinition(t *testing.T) {
	membershipCases(t, []string{"policy_target"}, func(s *membershipScenario) {
		policySetup(s)
		s.exec("CREATE POLICY p ON t USING(id>0) WITH CHECK(allowed)")
		s.exec("CREATE POLICY p ON u")
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
				s.exec(fmt.Sprintf("CREATE POLICY %s ON t AS %s FOR %s TO policy_target%s", name, mode, command, clauses))
				s.exec("ALTER POLICY " + name + " ON t")
				s.exec("DROP POLICY " + name + " ON t RESTRICT")
			}
		}
		for _, name := range []string{"MiXeD", `"Quoted"`, `"策略"`, "policy", "permissive", "restrictive"} {
			s.exec("CREATE POLICY " + name + " ON t")
			s.exec("DROP POLICY " + name + " ON t CASCADE")
		}
		long := strings.Repeat("x", 64)
		s.exec("CREATE POLICY " + long + " ON t")
		policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "42622", Message: fmt.Sprintf(`identifier "%s" will be truncated to "%s"`, long, long[:63])})
		s.exec("DROP POLICY " + long[:63] + " ON t")
		policyHeld(s, "ALTER POLICY p ON t USING(id>1)", func(stmt *sql.Stmt) {
			for range 2 {
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor: s.c, Statement: stmt, SQL: "ALTER POLICY p ON t USING(id>1)"}, s.record, fixture.Exec("ALTER POLICY p ON t USING(id>1)", 0))
			}
		})
		// Query utility results also have no result columns or rows.
		var rows *sql.Rows
		var err error
		if s.prepared {
			stmt, e := s.c.PrepareContext(s.p.Ctx, "ALTER POLICY p ON t")
			if e != nil {
				s.t.Fatal(e)
			}
			defer stmt.Close()
			rows, err = stmt.QueryContext(s.p.Ctx)
		} else {
			rows, err = s.c.QueryContext(s.p.Ctx, "ALTER POLICY p ON t")
		}
		if err != nil {
			s.t.Fatal(err)
		}
		defer rows.Close()
		cols, err := rows.Columns()
		if err != nil || len(cols) != 0 || rows.Next() || rows.Err() != nil {
			s.t.Fatal("utility cursor", cols, err, rows.Err())
		}
		rows.Close()
		s.exec("DROP POLICY p ON t")
		s.exec("DROP POLICY p ON u")
		policyRows(s)
	})
}

func TestReferenceRLSPolicyFeatureSmoke(t *testing.T) {
	membershipCases(t, []string{"policy_smoke_target"}, func(s *membershipScenario) {
		policySetup(s)
		s.exec("CREATE POLICY p ON t AS RESTRICTIVE FOR UPDATE TO policy_smoke_target USING(id>0) WITH CHECK(allowed)")
		policyError(s, "CREATE POLICY p ON t", "42710", `policy "p" for table "t" already exists`, "", 0)
		s.exec("ALTER POLICY p ON t USING(id>1)")
		s.exec("ALTER POLICY p ON t RENAME TO renamed")
		policyError(s, "DROP ROLE policy_smoke_target", "2BP01", `role "policy_smoke_target" cannot be dropped because some objects depend on it`, "target of policy renamed on table t", 0)
		s.exec("DROP POLICY renamed ON t")
		s.exec("DROP ROLE policy_smoke_target")
		policyError(s, "DROP POLICY renamed ON t", "42704", `policy "renamed" for table "t" does not exist`, "", 0)
		s.exec("DROP POLICY IF EXISTS renamed ON t")
		policyNotice(s, fixture.Diagnostic{Severity: "NOTICE", SeverityUnlocalized: "NOTICE", Code: "00000", Message: `policy "renamed" for relation "t" does not exist, skipping`})
		policyRows(s)
	})
}
