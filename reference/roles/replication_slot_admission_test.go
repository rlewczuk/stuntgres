package reference_test

import (
	"database/sql"
	"testing"

	"stuntgres/reference/fixture"
)

// Logical slot creation admits its output plugin and transaction state before
// publishing anything: an allow-listed library that does not exist, or a
// transaction holding an XID, leaves no slot behind.
func TestReferenceReplicationSlotPluginAdmission(t *testing.T) {
	slotCases(t, func(s *fixture.RoleMembershipScenario, db *sql.DB) {
		if s.Backend.PG {
			slotCleanup(s.T, db, "rsl_admission")
		}
		s.Exec("SET output_plugin_libraries = rsl_absent_plugin")
		defer s.C.ExecContext(s.P.Ctx, "RESET output_plugin_libraries")
		for _, temporary := range []string{"false", "true"} {
			slotFailure(s, "SELECT * FROM pg_create_logical_replication_slot('rsl_admission','rsl_absent_plugin',"+temporary+")", slotPluginMissing("rsl_absent_plugin"))
			slotInventoryAbsent(s, s.C, "rsl_admission")
		}
		s.Exec("BEGIN")
		predefinedSelect(s, "SELECT pg_current_xact_id() IS NOT NULL AS assigned", []string{"assigned"}, []string{"BOOL"}, [][]any{{true}})
		slotFailure(s, "SELECT * FROM pg_create_logical_replication_slot('rsl_admission','rsl_absent_plugin')", fixture.Diagnostic{Code: "25001", Message: "cannot create logical replication slot in transaction that has performed writes"})
		s.Exec("ROLLBACK")
		slotInventoryAbsent(s, s.C, "rsl_admission")
	})
}
