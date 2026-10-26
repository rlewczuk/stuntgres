package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLockSettingsNonEffects(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE TABLE lock_settings_sentinel(v integer PRIMARY KEY CHECK(v=7))", 0))
		s.run(fixture.Exec("INSERT INTO lock_settings_sentinel VALUES(7)", 1))
		peer := s.lockPeer()
		for _, prepared := range []bool{false, true} {
			step := fixture.Exec("SET deadlock_timeout='1500ms'", 0)
			step.Prepared = prepared
			s.run(step)
			s.lockReads(prepared, "1500ms")
			s.lockInventory(prepared, "1500", "session")
			peer.lockReads(prepared, "1s")
			peer.lockInventory(prepared, "1000", "default")
			s.run(physicalError("SET max_locks_per_transaction=65", physicalRestart("max_locks_per_transaction"), prepared, false))
			s.run(physicalError("SET max_pred_locks_per_page=3", lockContext("max_pred_locks_per_page", "sighup"), prepared, false))
			s.run(fixture.Exec("UPDATE lock_settings_sentinel SET v=7", 1))
			d := physicalDiagnostic("23505", `duplicate key value violates unique constraint "lock_settings_sentinel_pkey"`)
			d.Detail, d.Schema, d.Table, d.Constraint = "Key (v)=(7) already exists.", "public", "lock_settings_sentinel", "lock_settings_sentinel_pkey"
			s.run(physicalError("INSERT INTO lock_settings_sentinel VALUES(7)", d, prepared, false))
			s.lockSentinel()
			peer.lockSentinel()
			s.run(fixture.Exec("RESET ALL", 0))
			s.lockInventory(prepared, "1000", "default")
			s.lockReads(prepared, "1s")
			s.lockSentinel()
		}
	})
}
