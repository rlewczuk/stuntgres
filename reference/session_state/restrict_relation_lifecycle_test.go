package reference_test

import (
	"testing"

	"stuntgres/reference/fixture"
)

// Recover expected SQL errors inside transactions; an aborted transaction is
// never allowed to masquerade as restored restriction behavior.
func rkTransactionalRead(s *physicalCase, restricted bool) {
	s.run(fixture.Exec("SAVEPOINT rk_expected_error", 0))
	rkRead(s, restricted)
	s.run(fixture.Exec("ROLLBACK TO rk_expected_error", 0))
	s.run(fixture.Exec("RELEASE rk_expected_error", 0))
}

func TestReferenceRestrictRelationKindLifecycle(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		rkSeed(s)
		pool := s.p.Memory
		if s.pg {
			pool = s.p.Postgres
		}
		peer, err := pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer peer.Close()
		other := &physicalCase{s.t, s.p, peer, s.pg, s.recorder}
		rkSet(s, "view")
		rkRead(s, true)
		rkRead(other, false)
		other.run(clientText("SHOW "+rkSetting, rkSetting, ""))
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SET LOCAL "+rkSetting+"=''", 0))
		rkRead(s, false)
		s.run(fixture.Exec("COMMIT", 0))
		rkRead(s, true)
		rkSet(s, "")
		s.run(fixture.Exec("BEGIN", 0))
		rkSet(s, "view")
		rkTransactionalRead(s, true)
		s.run(fixture.Exec("ROLLBACK", 0))
		rkRead(s, false)
		s.run(fixture.Exec("BEGIN", 0))
		rkSet(s, "view")
		s.run(fixture.Exec("SAVEPOINT rk_s", 0))
		s.run(fixture.Exec("SET LOCAL "+rkSetting+"=''", 0))
		rkRead(s, false)
		s.run(fixture.Exec("ROLLBACK TO rk_s", 0))
		rkTransactionalRead(s, true)
		s.run(fixture.Exec("ROLLBACK", 0))
		rkRead(s, false)
		// Session SET commits; LOCAL restores at either transaction outcome.
		s.run(fixture.Exec("BEGIN", 0))
		rkSet(s, "view")
		s.run(fixture.Exec("COMMIT", 0))
		rkRead(s, true)
		for _, end := range []string{"COMMIT", "ROLLBACK"} {
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec("SAVEPOINT rk_release", 0))
			s.run(fixture.Exec("SET LOCAL "+rkSetting+"=''", 0))
			s.run(fixture.Exec("RELEASE rk_release", 0))
			rkRead(s, false)
			s.run(fixture.Exec(end, 0))
			rkRead(s, true)
		}
		s.run(fixture.Exec("BEGIN", 0))
		s.run(fixture.Exec("SAVEPOINT rk_invalid", 0))
		s.run(physicalError("SET "+rkSetting+"='bogus,'", clientInvalid(rkSetting, "bogus,", "List syntax is invalid.", ""), false, false))
		s.run(fixture.Exec("ROLLBACK TO rk_invalid", 0))
		rkTransactionalRead(s, true)
		s.run(fixture.Exec("COMMIT", 0))
		rkRead(s, true)
		for _, reset := range []string{"SET " + rkSetting + "=DEFAULT", "RESET " + rkSetting, "RESET ALL"} {
			rkSet(s, "view")
			s.run(fixture.Exec(reset, 0))
			rkRead(s, false)
		}
		// A physical connection retains its policy on ordinary pool reuse.
		pool.SetMaxIdleConns(0)
		other.conn.Close()
		pool.SetMaxIdleConns(1)
		rkSet(s, "view")
		identity := fixture.SettingsPhysical(s.t, s.conn)
		if err := s.conn.Close(); err != nil {
			s.t.Fatal(err)
		}
		s.conn, err = pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer s.conn.Close()
		if fixture.SettingsPhysical(s.t, s.conn) != identity {
			s.t.Fatal("physical session was replaced")
		}
		rkRead(s, true)
		s.run(fixture.Exec("RESET "+rkSetting, 0))
		rkRead(s, false)
	})
}

func TestReferenceRestrictRelationKindCapturedDefaults(t *testing.T) {
	physicalCases(t, []string{"rk_default"}, func(s *physicalCase) {
		rkSeed(s)
		s.run(fixture.Exec("CREATE ROLE rk_default LOGIN PASSWORD 'role-core-fixture'", 0))
		s.run(fixture.Exec("GRANT USAGE ON SCHEMA public TO rk_default", 0))
		s.run(fixture.Exec("GRANT SELECT ON rk_v TO rk_default", 0))
		s.run(fixture.Exec("ALTER ROLE rk_default SET "+rkSetting+"='view'", 0))
		captured := s.login("rk_default")
		// The first SQL access sees the captured nonempty typed default.
		rkRead(captured, true)
		captured.run(clientText("SHOW "+rkSetting, rkSetting, "view"))
		captured.run(fixture.Select("SELECT setting,reset_val,source FROM pg_settings WHERE name='"+rkSetting+"'", []string{"setting", "reset_val", "source"}, []string{"TEXT", "TEXT", "TEXT"}, [][]any{{"view", "view", "user"}}))
		for _, reset := range []string{"SET " + rkSetting + "=DEFAULT", "RESET " + rkSetting, "RESET ALL"} {
			rkSet(captured, "")
			rkRead(captured, false)
			captured.run(fixture.Exec(reset, 0))
			rkRead(captured, true)
		}
		s.run(fixture.Exec("ALTER ROLE rk_default RESET "+rkSetting, 0))
		// Existing captured defaults are immutable across later role changes.
		rkSet(captured, "")
		captured.run(fixture.Exec("RESET "+rkSetting, 0))
		rkRead(captured, true)
		fresh := s.login("rk_default")
		rkRead(fresh, false)
	})
}
