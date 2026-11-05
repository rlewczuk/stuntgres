package statistics_test

import (
	"math"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsResetAccumulator(t *testing.T) {
	for _, minmax := range []bool{false, true} {
		t.Run(map[bool]string{false: "remove", true: "minmax"}[minmax], func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statementClearStore(t, now)
			keys := []statistics.StatementKey{
				{UserID: 10, DatabaseID: 1, QueryID: 1, TopLevel: true},
				{UserID: 10, DatabaseID: 1, QueryID: 1, TopLevel: false},
				{UserID: 11, DatabaseID: 1, QueryID: 1, TopLevel: true},
				{UserID: 10, DatabaseID: 2, QueryID: 1, TopLevel: true},
				{UserID: 10, DatabaseID: 1, QueryID: 2, TopLevel: true},
			}
			var tokens [5]statistics.StatementToken
			for i, key := range keys {
				tokens[i] = statementClearSample(store, key, now.Add(time.Second))
				defer store.Release(tokens[i])
			}
			held := store.Snapshot()
			saved := append([]statistics.StatementEntry(nil), held...)
			before, info := statementClearSnapshot(store), store.Info()
			at := now.Add(2 * time.Second)
			if count := store.Reset(statistics.StatementFilter{UserID: 10, DatabaseID: 1, QueryID: 1}, minmax, at); count != 2 {
				t.Fatal("conjunction must select both top-level dimensions", count)
			}
			after := statementClearSnapshot(store)
			if store.Info() != info {
				t.Fatal("partial reset changed nonzero dealloc/global timestamp", store.Info(), info)
			}
			for i, key := range keys {
				old := before[key]
				entry, present := after[key]
				if i >= 2 {
					if !present || entry != old {
						t.Fatal("one-dimension nonmatch changed", key, entry, old)
					}
					continue
				}
				if !minmax {
					if present {
						t.Fatal("reset credited success or retained a removed entry", entry)
					}
				} else {
					if !present || entry.Rows != 3 || entry.StatsSince != old.StatsSince || entry.MinMaxStatsSince != at || entry.Query != old.Query {
						t.Fatal("minmax changed cumulative identity/rows", entry, old)
					}
					for _, moments := range []statistics.StatementMoments{entry.Planning, entry.Execution} {
						if moments.Count != 2 || moments.Total != 6 || moments.Mean != 3 || moments.Stddev() != 1 || moments.Min != 0 || moments.Max != 0 {
							t.Fatal("independent 2+4ms minmax baseline", moments)
						}
					}
				}
				// SQL Reset does not fence old tokens: only actual later phase
				// completion recreates entries or initializes the new extrema.
				store.RecordExecution(tokens[i], statistics.StatementRecord{Key: key, Query: "later raw", At: at.Add(time.Second), Milliseconds: 10, Rows: 4})
				entry = statementEntryFor(t, store, key)
				if entry.Execution.Min != 10 || entry.Execution.Max != 10 {
					t.Fatal("eligible old execution failed to initialize extrema", entry)
				}
				if minmax {
					m := entry.Execution
					if entry.Rows != 7 || m.Count != 3 || m.Total != 16 || math.Abs(m.Mean-16.0/3) > 1e-12 || math.Abs(m.Stddev()*m.Stddev()-104.0/9) > 1e-12 || entry.Planning.Min != 0 || entry.Planning.Max != 0 {
						t.Fatal("cumulative moments or unsampled phase extrema changed", entry)
					}
				} else if entry.Execution.Count != 1 || entry.Execution.Total != 10 || entry.Rows != 4 || entry.Planning.Count != 0 || entry.Query != "later raw" || entry.StatsSince != at.Add(time.Second) {
					t.Fatal("destructive reset replayed old planning/history", entry)
				}
			}
			if !reflect.DeepEqual(held, saved) {
				t.Fatal("reset/later completion mutated owned snapshots")
			}
		})
	}
	t.Run("physical_empty_and_sticky_info", func(t *testing.T) {
		now := time.Unix(100, 0)
		store := statistics.NewStatements(8, now)
		key := statementKey(1, 1)
		token := store.Begin(key, key, "sticky", now)
		defer store.Release(token)
		info := store.Info()
		if len(store.Snapshot()) != 0 || store.Reset(statistics.StatementFilter{QueryID: 99}, true, now.Add(time.Second)) != 0 || store.Info() != info {
			t.Fatal("invisible sticky admission incorrectly treated as empty")
		}
		if store.Reset(statistics.StatementFilter{QueryID: 1}, true, now.Add(2*time.Second)) != 1 || store.Info() != info {
			t.Fatal("nonempty all-matching minmax reset global info")
		}
		at := now.Add(3 * time.Second)
		if store.Reset(statistics.StatementFilter{QueryID: 1}, false, at) != 1 || store.Info() != (statistics.StatementInfo{StatsReset: at}) {
			t.Fatal("filtered final physical removal metadata", store.Info())
		}
		for _, minmax := range []bool{false, true} {
			at = at.Add(time.Second)
			if store.Reset(statistics.StatementFilter{QueryID: 99}, minmax, at) != 0 || store.Info() != (statistics.StatementInfo{StatsReset: at}) {
				t.Fatal("empty 0==0 reset outcome", store.Info())
			}
		}
	})
}

