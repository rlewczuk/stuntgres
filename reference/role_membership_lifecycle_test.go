package reference_test

import (
	"bytes"
	"database/sql"
	"strings"
	"testing"

	"stuntgres"
)

func TestReferenceRoleMembershipTransactionBoundary(t *testing.T) {
	for _, q := range []string{"GRANT mt_role TO mt_member", "REVOKE mt_role FROM mt_member", "ALTER GROUP mt_role ADD USER mt_member", "CREATE ROLE mt_new IN ROLE mt_role"} {
		t.Run(q, func(t *testing.T) {
			roleReferenceCleanup(t, []string{"mt_new"})
			membershipCases(t, []string{"mt_role", "mt_member"}, func(s *membershipScenario) {
				t := s.t
				tx, err := s.c.BeginTx(s.p.ctx, nil)
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				var stmt *sql.Stmt
				if s.prepared {
					stmt, err = tx.PrepareContext(s.p.ctx, q)
					if err == nil {
						_, err = stmt.ExecContext(s.p.ctx)
						stmt.Close()
					}
				} else {
					_, err = tx.ExecContext(s.p.ctx, q)
				}
				if s.backend.pg {
					referenceState(t, err, "")
				} else {
					want := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
					referenceAssertDiagnostic(t, err, &want)
				}
				if !s.backend.pg {
					_, failed := tx.ExecContext(s.p.ctx, "SELECT current_user")
					want := controlsReferenceDiagnostic("25P02", "current transaction is aborted, commands ignored until end of transaction block")
					referenceAssertDiagnostic(t, failed, &want)
				}
				if err := tx.Rollback(); err != nil {
					t.Fatal(err)
				}
				s.flags("mt_role", "mt_member", s.bootstrap, 0, false, false, false)
				s.fail("DROP ROLE mt_new", "42704", `role "mt_new" does not exist`, "")
				tx, err = s.c.BeginTx(s.p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				defer tx.Rollback()
				_, err = tx.ExecContext(s.p.ctx, q)
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
				want := controlsReferenceDiagnostic("25006", "cannot execute "+tag+" in a read-only transaction")
				referenceAssertDiagnostic(t, err, &want)
				tx.Rollback()
				s.exec("SET default_transaction_read_only TO on")
				_, err = s.c.ExecContext(s.p.ctx, q)
				referenceAssertDiagnostic(t, err, &want)
				s.exec("SET default_transaction_read_only TO off")
				tx, err = s.c.BeginTx(s.p.ctx, &sql.TxOptions{ReadOnly: true})
				if err != nil {
					t.Fatal(err)
				}
				stmt, err = tx.PrepareContext(s.p.ctx, q)
				if s.backend.pg {
					if err != nil {
						t.Fatal("PG read-only utility Prepare", err)
					}
					_, err = stmt.ExecContext(s.p.ctx)
					stmt.Close()
					referenceAssertDiagnostic(t, err, &want)
				} else {
					boundary := controlsReferenceDiagnostic("0A000", "statement is unsupported in an explicit transaction")
					referenceAssertDiagnostic(t, err, &boundary)
				}
				tx.Rollback()
				if s.backend.pg {
					tx, err = s.c.BeginTx(s.p.ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					referenceRunStep(t, s.p.ctx, tx, s.record, referenceExec(q, 0))
					if err := tx.Commit(); err != nil {
						t.Fatal(err)
					}
					if strings.HasPrefix(q, "CREATE") {
						s.exec("DROP ROLE mt_new")
					} else if !strings.HasPrefix(q, "REVOKE") {
						s.flags("mt_role", "mt_member", s.bootstrap, 1, false, true, true)
						s.exec("REVOKE mt_role FROM mt_member")
					}
				}
			})
		})
	}
}

func TestReferenceRoleMembershipControls(t *testing.T) {
	membershipCases(t, []string{"ml_owner", "ml_member"}, func(s *membershipScenario) {
		t := s.t
		s.owned("ml_owner")
		s.exec("GRANT ml_owner TO ml_member")
		s.actor("ml_member")
		s.read(true)
		s.run(referenceSelect("SELECT session_user,current_user", []string{"session_user", "current_user"}, []string{"NAME", "NAME"}, [][]any{{"ml_member", "ml_member"}}))
		if s.backend.pg {
			return
		} // Only the native host-control phase has no PG API.
		rows, err := s.c.QueryContext(s.p.ctx, "SELECT id FROM membership_owned ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer rows.Close()
		identities, err := s.c.QueryContext(s.p.ctx, "SELECT current_user")
		if err != nil {
			t.Fatal(err)
		}
		defer identities.Close()
		var dump bytes.Buffer
		if err := stuntgres.Dump(s.p.ctx, s.p.native, &dump); err != nil {
			t.Fatal(err)
		}
		for _, ddl := range []string{"CREATE ROLE", "GRANT ", "REVOKE ", "ALTER GROUP"} {
			if strings.Contains(dump.String(), ddl) {
				t.Fatal("membership serialized", dump.String())
			}
		}
		if err := stuntgres.Reset(s.p.ctx, s.p.native); err != nil {
			t.Fatal(err)
		}
		if err := stuntgres.Restore(s.p.ctx, s.p.native, strings.NewReader(dump.String())); err != nil {
			t.Fatal(err)
		}
		s.read(true)
		var id int
		if !rows.Next() || rows.Scan(&id) != nil || id != 7 || rows.Next() || rows.Err() != nil {
			t.Fatal("retained rows", id, rows.Err())
		}
		var name string
		if !identities.Next() || identities.Scan(&name) != nil || name != "ml_member" || identities.Next() || identities.Err() != nil {
			t.Fatal("retained identity", name, identities.Err())
		}
		want := controlsReferenceDiagnostic("0A000", "restore accepts only CREATE/ALTER/DROP SCHEMA, CREATE TABLE/SEQUENCE/VIEW/INDEX, INSERT, deferred reference DEFAULT, database-local COMMENT, generated sequence ownership/state and SET SESSION AUTHORIZATION")
		for _, script := range []string{"GRANT ml_owner TO ml_member;", "CREATE ROLE ml_rejected IN ROLE ml_owner;"} {
			referenceAssertDiagnostic(t, stuntgres.Restore(s.p.ctx, s.p.native, strings.NewReader(script)), &want)
		}
		s.read(true)
		s.actor("")
		s.exec("REVOKE INHERIT OPTION FOR ml_owner FROM ml_member")
		s.actor("ml_member")
		s.read(false)
	})
}

func TestReferenceRoleMembershipFeatureSmoke(t *testing.T) {
	roleReferenceCleanup(t, []string{"ms_new"})
	membershipCases(t, []string{"ms_owner", "ms_member", "ms_admin", "ms_child", "ms_parent"}, func(s *membershipScenario) {
		t := s.t
		s.owned("ms_owner")
		s.exec("GRANT ms_owner TO ms_member WITH INHERIT FALSE, SET FALSE")
		s.actor("ms_member")
		s.read(false)
		s.actor("")
		s.exec("GRANT ms_owner TO ms_member WITH INHERIT TRUE")
		s.actor("ms_member")
		s.read(true)
		s.actor("")
		s.flags("ms_owner", "ms_member", s.bootstrap, 1, false, true, false)
		s.exec("GRANT ms_owner TO ms_admin WITH ADMIN TRUE, INHERIT FALSE, SET FALSE")
		s.actor("ms_admin")
		s.exec("GRANT ms_owner TO ms_child")
		s.actor("ms_child")
		message, detail := membershipDenied("ms_owner", "grant")
		s.fail("GRANT ms_owner TO ms_parent", "42501", message, detail)
		s.actor("")
		s.restrict("REVOKE ADMIN OPTION FOR ms_owner FROM ms_admin")
		s.actor("ms_child")
		s.read(true)
		s.actor("")
		s.exec("REVOKE ADMIN OPTION FOR ms_owner FROM ms_admin CASCADE")
		s.actor("ms_child")
		s.read(false)
		s.actor("")
		s.exec("CREATE ROLE ms_new LOGIN IN ROLE ms_parent ROLE ms_member ADMIN ms_admin")
		s.actor("ms_admin")
		s.exec("GRANT ms_new TO ms_child")
		s.actor("")
		s.exec("ALTER GROUP ms_owner ADD USER ms_child")
		s.actor("ms_child")
		s.read(true)
		s.actor("")
		s.exec("ALTER GROUP ms_owner DROP USER ms_child")
		s.actor("ms_child")
		s.read(false)
		s.actor("")
		s.fail("GRANT ms_member TO ms_owner WITH INHERIT FALSE, SET FALSE", "0LP01", `role "ms_member" is a member of role "ms_owner"`, "")
		s.fail("GRANT ms_owner TO public", "42704", `role "public" does not exist`, "")
		s.actor("ms_member")
		held, err := s.c.PrepareContext(s.p.ctx, "SELECT id FROM membership_owned ORDER BY id")
		if err != nil {
			t.Fatal(err)
		}
		defer held.Close()
		var id int
		if err := held.QueryRowContext(s.p.ctx).Scan(&id); err != nil || id != 7 {
			t.Fatal(id, err)
		}
		s.actor("")
		s.exec("REVOKE INHERIT OPTION FOR ms_owner FROM ms_member")
		s.actor("ms_member")
		err = held.QueryRowContext(s.p.ctx).Scan(&id)
		want := controlsReferenceDiagnostic("42501", "permission denied for table membership_owned")
		referenceAssertDiagnostic(t, err, &want)
		s.actor("")
		s.exec("GRANT ms_owner TO ms_member WITH INHERIT TRUE")
		s.actor("ms_member")
		if err := held.QueryRowContext(s.p.ctx).Scan(&id); err != nil || id != 7 {
			t.Fatal(id, err)
		}
		s.actor("")
		s.exec("DROP ROLE ms_new")
	})
}
