package reference_test

import (
	"database/sql"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func tempAsAdmissionCircular(s *fixture.TempReferenceSession, _ *[]fixture.Diagnostic, noData bool) {
	tempAsAdmissionSettings(s)
	// Avoid namespace bootstrap changing the measured selector's initial phase.
	tempAsExec(s, s.C, "CREATE TEMP TABLE ta_cycle_bootstrap(v INT)", 0)
	tempAsExec(s, s.C, "DROP TABLE ta_cycle_bootstrap", 0)
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "SET temp_tablespaces=pg_default, pg_global", 0)
	var successes [6]bool
	count := 0
	for i := range successes {
		tempAsExec(s, s.C, "SAVEPOINT ta_attempt", 0)
		name := fmt.Sprintf("ta_cycle_%d", i)
		q := tempAsAdmissionQuery("CREATE TEMP TABLE", name, noData)
		var result sql.Result
		var err error
		if s.Prepared {
			stmt, prepareErr := s.C.PrepareContext(s.P.Ctx, q)
			if prepareErr != nil {
				s.T.Fatal("Prepare performed tablespace admission", prepareErr)
			}
			result, err = stmt.ExecContext(s.P.Ctx)
			if closeErr := stmt.Close(); closeErr != nil {
				s.T.Fatal(closeErr)
			}
		} else {
			result, err = s.C.ExecContext(s.P.Ctx, q)
		}
		if err == nil {
			n, affectedErr := result.RowsAffected()
			if affectedErr != nil || n != tempAsAdmissionCount(noData) {
				s.T.Fatalf("attempt %d affected=%d: %v", i, n, affectedErr)
			}
			successes[i] = true
			count++
			tempAsAdmissionRows(s, name, noData)
		} else {
			d := fixture.ControlsReferenceDiagnostic("22023", "only shared relations can be placed in pg_global tablespace")
			fixture.AssertDiagnostic(s.T, err, &d)
			tempAsExec(s, s.C, "ROLLBACK TO ta_attempt", 0)
			tempAsAbsent(s, name)
		}
		tempAsExec(s, s.C, "RELEASE ta_attempt", 0)
	}
	if count != 3 {
		s.T.Fatalf("circular policy successes=%d, want 3: %v", count, successes)
	}
	for i := 1; i < len(successes); i++ {
		if successes[i] == successes[i-1] {
			s.T.Fatalf("failed CTAS/savepoint rewound selector: %v", successes)
		}
	}
	tempAsExec(s, s.C, "COMMIT", 0)
	tempAsAdmissionPolicy(s, "pg_default, pg_global")
}

func tempAsAdmissionLocal(s *fixture.TempReferenceSession, _ *[]fixture.Diagnostic, noData bool) {
	tempAsAdmissionSettings(s)
	tempAsExec(s, s.C, "BEGIN", 0)
	tempAsExec(s, s.C, "SAVEPOINT ta_before_policy", 0)
	tempAsExec(s, s.C, "SET LOCAL temp_tablespaces='pg_global'", 0)
	tempAsExec(s, s.C, "SAVEPOINT ta_after_policy", 0)
	tempAsAdmissionGlobal(s, s.C, tempAsAdmissionQuery("CREATE TEMP TABLE", "ta_local_failure", noData))
	tempAsExec(s, s.C, "ROLLBACK TO ta_after_policy", 0)
	tempAsAdmissionPolicy(s, "pg_global")
	tempAsAbsent(s, "ta_local_failure")
	tempAsExec(s, s.C, "ROLLBACK TO ta_before_policy", 0)
	tempAsAdmissionPolicy(s, "")
	tempAsExec(s, s.C, tempAsAdmissionQuery("CREATE TEMP TABLE", "ta_local_restored", noData), tempAsAdmissionCount(noData))
	tempAsAdmissionRows(s, "ta_local_restored", noData)
	tempAsExec(s, s.C, "COMMIT", 0)
	tempAsAdmissionPolicy(s, "")
	for i, end := range []string{"COMMIT", "ROLLBACK"} {
		tempAsExec(s, s.C, "SET temp_tablespaces='pg_global'", 0)
		tempAsExec(s, s.C, "BEGIN", 0)
		tempAsExec(s, s.C, "SET LOCAL temp_tablespaces=''", 0)
		// Explicit empty assignment is canonically shown as a quoted empty member.
		tempAsAdmissionPolicy(s, `""`)
		name := fmt.Sprintf("ta_local_override_%d", i)
		tempAsExec(s, s.C, tempAsAdmissionQuery("CREATE TEMP TABLE", name, noData), tempAsAdmissionCount(noData))
		tempAsAdmissionRows(s, name, noData)
		tempAsExec(s, s.C, end, 0)
		tempAsAdmissionPolicy(s, "pg_global")
		blocked := fmt.Sprintf("ta_local_blocked_%d", i)
		tempAsAdmissionGlobal(s, s.C, tempAsAdmissionQuery("CREATE TEMP TABLE", blocked, noData))
		tempAsAbsent(s, blocked)
	}
}

func TestReferenceCreateTempTableAsTablespaceSelection(t *testing.T) {
	for _, noData := range []bool{false, true} {
		for _, policy := range []struct {
			name string
			body func(*fixture.TempReferenceSession, *[]fixture.Diagnostic, bool)
		}{{"circular", tempAsAdmissionCircular}, {"local-restoration", tempAsAdmissionLocal}} {
			t.Run(fmt.Sprintf("%s/no-data-%t", policy.name, noData), func(t *testing.T) {
				tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, notices *[]fixture.Diagnostic) { policy.body(s, notices, noData) })
			})
		}
	}
}

func TestReferenceCreateTempTableAsAdmissionFeatureSmoke(t *testing.T) {
	t.Run("default-global", func(t *testing.T) {
		tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
			tempAsAdmissionDefault(s, n, "CREATE TEMP TABLE", "ta_space_ok", false)
		})
	})
	t.Run("temp-global", func(t *testing.T) {
		tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
			tempAsAdmissionTempGlobal(s, n, "CREATE TEMP TABLE", "ta_space_rejected", false)
		})
	})
	t.Run("circular-policy", func(t *testing.T) {
		tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) { tempAsAdmissionCircular(s, n, true) })
	})
	t.Run("restricted-existing-index", func(t *testing.T) {
		tempAsAdmissionScenario(t, func(s *fixture.TempReferenceSession, n *[]fixture.Diagnostic) {
			tempAsAdmissionRestricted(s, n, false, true, false)
		})
	})
}