func TestPgStatStatementsResetEligibility(t *testing.T) {
	now := time.Unix(100, 0)
	filter := statistics.StatementFilter{DatabaseID: 1}
	key := statementKey(1, 1)
	for _, nativeMinmax := range []bool{false, true} {
		store := statistics.NewStatements(8, now)
		token := statementClearSample(store, key, now)
		store.Clear(filter, nativeMinmax, now.Add(time.Second))
		store.Reset(filter, false, now.Add(2*time.Second))
		store.RecordExecution(token, statistics.StatementRecord{Key: key, Query: "later", At: now.Add(3 * time.Second), Milliseconds: 10, Rows: 4})
		if nativeMinmax {
			entry := statementEntryFor(t, store, key)
			if entry.Execution.Count != 1 || entry.Rows != 4 || entry.Execution.Min != 0 || entry.Execution.Max != 0 {
				t.Fatal("SQL Reset revived a native minmax fence", entry)
			}
		} else if len(store.Snapshot()) != 0 {
			t.Fatal("SQL Reset revived a native destructive fence")
		}
		store.Release(token)
		before := store.Snapshot()
		store.RecordExecution(token, statistics.StatementRecord{Key: key, At: now, Milliseconds: 1, Rows: 9})
		store.RecordPlan(statistics.StatementToken{}, statistics.StatementRecord{Key: key, At: now, Milliseconds: 1})
		if !reflect.DeepEqual(store.Snapshot(), before) {
			t.Fatal("released/zero token republished history")
		}
	}
	// Deterministic concurrent handoff: the real pre-reset token records only
	// after entry removal. The leaf mutex protects Reset/record/snapshot state.
	store := statistics.NewStatements(8, now)
	token := statementClearSample(store, key, now)
	defer store.Release(token)
	start, done := make(chan struct{}), make(chan struct{})
	go func() {
		defer close(done)
		<-start
		store.RecordPlan(token, statistics.StatementRecord{Key: key, Query: "later", At: now.Add(2 * time.Second), Milliseconds: 10})
		store.RecordExecution(token, statistics.StatementRecord{Key: key, Query: "later", At: now.Add(2 * time.Second), Milliseconds: 10, Rows: 4})
	}()
	store.Reset(filter, false, now.Add(time.Second))
	beforeCompletion := store.Snapshot()
	close(start)
	<-done
	if len(beforeCompletion) != 0 {
		t.Fatal("reset manufactured a successful phase")
	}
	entry := statementEntryFor(t, store, key)
	if entry.Planning.Count != 1 || entry.Execution.Count != 1 || entry.Planning.Total != 10 || entry.Execution.Total != 10 || entry.Rows != 4 {
		t.Fatal("real later phases lost or old phases replayed", entry)
	}
	store.Reset(filter, true, now.Add(3*time.Second))
	store.Clear(filter, false, now.Add(4*time.Second))
	store.RecordExecution(token, statistics.StatementRecord{Key: key, At: now, Milliseconds: 1, Rows: 1})
	if len(store.Snapshot()) != 0 {
		t.Fatal("later native Clear did not fence prior SQL Reset admission")
	}
}
