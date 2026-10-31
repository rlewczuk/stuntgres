package enginetests_test

import (
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/settings"
	"stuntgres/internal/engine/statistics"
	"sync"
	"testing"
	"time"
)

func TestStatsSnapshotConsistencyAuthority(t *testing.T) {
	registry := settings.NewRegistry(nil, false)
	fresh := func() *session.SettingsStore {
		s := &session.SettingsStore{Values: make(map[string]string)}
		s.InitializeShared(registry.SessionDefaults())
		return s
	}
	assign := func(s *session.SettingsStore, mode string) {
		s.Mu.Lock()
		defer s.Mu.Unlock()
		before := s.Value("stats_fetch_consistency")
		s.Touch("stats_fetch_consistency", session.SettingSet)
		s.Values["stats_fetch_consistency"], s.Typed["stats_fetch_consistency"] = mode, settings.Value{Text: mode}
		s.StatsChanged(before)
	}
	root := fresh()
	assign(root, "snapshot")
	mode, generation, _ := root.StatsConfiguration()
	if generation != 1 {
		t.Fatal("initial authority", generation)
	}
	var provider statistics.Provider
	var reader statistics.Reader
	reader.Configure(mode, generation)
	now := time.Unix(100, 0)
	reader.Read(&provider, statistics.Key{}, now)
	physical := &session.Session{Settings: root}
	childFrame := &session.RestrictedInvocation{}
	op := &session.Operation{Session: physical, Restricted: childFrame}
	child := op.EffectiveSettings()
	mode, generation, _ = child.StatsConfiguration()
	reader.Configure(mode, generation)
	if reader.Timestamp() != now {
		t.Fatal("unchanged detached entry invalidated reader")
	}
	childMark := op.CaptureInvocation()
	assign(child, "none")
	nestedFrame := &session.RestrictedInvocation{Parent: childFrame}
	op.Restricted = nestedFrame
	nested := op.EffectiveSettings()
	assign(nested, "snapshot")
	mode, generation, _ = root.StatsConfiguration()
	if mode != "snapshot" || generation != 3 {
		t.Fatal("nested clone copied/reset authority or changed root values", mode, generation)
	}
	// No statistics access occurred between the two effective mode changes.
	reader.Configure(mode, generation)
	if !reader.Timestamp().IsZero() {
		t.Fatal("detached round trip retained capture")
	}
	var writer statistics.Writer
	writer.Add(&provider, statistics.Key{}, statistics.Fact{Inserts: 2})
	writer.Flush(&provider, now.Add(time.Second), true)
	if fact := reader.Read(&provider, statistics.Key{}, now.Add(time.Second)); fact.Inserts != 2 {
		t.Fatal("invalidated reader missed newer published fact", fact)
	}
	nestedMark := op.CaptureInvocation()
	assign(nested, "none")
	nestedMark.Restore()
	mode, generation, _ = op.EffectiveSettings().StatsConfiguration()
	if mode != "snapshot" || generation != 5 {
		t.Fatal("checkpoint restoration omitted effective generation", mode, generation)
	}
	op.Restricted = childFrame
	childMark.Restore()
	mode, generation, _ = root.StatsConfiguration()
	if mode != "snapshot" || generation != 6 {
		t.Fatal("original checkpoint lost authority", mode, generation)
	}
	if fact := provider.Read(statistics.Key{}); fact.Inserts != 2 {
		t.Fatal("settings restoration reset shared counters", fact)
	}
	other := fresh()
	_, independent, _ := other.StatsConfiguration()
	if independent != 0 {
		t.Fatal("independent physical session borrowed generation")
	}
	// Detached images have separate map locks, but one atomic physical marker.
	// Exercise that ownership without concurrent mutation of an invocation frame.
	clones := make([]*session.SettingsStore, 2)
	for i := range clones {
		invocation := &session.Operation{Session: physical, Restricted: &session.RestrictedInvocation{}}
		clones[i] = invocation.EffectiveSettings()
	}
	var workers sync.WaitGroup
	for _, clone := range clones {
		workers.Go(func() {
			for range 100 {
				assign(clone, "none")
				assign(clone, "snapshot")
			}
		})
	}
	workers.Wait()
	mode, generation, _ = root.StatsConfiguration()
	if mode != "snapshot" || generation != 406 {
		t.Fatal("lost detached effective changes", mode, generation)
	}
	_, independent, _ = other.StatsConfiguration()
	if independent != 0 {
		t.Fatal("authority escaped physical session")
	}
}
