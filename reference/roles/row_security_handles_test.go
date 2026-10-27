package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityHeldHandles(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		read := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		write := predefinedHeld(s, "INSERT INTO t VALUES($1,$2)")
		s.Exec("PREPARE setting_sql AS SELECT id FROM t ORDER BY id")
		rowSecurityHeldRows(s, read, nil)
		predefinedSelect(s, "EXECUTE setting_sql", []string{"id"}, []string{"INT4"}, nil)
		s.Exec("SET row_security=off")
		rowSecurityHeldOff(s, read, true)
		step := fixture.ErrorDiagnostic(write.SQL, rowSecurityOffDiagnostic("t", false), int64(3), int64(30))
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, write, s.Record, step)
		// The pinned oracle revalidates SQL EXECUTE during protocol Prepare
		// as well as direct execution; this differs from a held *sql.Stmt.
		predefinedFailure(s, "EXECUTE setting_sql", rowSecurityOffDiagnostic("t", false), true, "prepare")
		s.Exec("SET row_security=on")
		rowSecurityHeldRows(s, read, nil)
		step = fixture.ImplicitError(write.SQL, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`}, int64(3), int64(30))
		step.ErrorStage = "execute"
		fixture.RunStep(s.T, s.P.Ctx, write, s.Record, step)
		predefinedSelect(s, "EXECUTE setting_sql", []string{"id"}, []string{"INT4"}, nil)
		s.Exec("DEALLOCATE setting_sql")
		// A captured empty on-result must not acquire a later off error.
		rows, err := s.C.QueryContext(s.P.Ctx, read.SQL)
		if err != nil {
			s.T.Fatal(err)
		}
		defer rows.Close()
		// Only native eager rows allow a same-session command before draining.
		// The PostgreSQL companion drains its protocol stream before SET.
		if !s.Backend.PG {
			s.Exec("SET row_security=off")
		}
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"id"}) {
			s.T.Fatal(columns, err)
		}
		if rows.Next() || rows.Err() != nil {
			s.T.Fatal("old on rows changed", rows.Err())
		}
		rows.Close()
		if s.Backend.PG {
			s.Exec("SET row_security=off")
		}
		rowSecurityUnchanged(s)
		// Fresh authority on the SAME read handle: owner bypass, FORCE, ACL.
		s.Actor("rls_owner")
		rowSecurityHeldRows(s, read, rlsBaseSeedRows())
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		ownerStep := fixture.ErrorDiagnostic(read.SQL, rowSecurityOffDiagnostic("t", true))
		ownerStep.SelectRows, ownerStep.ErrorStage = true, "execute"
		fixture.RunStep(s.T, s.P.Ctx, read, s.Record, ownerStep)
		s.Exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		s.Exec("REVOKE SELECT ON t FROM rls_reader")
		s.Actor("rls_reader")
		s.Exec("SET row_security=on")
		acl := fixture.ImplicitError(read.SQL, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		acl.SelectRows, acl.ErrorStage = true, "execute"
		fixture.RunStep(s.T, s.P.Ctx, read, s.Record, acl)
	})
}

func TestReferenceRowSecurityFeatureSmoke(t *testing.T) {
	rlsBaseCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		s.Exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		peer := rowSecurityPeer(s, db)
		rowSecurityValue(s, "on")
		rowSecurityMetadata(s, "on", "default")
		rlsBaseRows(s, nil)
		held := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		rowSecurityHeldRows(s, held, nil)
		s.Exec("SET row_security=off")
		rowSecurityValue(s, "off")
		rowSecurityMetadata(s, "off", "session")
		rowSecurityHeldOff(s, held, true)
		s.Actor("rls_owner")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.Exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rowSecurityOff(s, "SELECT id FROM t", "t", true, true)
		s.Exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		s.Actor("rls_reader")
		s.Exec("RESET row_security")
		s.Exec("BEGIN")
		defer s.C.ExecContext(s.P.Ctx, "ROLLBACK")
		s.Exec("SAVEPOINT healthy")
		s.Exec("SET LOCAL row_security=off")
		rowSecurityHeldOff(s, held, true)
		rowSecurityRecovery(s)
		rowSecurityValue(s, "on")
		rowSecurityMetadata(s, "on", "default")
		rowSecurityHeldRows(s, held, nil)
		s.Exec("COMMIT")
		rowSecurityValue(peer, "on")
		rowSecurityMetadata(peer, "on", "default")
		rlsBaseRows(peer, nil)
		rowSecurityUnchanged(s)
	})
}
