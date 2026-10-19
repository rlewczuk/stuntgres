package reference_test

import (
	"database/sql"
	"reflect"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceRowSecurityHeldHandles(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		read := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		write := predefinedHeld(s, "INSERT INTO t VALUES($1,$2)")
		s.exec("PREPARE setting_sql AS SELECT id FROM t ORDER BY id")
		rowSecurityHeldRows(s, read, nil)
		predefinedSelect(s, "EXECUTE setting_sql", []string{"id"}, []string{"INT4"}, nil)
		s.exec("SET row_security=off")
		rowSecurityHeldOff(s, read, true)
		step := fixture.ErrorDiagnostic(write.SQL, rowSecurityOffDiagnostic("t", false), int64(3), int64(30))
		step.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, write, s.record, step)
		// The pinned oracle revalidates SQL EXECUTE during protocol Prepare
		// as well as direct execution; this differs from a held *sql.Stmt.
		predefinedFailure(s, "EXECUTE setting_sql", rowSecurityOffDiagnostic("t", false), true, "prepare")
		s.exec("SET row_security=on")
		rowSecurityHeldRows(s, read, nil)
		step = fixture.ImplicitError(write.SQL, fixture.Diagnostic{Code: "42501", Message: `new row violates row-level security policy for table "t"`}, int64(3), int64(30))
		step.ErrorStage = "execute"
		fixture.RunStep(s.t, s.p.Ctx, write, s.record, step)
		predefinedSelect(s, "EXECUTE setting_sql", []string{"id"}, []string{"INT4"}, nil)
		s.exec("DEALLOCATE setting_sql")
		// A captured empty on-result must not acquire a later off error.
		rows, err := s.c.QueryContext(s.p.Ctx, read.SQL)
		if err != nil {
			s.t.Fatal(err)
		}
		defer rows.Close()
		// Only native eager rows allow a same-session command before draining.
		// The PostgreSQL companion drains its protocol stream before SET.
		if !s.backend.PG {
			s.exec("SET row_security=off")
		}
		columns, err := rows.Columns()
		if err != nil || !reflect.DeepEqual(columns, []string{"id"}) {
			s.t.Fatal(columns, err)
		}
		if rows.Next() || rows.Err() != nil {
			s.t.Fatal("old on rows changed", rows.Err())
		}
		rows.Close()
		if s.backend.PG {
			s.exec("SET row_security=off")
		}
		rowSecurityUnchanged(s)
		// Fresh authority on the SAME read handle: owner bypass, FORCE, ACL.
		s.actor("rls_owner")
		rowSecurityHeldRows(s, read, rlsBaseSeedRows())
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		ownerStep := fixture.ErrorDiagnostic(read.SQL, rowSecurityOffDiagnostic("t", true))
		ownerStep.SelectRows, ownerStep.ErrorStage = true, "execute"
		fixture.RunStep(s.t, s.p.Ctx, read, s.record, ownerStep)
		s.exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		s.exec("REVOKE SELECT ON t FROM rls_reader")
		s.actor("rls_reader")
		s.exec("SET row_security=on")
		acl := fixture.ImplicitError(read.SQL, fixture.Diagnostic{Code: "42501", Message: "permission denied for table t"})
		acl.SelectRows, acl.ErrorStage = true, "execute"
		fixture.RunStep(s.t, s.p.Ctx, read, s.record, acl)
	})
}

func TestReferenceRowSecurityFeatureSmoke(t *testing.T) {
	rlsBaseCases(t, func(s *membershipScenario, db *sql.DB) {
		s.exec("ALTER TABLE t ENABLE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		peer := rowSecurityPeer(s, db)
		rowSecurityValue(s, "on")
		rowSecurityMetadata(s, "on", "default")
		rlsBaseRows(s, nil)
		held := predefinedHeld(s, "SELECT id FROM t ORDER BY id")
		rowSecurityHeldRows(s, held, nil)
		s.exec("SET row_security=off")
		rowSecurityValue(s, "off")
		rowSecurityMetadata(s, "off", "session")
		rowSecurityHeldOff(s, held, true)
		s.actor("rls_owner")
		rlsBaseRows(s, rlsBaseSeedRows())
		s.exec("ALTER TABLE t FORCE ROW LEVEL SECURITY")
		rowSecurityOff(s, "SELECT id FROM t", "t", true, true)
		s.exec("ALTER TABLE t NO FORCE ROW LEVEL SECURITY")
		s.actor("rls_reader")
		s.exec("RESET row_security")
		s.exec("BEGIN")
		defer s.c.ExecContext(s.p.Ctx, "ROLLBACK")
		s.exec("SAVEPOINT healthy")
		s.exec("SET LOCAL row_security=off")
		rowSecurityHeldOff(s, held, true)
		rowSecurityRecovery(s)
		rowSecurityValue(s, "on")
		rowSecurityMetadata(s, "on", "default")
		rowSecurityHeldRows(s, held, nil)
		s.exec("COMMIT")
		rowSecurityValue(peer, "on")
		rowSecurityMetadata(peer, "on", "default")
		rlsBaseRows(peer, nil)
		rowSecurityUnchanged(s)
	})
}
