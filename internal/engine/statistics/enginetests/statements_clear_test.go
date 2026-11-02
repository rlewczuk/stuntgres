package statistics_test

import (
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

// Seed a real deallocation, then leave room for both matching top-level
// dimensions and peers. No expectation depends on tied eviction order.
func statementClearStore(t *testing.T, now time.Time) *statistics.Statements {
	t.Helper()
	store := statistics.NewStatements(8, now)
	for i := int64(100); i < 108; i++ {
		token := store.Begin(statementKey(9, i), statementKey(9, i), "pressure", now)
		defer store.Release(token)
		store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(9, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
	}
	store.Release(store.Begin(statementKey(1, 1), statementKey(1, 1), "target", now.Add(time.Second)))
	if store.Info().Dealloc != 1 || len(store.Snapshot()) != 0 {
		t.Fatal("deallocation setup", store.Info(), store.Snapshot())
	}
	return store
}

func statementClearSample(store *statistics.Statements, key statistics.StatementKey, now time.Time) statistics.StatementToken {
	token := store.Begin(key, key, "target", now)
	store.RecordPlan(token, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 2, Rows: 0})
	store.RecordPlan(token, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 4, Rows: 0})
	store.RecordExecution(token, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 2, Rows: 2})
	store.RecordExecution(token, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 4, Rows: 1})
	return token
}

func statementClearSnapshot(store *statistics.Statements) map[statistics.StatementKey]statistics.StatementEntry {
	result := make(map[statistics.StatementKey]statistics.StatementEntry)
	for _, entry := range store.Snapshot() {
		result[entry.Key] = entry
	}
	return result
}

func TestPgStatStatementsClearFilteredAllMetadata(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter statistics.StatementFilter
	}{
		{"user", statistics.StatementFilter{UserID: 10}},
		{"database", statistics.StatementFilter{DatabaseID: 1}},
		{"query", statistics.StatementFilter{QueryID: 1}},
		{"combined", statistics.StatementFilter{UserID: 10, DatabaseID: 1, QueryID: 1}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statementClearStore(t, now)
			key := statementKey(1, 1)
			store.Release(statementClearSample(store, key, now.Add(time.Second)))
			key.TopLevel = false
			store.Release(statementClearSample(store, key, now.Add(time.Second)))
			held := store.Snapshot()
			saved := append([]statistics.StatementEntry(nil), held...)
			later := now.Add(2 * time.Second)
			if n := store.Clear(tc.filter, false, later); n != 2 {
				t.Fatal("filter did not remove both top-level dimensions", n)
			}
			if len(store.Snapshot()) != 0 || store.Info() != (statistics.StatementInfo{StatsReset: later}) {
				t.Fatal("filtered all-removal retained global metadata", store.Snapshot(), store.Info())
			}
			if !reflect.DeepEqual(held, saved) {
				t.Fatal("clear mutated held snapshots", held, saved)
			}
		})
	}
}

