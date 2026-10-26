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

func TestReferenceLockSettingsLifecycle(t *testing.T) {
	t.Run("actions", func(t *testing.T) {
		physicalCases(t, nil, func(s *physicalCase) {
			peer := s.lockPeer()
			query := "SELECT current_setting('deadlock_timeout')"
			held, err := s.conn.PrepareContext(s.p.Ctx, query)
			if err != nil { s.t.Fatal(err) }
			defer held.Close()
			check := func(want string) {
				s.lockReads(false, want)
				fixture.RunStep(s.t, s.p.Ctx, fixture.HeldStatement{Executor:s.conn, Statement:held, SQL:query}, s.recorder, clientText(query, "current_setting", want))
				peer.lockReads(true, "1s")
			}
			for _, tc := range []struct{ commands []string; want string }{
				{[]string{"BEGIN", "SET deadlock_timeout='2s'", "ROLLBACK"}, "1s"},
				{[]string{"BEGIN", "SET deadlock_timeout='2s'", "COMMIT"}, "2s"},
				{[]string{"RESET deadlock_timeout", "BEGIN", "SET LOCAL deadlock_timeout='3s'"}, "3s"},
				{[]string{"COMMIT"}, "1s"},
				{[]string{"BEGIN", "SET LOCAL deadlock_timeout='3s'", "ROLLBACK"}, "1s"},
				{[]string{"BEGIN", "SET deadlock_timeout='2s'", "SET LOCAL deadlock_timeout='3s'"}, "3s"},
				{[]string{"COMMIT"}, "2s"},
			} {
				for _, command := range tc.commands { s.run(fixture.Exec(command, 0)) }
				check(tc.want)
			}
			for _, action := range []string{"SET LOCAL deadlock_timeout='3s'", "SET deadlock_timeout='3s'", "RESET deadlock_timeout"} {
				s.run(fixture.Exec("BEGIN", 0))
				s.run(fixture.Exec("SAVEPOINT lock_lifecycle_save", 0))
				s.run(fixture.Exec(action, 0))
				s.run(fixture.Exec("ROLLBACK TO lock_lifecycle_save", 0))
				check("2s")
				s.run(fixture.Exec("RELEASE lock_lifecycle_save", 0))
				s.run(fixture.Exec("COMMIT", 0))
				check("2s")
			}
			s.run(fixture.Exec("BEGIN", 0))
			s.run(fixture.Exec("SET deadlock_timeout='2s'", 0))
			s.run(fixture.Exec("SAVEPOINT lock_release_save", 0))
			s.run(fixture.Exec("SET LOCAL deadlock_timeout='3s'", 0))
			s.run(fixture.Exec("RELEASE lock_release_save", 0))
			check("3s")
			s.run(fixture.Exec("COMMIT", 0))
			check("2s")
			for _, prepared := range []bool{false, true} {
				for _, tc := range []struct{ local any; after string }{{false, "3s"}, {true, "2s"}, {nil, "3s"}} {
					s.run(fixture.Exec("SET deadlock_timeout='2s'", 0))
					s.run(fixture.Exec("BEGIN", 0))
					step := fixture.Select("SELECT set_config('deadlock_timeout',$1,$2)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"3s"}}, "3s", tc.local)
					step.Prepared = prepared
					s.run(step)
					check("3s")
					s.run(fixture.Exec("COMMIT", 0))
					check(tc.after)
					step = fixture.Select("SELECT set_config('deadlock_timeout',$1,false)", []string{"set_config"}, []string{"TEXT"}, [][]any{{"1s"}}, nil)
					step.Prepared = prepared
					s.run(step)
					check("1s")
				}
			}
		})
	})
	t.Run("physical-controls", lockSettingsControls)
}

func lockSettingsControls(t *testing.T) {
	physicalCases(t, nil, func(s *physicalCase) {
		pool := s.p.Memory
		if s.pg { pool = s.p.Postgres }
		pool.SetMaxOpenConns(1)
		pool.SetMaxIdleConns(1)
		s.run(fixture.Exec("CREATE TABLE lock_settings_sentinel(v integer)", 0))
		s.run(fixture.Exec("INSERT INTO lock_settings_sentinel VALUES(7)", 1))
		s.run(fixture.Exec("SET deadlock_timeout='2s'", 0))
		before := fixture.SettingsPhysical(s.t, s.conn)
		if err := s.conn.Close(); err != nil { s.t.Fatal(err) }
		conn, err := pool.Conn(s.p.Ctx)
		if err != nil { s.t.Fatal(err) }
		defer conn.Close()
		s.conn = conn
		if fixture.SettingsPhysical(s.t, conn) != before { s.t.Fatal("physical session was not reused") }
		s.lockReads(true, "2s")
		if !s.pg {
			rows, err := conn.QueryContext(s.p.Ctx, "SELECT v FROM lock_settings_sentinel")
			if err != nil { s.t.Fatal(err) }
			defer rows.Close()
			metadata, err := conn.QueryContext(s.p.Ctx, "SELECT setting FROM pg_settings WHERE name='deadlock_timeout'")
			if err != nil { s.t.Fatal(err) }
			defer metadata.Close()
			var dump bytes.Buffer
			if err := stuntgres.Dump(s.p.Ctx, s.p.Native, &dump); err != nil { s.t.Fatal(err) }
			if strings.Contains(dump.String(), "deadlock_timeout") { s.t.Fatal("Dump serialized live settings") }
			if err := stuntgres.Reset(s.p.Ctx, s.p.Native); err != nil { s.t.Fatal(err) }
			s.lockReads(false, "2s")
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, bytes.NewReader(dump.Bytes())); err != nil { s.t.Fatal(err) }
			s.lockReads(false, "2s")
			if err := stuntgres.Restore(s.p.Ctx, s.p.Native, strings.NewReader("CREATE TABLE broken(")); err == nil { s.t.Fatal("invalid Restore succeeded") }
			s.lockReads(false, "2s")
			s.lockInventory(true, "2000", "session")
			if !rows.Next() { s.t.Fatal("retained sentinel missing") }
			var v int
			if err := rows.Scan(&v); err != nil || v != 7 { s.t.Fatal(v, err) }
			if rows.Next() || rows.Err() != nil { s.t.Fatal("retained sentinel cardinality", rows.Err()) }
			rows.Close()
			if !metadata.Next() { s.t.Fatal("retained settings missing") }
			var raw string
			if err := metadata.Scan(&raw); err != nil || raw != "2000" { s.t.Fatal(raw, err) }
			if metadata.Next() || metadata.Err() != nil { s.t.Fatal("retained settings cardinality", metadata.Err()) }
			metadata.Close()
		}
		s.lockSentinel()
		if err := conn.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) { s.t.Fatal(err) }
		conn.Close()
		replacement, err := pool.Conn(s.p.Ctx)
		if err != nil { s.t.Fatal(err) }
		defer replacement.Close()
		s.conn = replacement
		if fixture.SettingsPhysical(s.t, replacement) == before { s.t.Fatal("physical session was not replaced") }
		s.lockInventory(true, "1000", "default")
		s.lockReads(true, "1s")
		s.lockSentinel()
	})
}
