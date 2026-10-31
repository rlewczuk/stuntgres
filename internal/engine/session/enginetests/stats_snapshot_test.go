package enginetests_test

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"testing"
)

func TestStatsSnapshotSettingsGeneration(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	fresh := func() *session.SettingsStore {
		s := &session.SettingsStore{Values: make(map[string]string)}
		s.InitializeShared(registry.SessionDefaults())
		return s
	}
	assign := func(s *session.SettingsStore, mode string, action session.SettingAction) {
		before := s.Value("stats_fetch_consistency")
		s.Touch("stats_fetch_consistency", action)
		s.Values["stats_fetch_consistency"], s.Typed["stats_fetch_consistency"] = mode, settings.Value{Text: mode}
		s.StatsChanged(before)
	}
	for _, terminal := range []string{"reset", "rollback", "local_commit", "save", "savepoint"} {
		t.Run(terminal, func(t *testing.T) {
			s := fresh()
			assign(s, "snapshot", session.SettingSet)
			s.FinishTransaction(true)
			_, generation, _ := s.StatsConfiguration()
			if generation != 1 {
				t.Fatal("initial effective generation", generation)
			}
			switch terminal {
			case "reset":
				s.ResetAllLocked(registry)
			case "rollback":
				assign(s, "none", session.SettingSet)
				s.FinishTransaction(false)
			case "local_commit":
				assign(s, "none", session.SettingLocal)
				s.FinishTransaction(true)
			case "save":
				level := s.BeginSettingsSaveLocked()
				assign(s, "none", session.SettingSave)
				s.EndSettingsSaveLocked(level, true)
			case "savepoint":
				mark := s.Savepoint()
				assign(s, "none", session.SettingLocal)
				s.RestoreSavepointLocked(mark.Clone(), authorization.RoleState{})
			}
			mode, after, _ := s.StatsConfiguration()
			want := uint64(3)
			if terminal == "reset" {
				want = 2
				if mode != "cache" {
					t.Fatal("reset did not restore default")
				}
			} else if mode != "snapshot" {
				t.Fatal("restoration lost original mode", mode)
			}
			if after != want {
				t.Fatal("effective round trip lost generation", terminal, after, want)
			}
			assign(s, mode, session.SettingSet)
			_, same, _ := s.StatsConfiguration()
			if same != after {
				t.Fatal("same assignment invalidated reader")
			}
		})
	}
}
