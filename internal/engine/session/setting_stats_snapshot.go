package session

import "stuntgres/internal/engine/statistics"

// StatsChanged runs under Settings.Mu or exclusive detached-image ownership.
func (s *SettingsStore) StatsChanged(before string) {
	if before != s.Value("stats_fetch_consistency") {
		s.statsAuthority().StatsGeneration.Add(1)
	}
}
func (s *SettingsStore) statsAuthority() *SettingsStore {
	if s.statsOwner != nil {
		return s.statsOwner
	}
	return s
}
func (s *SettingsStore) StatsConfiguration() (string, uint64, statistics.Policy) {
	s.Mu.Lock()
	defer s.Mu.Unlock()
	return s.Value("stats_fetch_consistency"), s.statsAuthority().StatsGeneration.Load(), statistics.Policy{
		Counts: s.TypedValue("track_counts").Boolean, Activities: s.TypedValue("track_activities").Boolean,
		Functions: s.TypedValue("track_functions").Text, IOTiming: s.TypedValue("track_io_timing").Boolean, WALTiming: s.TypedValue("track_wal_io_timing").Boolean,
	}
}
