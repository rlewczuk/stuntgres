package reference_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDeveloperSettingsLifecycle(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
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
		for _, prepared := range []bool{false, true} {
			for _, tc := range developerSettings {
				if tc.name != "trace_sort" && tc.name != "backtrace_functions" {
					continue
				}
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SAVEPOINT developer_save", prepared)
				s.developerExec("SET LOCAL "+tc.name+"="+physicalLiteral(tc.initial), prepared)
				s.developerRead(tc, tc.initial, prepared)
				s.developerExec("ROLLBACK TO developer_save", prepared)
				s.developerRead(tc, tc.alternate, prepared)
				s.developerExec("COMMIT", prepared)
				s.developerRead(tc, tc.alternate, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.initial), prepared)
				s.developerExec("ROLLBACK", prepared)
				s.developerRead(tc, tc.alternate, prepared)
				s.developerExec("BEGIN", prepared)
				invalid := "broken"
				if tc.kind == "string" {
					invalid = "foo-bar"
				}
				s.run(physicalError("SET "+tc.name+"="+physicalLiteral(invalid), developerInvalid(tc, invalid), prepared, false))
				s.developerExec("ROLLBACK", prepared)
				s.developerRead(tc, tc.alternate, prepared)
				other.developerRead(tc, tc.initial, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("RESET ALL", prepared)
				s.developerRead(tc, tc.initial, prepared)
				s.developerExec("ROLLBACK", prepared)
				s.developerRead(tc, tc.alternate, prepared)
				s.developerExec("RESET "+tc.name, prepared)
				s.developerRead(tc, tc.initial, prepared)
			}
		}
		// Close the peer before constraining the pool; keep only the owned borrower.
		peer.Close()
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pool.SetMaxIdleConns(0)
		pool.SetMaxIdleConns(1)
		for _, tc := range developerSettings {
			if developerMutable(tc) {
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), false)
			}
		}
		before := fixture.SettingsPhysical(t, s.conn)
		s.conn.Close()
		conn, err := pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer conn.Close()
		s.conn = conn
		if fixture.SettingsPhysical(t, conn) != before {
			s.t.Fatal("physical session not reused")
		}
		for _, tc := range developerSettings {
			if developerMutable(tc) {
				s.developerRead(tc, tc.alternate, true)
			}
		}
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
			s.t.Fatal(err)
		}
		conn.Close()
		replacement, err := pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer replacement.Close()
		s.conn = replacement
		if fixture.SettingsPhysical(t, replacement) == before {
			s.t.Fatal("physical session not replaced")
		}
		for _, tc := range developerSettings {
			s.developerRead(tc, tc.initial, true)
			s.developerMetadata(tc, tc.initial, "default", true)
		}
	})
}
