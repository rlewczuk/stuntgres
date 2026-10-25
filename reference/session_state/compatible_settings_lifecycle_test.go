package reference_test

import (
	"bytes"
	"database/sql/driver"
	"errors"
	"strings"
	"testing"

	"stuntgres"
	"stuntgres/reference/fixture"
)

func TestReferenceCompatibleSettingsLifecycle(t *testing.T) {
	for _, tc := range compatibleSettings[1:] {
		t.Run(tc.name, func(t *testing.T) {
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
					exec := func(query string) { step := fixture.Exec(query, 0); step.Prepared = prepared; s.run(step) }
					s.compatibleReads(tc, tc.initial, prepared)
					for _, end := range []string{"COMMIT", "ROLLBACK"} {
						exec("BEGIN")
						exec("SET LOCAL " + tc.name + "=" + physicalLiteral(tc.alternate))
						s.compatibleReads(tc, tc.alternate, prepared)
						other.compatibleReads(tc, tc.initial, prepared)
						exec(end)
						s.compatibleReads(tc, tc.initial, prepared)
						exec("BEGIN")
						step := clientText("SELECT set_config($1,$2,true)", "set_config", tc.alternate)
						step.Args, step.Prepared = []any{tc.name, tc.alternate}, prepared
						s.run(step)
						s.compatibleReads(tc, tc.alternate, prepared)
						exec(end)
						s.compatibleReads(tc, tc.initial, prepared)
					}
					exec("BEGIN")
					exec("SET " + tc.name + "=" + physicalLiteral(tc.alternate))
					exec("SAVEPOINT compatible_save")
					exec("SET LOCAL " + tc.name + "=DEFAULT")
					s.compatibleReads(tc, tc.initial, prepared)
					exec("ROLLBACK TO compatible_save")
					s.compatibleReads(tc, tc.alternate, prepared)
					exec("ROLLBACK")
					s.compatibleReads(tc, tc.initial, prepared)
					exec("BEGIN")
					exec("SET " + tc.name + "=" + physicalLiteral(tc.alternate))
					exec("COMMIT")
					s.compatibleReads(tc, tc.alternate, prepared)
					exec("BEGIN")
					exec("RESET ALL")
					s.compatibleReads(tc, tc.initial, prepared)
					exec("ROLLBACK")
					s.compatibleReads(tc, tc.alternate, prepared)
					other.compatibleReads(tc, tc.initial, prepared)
					exec("RESET ALL")
				}
			})
		})
	}
	t.Run("physical-reuse-and-controls", compatibleSettingsControls)
}

func compatibleSettingsControls(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		pool := s.p.Memory
		if s.pg {
			pool = s.p.Postgres
		}
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		s.run(fixture.Exec("CREATE TABLE compatible_lifecycle(v integer)", 0))
		s.run(fixture.Exec("INSERT INTO compatible_lifecycle VALUES(1),(2),(3)", 3))
		for _, tc := range compatibleSettings[1:] {
			s.run(fixture.Exec("SET "+tc.name+"="+physicalLiteral(tc.alternate), 0))
		}
		before := fixture.SettingsPhysical(t, s.conn)
		if err := s.conn.Close(); err != nil {
			s.t.Fatal(err)
		}
		conn, err := pool.Conn(s.p.Ctx)
		if err != nil {
			s.t.Fatal(err)
		}
		defer conn.Close()
		s.conn = conn
		if fixture.SettingsPhysical(t, conn) != before {
			s.t.Fatal("physical session not reused")
		}
		for _, tc := range compatibleSettings[1:] {
			s.compatibleReads(tc, tc.alternate, true)
		}
		if !s.pg {
			pool.SetMaxOpenConns(2)
			peer, err := pool.Conn(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer peer.Close()
			other := &physicalCase{s.t, s.p, peer, s.pg, s.recorder}
			for _, tc := range compatibleSettings {
				other.compatibleReads(tc, tc.initial, false)
			}
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil {
				s.t.Fatal(err)
			}
			for _, tc := range compatibleSettings {
				if strings.Contains(dump.String(), tc.name) {
					s.t.Fatal("Dump serialized setting", tc.name)
				}
			}
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil {
				s.t.Fatal(err)
			}
			for _, tc := range compatibleSettings[1:] {
				s.compatibleReads(tc, tc.alternate, false)
			}
			for _, tc := range compatibleSettings {
				other.compatibleReads(tc, tc.initial, false)
			}
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil {
				s.t.Fatal(err)
			}
			for _, tc := range compatibleSettings[1:] {
				s.compatibleReads(tc, tc.alternate, true)
			}
			for _, tc := range compatibleSettings {
				other.compatibleReads(tc, tc.initial, true)
			}
			if err := peer.Close(); err != nil {
				s.t.Fatal(err)
			}
			pool.SetMaxOpenConns(1)
		}
		s.compatibleSentinel("compatible_lifecycle", true)
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
		for _, tc := range compatibleSettings {
			s.compatibleReads(tc, tc.initial, true)
		}
		s.compatibleSentinel("compatible_lifecycle", true)
	})
}
