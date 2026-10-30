package reference_test

import (
	"database/sql/driver"
	"errors"
	"fmt"
	"testing"

	"stuntgres/reference/fixture"
)

func TestReferenceDefaultTableAccessMethodLifecycle(t *testing.T) {
	for _, prepared := range []bool{false, true} {
		t.Run(fmt.Sprintf("prepared=%v", prepared), func(t *testing.T) {
			defaultAMCases(t, nil, func(s *physicalCase) {
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
				defaultAMSet(s, "settings_heap", prepared)
				defaultAMWitness(s, "dam_isolated_a", "settings_heap", prepared)
				defaultAMWitness(other, "dam_isolated_b", "heap", prepared)
				defaultAMRun(s, prepared, fixture.Exec("RESET default_table_access_method", 0))
				cases := []struct {
					name          string
					commands      []string
					inside, after string
				}{
					{"session_rollback", []string{"SET SESSION default_table_access_method='settings_heap'"}, "settings_heap", "heap"},
					{"session_commit", []string{"SET SESSION default_table_access_method='settings_heap'"}, "settings_heap", "settings_heap"},
					{"local_commit", []string{"SET LOCAL default_table_access_method='settings_heap'"}, "settings_heap", "heap"},
					{"local_rollback", []string{"SET LOCAL default_table_access_method='settings_heap'"}, "settings_heap", "heap"},
					{"save_restore", []string{"SET SESSION default_table_access_method='settings_heap'", "SAVEPOINT dam_save", "SET LOCAL default_table_access_method='heap'", "ROLLBACK TO dam_save"}, "settings_heap", "heap"},
					{"masked_commit", []string{"SET SESSION default_table_access_method='settings_heap'", "SET LOCAL default_table_access_method='heap'"}, "heap", "settings_heap"},
					{"save_commit", []string{"SAVEPOINT dam_save", "SET SESSION default_table_access_method='settings_heap'", "ROLLBACK TO dam_save"}, "heap", "heap"},
				}
				for _, tc := range cases {
					defaultAMRun(s, prepared, fixture.Exec("BEGIN", 0))
					for _, q := range tc.commands {
						defaultAMRun(s, prepared, fixture.Exec(q, 0))
						if q == "SET SESSION default_table_access_method='settings_heap'" {
							defaultAMWitness(s, "dam_session_"+tc.name, "settings_heap", prepared)
						}
						if q == "SET LOCAL default_table_access_method='heap'" {
							defaultAMWitness(s, "dam_local_"+tc.name, "heap", prepared)
						}
					}
					private := "dam_inside_" + tc.name
					defaultAMWitness(s, private, tc.inside, prepared)
					other.clientReads(defaultAMSetting, "heap", prepared)
					end := "COMMIT"
					if tc.name == "session_rollback" || tc.name == "local_rollback" || tc.name == "save_restore" {
						end = "ROLLBACK"
					}
					defaultAMRun(s, prepared, fixture.Exec(end, 0))
					if end == "ROLLBACK" {
						defaultAMRun(s, prepared, defaultAMAbsent(private))
					}
					defaultAMWitness(s, "dam_after_"+tc.name, tc.after, prepared)
					defaultAMWitness(other, "dam_peer_"+tc.name, "heap", prepared)
					defaultAMRun(s, prepared, fixture.Exec("RESET default_table_access_method", 0))
				}
			})
		})
	}
	t.Run("physical-reuse", func(t *testing.T) {
		defaultAMCases(t, nil, func(s *physicalCase) {
			pool := s.p.Memory
			if s.pg {
				pool = s.p.Postgres
			}
			pool.SetMaxOpenConns(1)
			pool.SetMaxIdleConns(1)
			defaultAMSet(s, "settings_heap", false)
			defaultAMWitness(s, "dam_committed", "settings_heap", false)
			before := fixture.SettingsPhysical(s.t, s.conn)
			if err := s.conn.Close(); err != nil {
				s.t.Fatal(err)
			}
			c, err := pool.Conn(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer c.Close()
			s.conn = c
			if fixture.SettingsPhysical(s.t, c) != before {
				s.t.Fatal("physical owner not reused")
			}
			defaultAMWitness(s, "dam_reused", "settings_heap", true)
			if err := c.Raw(func(any) error { return driver.ErrBadConn }); !errors.Is(err, driver.ErrBadConn) {
				s.t.Fatal(err)
			}
			c.Close()
			replacement, err := pool.Conn(s.p.Ctx)
			if err != nil {
				s.t.Fatal(err)
			}
			defer replacement.Close()
			s.conn = replacement
			if fixture.SettingsPhysical(s.t, replacement) == before {
				s.t.Fatal("physical owner not replaced")
			}
			defaultAMWitness(s, "dam_replaced", "heap", true)
			for _, name := range []string{"dam_committed", "dam_reused"} {
				s.run(defaultAMIdentity(name, "settings_heap"))
				s.run(defaultAMRows(name, defaultAMKeptRows()))
			}
		})
	})
}
