package reference_test

import (
	"database/sql/driver"
	"errors"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceLoggingSettingsLifecycle(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		t := s.t
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
			for _, tc := range loggingSettings {
				if tc.name != "debug_print_parse" && tc.name != "log_statement" && tc.name != "log_min_duration_statement" && tc.name != "log_parameter_max_length_on_error" && tc.name != "log_temp_files" && tc.name != "log_transaction_sample_rate" && tc.name != "application_name" {
					continue
				}
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.initial), prepared)
				s.developerExec("COMMIT", prepared)
				s.loggingRead(tc, tc.initial, prepared)
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.alternate), prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SAVEPOINT logging_save", prepared)
				s.developerExec("SET LOCAL "+tc.name+"="+physicalLiteral(tc.initial), prepared)
				s.loggingRead(tc, tc.initial, prepared)
				s.developerExec("ROLLBACK TO logging_save", prepared)
				s.loggingRead(tc, tc.show, prepared)
				s.developerExec("COMMIT", prepared)
				s.loggingRead(tc, tc.show, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SET "+tc.name+"="+physicalLiteral(tc.initial), prepared)
				s.developerExec("ROLLBACK", prepared)
				s.loggingRead(tc, tc.show, prepared)
				if tc.kind != "string" {
					s.developerExec("BEGIN", prepared)
					invalid := "broken"
					s.run(physicalError("SET "+tc.name+"="+physicalLiteral(invalid), loggingInvalid(tc, invalid), prepared, false))
					s.developerExec("ROLLBACK", prepared)
					s.loggingRead(tc, tc.show, prepared)
				}
				other.loggingRead(tc, tc.initial, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("SET LOCAL "+tc.name+"="+physicalLiteral(tc.initial), prepared)
				s.developerExec("COMMIT", prepared)
				s.loggingRead(tc, tc.show, prepared)
				s.developerExec("BEGIN", prepared)
				s.developerExec("RESET ALL", prepared)
				s.loggingRead(tc, tc.initial, prepared)
				s.developerExec("ROLLBACK", prepared)
				s.loggingRead(tc, tc.show, prepared)
				s.developerExec("RESET "+tc.name, prepared)
				s.loggingRead(tc, tc.initial, prepared)
			}
		}
		// Close the peer before constraining the pool; keep only the owned borrower.
		peer.Close()
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		pool.SetMaxIdleConns(0)
		pool.SetMaxIdleConns(1)
		for _, tc := range loggingSettings {
			if loggingMutable(tc) {
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
		for _, tc := range loggingSettings {
			if loggingMutable(tc) {
				s.loggingRead(tc, tc.show, true)
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
		for _, tc := range loggingSettings {
			initial, raw, source := s.loggingInitial(tc)
			s.loggingRead(tc, initial, true)
			s.loggingMetadata(tc, raw, source, true)
		}
	})
}
