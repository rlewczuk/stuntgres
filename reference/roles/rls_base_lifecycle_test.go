package reference_test

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceRLSBaseLocksAndCancellation(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		blocker, err := db.Conn(s.P.Ctx)
		if err != nil {
			s.T.Fatal(err)
		}
		defer blocker.Close()
		defer blocker.ExecContext(context.Background(), "ROLLBACK")
		for _, q := range []string{"BEGIN", "LOCK TABLE t IN ACCESS SHARE MODE"} {
			if _, err := blocker.ExecContext(s.P.Ctx, q); err != nil {
				s.T.Fatal(err)
			}
		}
		// The granted incompatible lock is the synchronization event, not a sleep.
		// A no-op DISABLE still requests AX and must not bypass this holder; the
		// exact lock-timeout outcome proves the wait.
		s.Exec("SET lock_timeout='10ms'")
		predefinedFailure(s, "ALTER TABLE t DISABLE ROW LEVEL SECURITY", fixture.Diagnostic{Code: "55P03", Message: "canceling statement due to lock timeout"}, false, "execute")
		s.Exec("RESET lock_timeout")
		if _, err := blocker.ExecContext(s.P.Ctx, "ROLLBACK"); err != nil {
			s.T.Fatal(err)
		}
		rlsBaseEnabled(s, false)
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY, FORCE ROW LEVEL SECURITY")
		rlsBaseRows(s, nil)
	})
}

func TestReferenceRLSBaseControls(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		// Revoking grants leaves an explicit ACL, which Dump intentionally
		// excludes. Recreate the relation with its genuine default owner ACL.
		s.Exec("DROP TABLE t")
		fixture.WithPublicSchemaOwner(s.T, s.P, s.Backend, "rls_owner", func() { s.Exec("CREATE TABLE t(id INT PRIMARY KEY,v INT DEFAULT 9)") })
		s.Run(fixture.Exec("INSERT INTO t VALUES(1,10),(2,20)", 2))
		s.Exec("CREATE POLICY retained ON t TO rls_other USING(id>0) WITH CHECK(v>0)")
		for _, state := range []struct{ enable, force bool }{{false, false}, {false, true}, {true, false}, {true, true}} {
			s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY, NO FORCE ROW LEVEL SECURITY")
			if state.force {
				s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
			}
			if state.enable {
				s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
			}
			retained, err := s.Backend.DB.QueryContext(s.P.Ctx, "SELECT id FROM t ORDER BY id")
			if err != nil {
				s.T.Fatal(err)
			}
			s.T.Cleanup(func() { retained.Close() })
			columns, err := retained.Columns()
			if err != nil || len(columns) != 1 || columns[0] != "id" {
				s.T.Fatal("retained metadata", columns, err)
			}
			if !s.Backend.PG {
				before := policyDump(s)
				if strings.Contains(before, "CREATE ROLE") {
					s.T.Fatal("Dump serialized shared roles")
				}
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(before)); err != nil {
					s.T.Fatal(err)
				}
				if after := policyDump(s); after != before {
					s.T.Fatalf("flags/policy/data changed on Restore\n%s\n%s", before, after)
				}
				// A late failed replacement cannot publish any earlier flag action.
				bad := before + "\nCREATE POLICY bad ON t TO rls_missing USING(TRUE);"
				if err := stuntgres.Restore(s.P.Ctx, s.P.Native, strings.NewReader(bad)); err == nil {
					s.T.Fatal("late invalid Restore succeeded")
				}
				ctx, cancel := context.WithCancel(s.P.Ctx)
				r := &policyReader{reader: strings.NewReader(before), before: cancel}
				err := stuntgres.Restore(ctx, s.P.Native, r)
				cancel()
				if !errors.Is(err, context.Canceled) {
					s.T.Fatal("canceled Restore", err)
				}
				if policyDump(s) != before {
					s.T.Fatal("failed/canceled replacement changed flags/policy/data")
				}
			}
			rlsBaseEnabled(s, state.enable)
			if state.enable && state.force {
				rlsBaseRows(s, nil)
			} else {
				rlsBaseRows(s, rlsBaseSeedRows())
			}
			// Enabling the restored disabled+forced state must still force owner.
			s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
			if state.force {
				rlsBaseRows(s, nil)
			} else {
				rlsBaseRows(s, rlsBaseSeedRows())
			}
			s.Actor("")
			rlsBaseRows(s, rlsBaseSeedRows())
			s.Actor("rls_owner")
			for _, want := range []int64{1, 2} {
				if !retained.Next() {
					s.T.Fatal("retained row missing", retained.Err())
				}
				var id int64
				if err := retained.Scan(&id); err != nil || id != want {
					s.T.Fatal("retained row", id, err)
				}
			}
			if retained.Next() || retained.Err() != nil {
				s.T.Fatal("retained tail", retained.Err())
			}
			retained.Close()
		}
		s.Exec("ALTER TABLE t DISABLE ROW LEVEL SECURITY")
		s.Exec("ALTER POLICY retained ON t RENAME TO retained_after_controls")
		rlsBaseRows(s, rlsBaseSeedRows())
	})
}