func TestPgStatStatementsClearCombinedPeers(t *testing.T) {
	for _, minmax := range []bool{false, true} {
		name := "remove"
		if minmax {
			name = "minmax"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statementClearStore(t, now)
			keys := []statistics.StatementKey{
				{UserID: 10, DatabaseID: 1, QueryID: 1, TopLevel: true},
				{UserID: 10, DatabaseID: 1, QueryID: 1, TopLevel: false},
				{UserID: 11, DatabaseID: 1, QueryID: 1, TopLevel: true},
				{UserID: 10, DatabaseID: 2, QueryID: 1, TopLevel: true},
				{UserID: 10, DatabaseID: 1, QueryID: 2, TopLevel: false},
			}
			tokens := make([]statistics.StatementToken, 0, len(keys))
			for _, key := range keys {
				token := statementClearSample(store, key, now.Add(time.Second))
				defer store.Release(token)
				tokens = append(tokens, token)
			}
			before := statementClearSnapshot(store)
			held := store.Snapshot()
			saved := append([]statistics.StatementEntry(nil), held...)
			info := store.Info()
			later := now.Add(2 * time.Second)
			filter := statistics.StatementFilter{UserID: 10, DatabaseID: 1, QueryID: 1}
			if n := store.Clear(filter, minmax, later); n != 2 {
				t.Fatal("combined filter did not select exactly both top-level dimensions", n)
			}
			after := statementClearSnapshot(store)
			wantCount := 3
			if minmax {
				wantCount = 5
			}
			if len(after) != wantCount || store.Info() != info {
				t.Fatal("peer-preserving clear changed global metadata", after, store.Info(), info)
			}
			for i, key := range keys {
				old := before[key]
				current, present := after[key]
				if i >= 2 {
					if !present || !reflect.DeepEqual(current, old) {
						t.Fatal("combined filter touched a one-dimension-mismatching peer", key, current, old)
					}
					continue
				}
				if !minmax {
					if present {
						t.Fatal("matching entry survived removal", current)
					}
					continue
				}
				if !present || current.Key != old.Key || current.Query != old.Query || current.Rows != 3 || current.StatsSince != old.StatsSince || current.MinMaxStatsSince != later {
					t.Fatal("minmax changed identity/history timestamps", current, old)
				}
				for _, moments := range []statistics.StatementMoments{current.Planning, current.Execution} {
					if moments.Count != 2 || moments.Total != 6 || moments.Mean != 3 || moments.Stddev() != 1 || moments.Min != 0 || moments.Max != 0 {
						t.Fatal("minmax changed cumulative moments", moments)
					}
				}
			}
			// Already admitted matching work contributes totals only after minmax;
			// a destructive clear fences it entirely. Peers remain eligible.
			for i, token := range tokens {
				store.RecordPlan(token, statistics.StatementRecord{Key: keys[i], Query: "successful raw", At: now, Milliseconds: 10, Rows: 0})
				store.RecordExecution(token, statistics.StatementRecord{Key: keys[i], Query: "successful raw", At: now, Milliseconds: 10, Rows: 4})
				if i < 2 && !minmax {
					continue
				}
				entry := statementEntryFor(t, store, keys[i])
				for _, moments := range []statistics.StatementMoments{entry.Planning, entry.Execution} {
					if moments.Count != 3 || moments.Total != 16 || (i < 2 && (moments.Min != 0 || moments.Max != 0)) || (i >= 2 && (moments.Min != 2 || moments.Max != 10)) {
						t.Fatal("wrong admitted-work fence", keys[i], moments)
					}
				}
				if entry.Rows != 7 {
					t.Fatal("wrong admitted rows", entry)
				}
			}
			if len(store.Snapshot()) != wantCount || store.Info() != info {
				t.Fatal("late work resurrected cleared history or changed global info", store.Snapshot(), store.Info())
			}
			if !reflect.DeepEqual(held, saved) {
				t.Fatal("clear or late publication mutated owned snapshots", held, saved)
			}
		})
	}
}

func TestPgStatStatementsClearEmptyMetadata(t *testing.T) {
	for _, minmax := range []bool{false, true} {
		for _, filter := range []statistics.StatementFilter{{}, {UserID: 10, DatabaseID: 1, QueryID: 1}} {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			old := store.Begin(statementKey(1, 1), statementKey(1, 1), "old", now)
			defer store.Release(old)
			store.RecordExecution(old, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
			// Evict to leave a nonzero dealloc baseline, then remove the sole
			// remaining entry. The final empty reset's0==0 edge is independent
			// of whether the preceding filtered clear reset global metadata.
			store.Release(store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now))
			store.Clear(statistics.StatementFilter{DatabaseID: 2}, false, now.Add(time.Second))
			later := now.Add(2 * time.Second)
			if n := store.Clear(filter, minmax, later); n != 0 {
				t.Fatal("empty clear matched entries", filter, minmax, n)
			}
			if store.Info() != (statistics.StatementInfo{StatsReset: later}) || len(store.Snapshot()) != 0 {
				t.Fatal("released empty-store all-removed metadata edge", filter, minmax, store.Info(), store.Snapshot())
			}
		}
	}
}

func TestPgStatStatementsClearMinmaxAllMetadata(t *testing.T) {
	now := time.Unix(100, 0)
	store := statementClearStore(t, now)
	for _, top := range []bool{false, true} {
		key := statementKey(1, 1)
		key.TopLevel = top
		store.Release(statementClearSample(store, key, now.Add(time.Second)))
	}
	info := store.Info()
	later := now.Add(2 * time.Second)
	if n := store.Clear(statistics.StatementFilter{UserID: 10, DatabaseID: 1, QueryID: 1}, true, later); n != 2 {
		t.Fatal("nonempty minmax did not match the whole store", n)
	}
	if store.Info() != info || len(store.Snapshot()) != 2 {
		t.Fatal("nonempty all-matching minmax reset global metadata", store.Info(), info, store.Snapshot())
	}
	for _, entry := range store.Snapshot() {
		if entry.Rows != 3 || entry.Execution.Count != 2 || entry.Execution.Total != 6 || entry.Planning.Count != 2 || entry.Planning.Total != 6 || entry.MinMaxStatsSince != later || entry.Execution.Min != 0 || entry.Execution.Max != 0 || entry.Planning.Min != 0 || entry.Planning.Max != 0 {
			t.Fatal("all-matching minmax changed cumulative history", entry)
		}
	}
}
