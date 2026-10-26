package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLockSettingsAuthority(t *testing.T) {
	physicalCases(t, []string{"lock_authority_login", "lock_authority_grantee", "lock_authority_super"}, func(s *physicalCase) {
		s.run(fixture.Exec("CREATE ROLE lock_authority_login LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("CREATE ROLE lock_authority_grantee", 0))
		s.run(fixture.Exec("CREATE ROLE lock_authority_super SUPERUSER", 0))
		ordinary := s.login("lock_authority_login")
		denied := clientSetDenied("deadlock_timeout")
		deny := func() {
			for _, prepared := range []bool{false, true} {
				ordinary.run(physicalError("SET deadlock_timeout='broken'", denied, prepared, false))
				ordinary.run(physicalError("SELECT set_config('deadlock_timeout',$1,false)", denied, prepared, true, "broken"))
			}
		}
		deny()
		s.run(fixture.Exec("GRANT pg_read_all_settings TO lock_authority_login", 0))
		deny()
		s.run(fixture.Exec("GRANT lock_authority_super TO lock_authority_login", 0))
		deny()
		s.run(fixture.Exec("SET deadlock_timeout='2s'", 0))
		s.lockReads(true, "2s")
		s.run(fixture.Exec("RESET deadlock_timeout", 0))
		query := "SET deadlock_timeout='2s'"
		held, err := ordinary.conn.PrepareContext(s.p.Ctx, query)
		if err != nil { s.t.Fatal(err) }
		defer held.Close()
		ordinary.lockReads(false, "1s")
		execute := func(step fixture.Step) { fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor:ordinary.conn, Statement:held, SQL:query}, ordinary.recorder, step) }
		execute(physicalError(query, denied, false, false))
		for _, grantee := range []string{"lock_authority_login", "lock_authority_grantee"} {
			if grantee == "lock_authority_grantee" { s.run(fixture.Exec("GRANT lock_authority_grantee TO lock_authority_login WITH INHERIT TRUE", 0)) }
			s.run(fixture.Exec("GRANT SET ON PARAMETER deadlock_timeout TO "+grantee, 0))
			execute(fixture.Exec(query, 0))
			ordinary.lockInventory(true, "2000", "session")
			s.run(fixture.Exec("REVOKE SET ON PARAMETER deadlock_timeout FROM "+grantee, 0))
			execute(physicalError(query, denied, false, false))
			ordinary.lockReads(true, "2s")
			ordinary.run(physicalError("RESET deadlock_timeout", denied, true, false))
			ordinary.run(fixture.Exec("BEGIN", 0))
			ordinary.run(fixture.Exec("RESET ALL", 0))
			ordinary.lockReads(false, "1s")
			ordinary.run(fixture.Exec("ROLLBACK", 0))
			ordinary.lockReads(false, "2s")
			ordinary.run(fixture.Exec("BEGIN", 0))
			ordinary.run(fixture.Exec("RESET ALL", 0))
			ordinary.run(fixture.Exec("COMMIT", 0))
			ordinary.lockInventory(false, "1000", "default")
			s.run(fixture.Exec("GRANT SET ON PARAMETER deadlock_timeout TO "+grantee, 0))
			execute(fixture.Exec(query, 0))
			ordinary.lockReads(false, "2s")
			s.run(fixture.Exec("REVOKE SET ON PARAMETER deadlock_timeout FROM "+grantee, 0))
			ordinary.run(fixture.Exec("RESET ALL", 0))
			s.lockReads(true, "1s")
		}
	})
}
