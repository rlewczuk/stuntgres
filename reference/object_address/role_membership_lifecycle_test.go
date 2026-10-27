package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"stuntgres"
	"stuntgres/reference/fixture"
	"testing"
)

func TestReferenceRoleMembershipTransactionBoundary(t *testing.T) {
	for _, q := range []string{"GRANT mt_role TO mt_member", "REVOKE mt_role FROM mt_member", "ALTER GROUP mt_role ADD USER mt_member", "ALTER GROUP mt_role DROP USER mt_member", "CREATE ROLE mt_new IN ROLE mt_role"} {
		t.Run(q, func(t *testing.T) {
			fixture.RoleReferenceCleanup(t, []string{"mt_new"})
			fixture.RoleMembershipCases(t, []string{"mt_role", "mt_member"}, func(s *fixture.RoleMembershipScenario) {
				t := s.T
				removing := strings.HasPrefix(q, "REVOKE") || strings.Contains(q, "DROP USER")
				if removing {
					s.Exec("GRANT mt_role TO mt_member")
				}
				tx, err := s.C.BeginTx(s.P.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				var stmt *sql.Stmt
				if s.Prepared {
					stmt, err = tx.PrepareContext(s.P.Ctx, q)
					if err == nil {
						_, err = stmt.ExecContext(s.P.Ctx)
						stmt.Close()
					}
				} else {
					_, err = tx.ExecContext(s.P.Ctx, q)
				}
				fixture.State(t, err, "")
				member := "mt_member"
				present := !removing
				if strings.HasPrefix(q, "CREATE") {
					member = "mt_new"
				}
				fixture.RunStep(t, s.P.Ctx, tx, s.Record, fixture.Select(
					"SELECT pg_has_role('"+member+"','mt_role','MEMBER') AS member",
					[]string{"member"}, []string{"BOOL"}, [][]any{{present}}))
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				if removing {
					s.Flags("mt_role", "mt_member", s.Bootstrap, 1, false, true, true)
				} else {
					s.Flags("mt_role", "mt_member", s.Bootstrap, 0, false, false, false)
				}
				s.Fail("DROP ROLE mt_new", "42704", `role "mt_new" does not exist`, "")
				tx, err = s.C.BeginTx(s.P.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				_, err = tx.ExecContext(s.P.Ctx, q)
				tag := "GRANT ROLE"
				if strings.HasPrefix(q, "REVOKE") {
					tag = "REVOKE ROLE"
				}
				if strings.HasPrefix(q, "ALTER") {
					tag = "ALTER ROLE"
				}
				if strings.HasPrefix(q, "CREATE") {
					tag = "CREATE ROLE"
				}
				want := fixture.ControlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
				fixture.AssertDiagnostic(t, err, &want)
				tx.Rollback()
				s.Exec("SET default_transaction_read_only TO on")
				_, err = s.C.ExecContext(s.P.Ctx, q)
				fixture.AssertDiagnostic(t, err, &want)
				s.Exec("SET default_transaction_read_only TO off")
				tx, err = s.C.BeginTx(s.P.Ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				stmt, err = tx.PrepareContext(s.P.Ctx, q)
				if err != nil {
					t.Fatal("read-only utility Prepare", err)
				}
				_, err = stmt.ExecContext(s.P.Ctx)
				stmt.Close()
				fixture.AssertDiagnostic(t, err, &want)
				tx.Rollback()
				tx, err = s.C.BeginTx(s.P.Ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				fixture.RunStep(t, s.P.Ctx, tx, s.Record, fixture.Exec(q, 0))
				if err := tx.Commit(); err != nil {
					t.Fatal(err)
				}
				if strings.HasPrefix(q, "CREATE") {
					s.Exec("DROP ROLE mt_new")
				} else if !removing {
					s.Flags("mt_role", "mt_member", s.Bootstrap, 1, false, true, true)
					s.Exec("REVOKE mt_role FROM mt_member")
				} else {
					s.Flags("mt_role", "mt_member", s.Bootstrap, 0, false, false, false)
				}
			})
		})
	}
}

func TestReferenceRoleMembershipControls(t *testing.T) {
	fixture.RoleMembershipCases(t, []string{"ml_owner", "ml_member"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("ml_owner")
		s.Exec("GRANT ml_owner TO ml_member")
		s.Actor("ml_member")
		s.Read(true)
		s.Run(fixture.Select("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"ml_member", "ml_member"}}))
		if s.Backend.PG {
			return
		} // Only the native host-control phase has no PG API.
		rows, err := s.C.QueryContext(s.P.Ctx, "SELECT id FROM membership_owned ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		identities, err := s.C.QueryContext(s.P.Ctx, "SELECT current_user")
		if err != nil {
			t.Fatal(err)
		}
		defer identities.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.P.Ctx, s.P.Native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, ddl := range []string{"CREATE ROLE", "GRANT ", "REVOKE ", "ALTER GROUP"} {
			if strings.Contains(dump.String(), ddl) {
				t.Fatal("membership serialized", dump.String())
			}
		}
		if err := stuntgres.Reset(s.P.Ctx, s.P.Native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		s.Read(true)
		var id int
		if !rows.Next() || rows.Scan(&id) != nil || id != 7 || rows.Next() || rows.Err() != nil {
			t.Fatal("retained rows", id, rows.Err())
		}
		var name string
		if !identities.Next() || identities.Scan(&name) != nil || name != "ml_member" || identities.Next() || identities.Err() != nil {
			t.Fatal("retained identity", name, identities.Err())
		}
		for _, script := range []string{"GRANT ml_owner TO ml_member;", "CREATE ROLE ml_rejected IN ROLE ml_owner;"} {
			fixture.State(t, stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(script)), "0A000")
		}
		s.Read(true)
		s.Actor("")
		s.Exec("REVOKE INHERIT OPTION FOR ml_owner FROM ml_member")
		s.Actor("ml_member")
		s.Read(false)
	})
}

func TestReferenceRoleMembershipFeatureSmoke(t *testing.T) {
	fixture.RoleReferenceCleanup(t, []string{"ms_new"})
	fixture.RoleMembershipCases(t, []string{"ms_owner", "ms_member", "ms_admin", "ms_child", "ms_parent"}, func(s *fixture.RoleMembershipScenario) {
		t := s.T
		s.Owned("ms_owner")
		s.Exec("GRANT ms_owner TO ms_member WITH INHERIT FALSE, SET FALSE")
		s.Actor("ms_member")
		s.Read(false)
		s.Actor("")
		s.Exec("GRANT ms_owner TO ms_member WITH INHERIT TRUE")
		s.Actor("ms_member")
		s.Read(true)
		s.Actor("")
		s.Flags("ms_owner", "ms_member", s.Bootstrap, 1, false, true, false)
		s.Exec("GRANT ms_owner TO ms_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.Actor("ms_admin")
		s.Exec("GRANT ms_owner TO ms_child")
		s.Actor("ms_child")
		message, detail := fixture.RoleMembershipDenied("ms_owner", "grant")
		s.Fail("GRANT ms_owner TO ms_parent", "42501", message, detail)
		s.Actor("")
		s.Restrict("REVOKE ADMIN OPTION FOR ms_owner FROM ms_admin")
		s.Actor("ms_child")
		s.Read(true)
		s.Actor("")
		s.Exec("REVOKE ADMIN OPTION FOR ms_owner FROM ms_admin CASCADE")
		s.Actor("ms_child")
		s.Read(false)
		s.Actor("")
		s.Exec("CREATE ROLE ms_new LOGIN IN ROLE ms_parent ROLE ms_member ADMIN ms_admin")
		s.Actor("ms_admin")
		s.Exec("GRANT ms_new TO ms_child")
		s.Actor("")
		s.Exec("ALTER GROUP ms_owner ADD USER ms_child")
		s.Actor("ms_child")
		s.Read(true)
		s.Actor("")
		s.Exec("ALTER GROUP ms_owner DROP USER ms_child")
		s.Actor("ms_child")
		s.Read(false)
		s.Actor("")
		s.Fail("GRANT ms_member TO ms_owner WITH INHERIT FALSE, SET FALSE", "0LP01", `role "ms_member" is a member of role "ms_owner"`, "")
		s.Fail("GRANT ms_owner TO public", "42704", `role "public" does not exist`, "")
		s.Actor("ms_member")
		held, err := s.C.PrepareContext(s.P.Ctx, "SELECT id FROM membership_owned ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var id int
		if err := held.QueryRowContext(s.P.Ctx).Scan(&id); err != nil || id != 7 {
			t.Fatal(id, err)
		}
		s.Actor("")
		s.Exec("REVOKE INHERIT OPTION FOR ms_owner FROM ms_member")
		s.Actor("ms_member")
		err = held.QueryRowContext(s.P.Ctx).Scan(&id)
		want := fixture.ControlsReferenceDiagnostic("42501", "permission denied for table membership_owned")
		fixture.AssertDiagnostic(t, err, &want)
		s.Actor("")
		s.Exec("GRANT ms_owner TO ms_member WITH INHERIT TRUE")
		s.Actor("ms_member")
		if err := held.QueryRowContext(s.P.Ctx).Scan(&id); err != nil || id != 7 {
			t.Fatal(id, err)
		}
		s.Actor("")
		s.Exec("DROP ROLE ms_new")
	})
}
