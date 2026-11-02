package statistics_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func statementEntryFor(t *testing.T, store *statistics.Statements, key statistics.StatementKey) statistics.StatementEntry {
	t.Helper()
	for _, entry := range store.Snapshot() {
		if entry.Key == key {
			return entry
		}
	}
	t.Fatalf("missing successful statement %+v in %+v", key, store.Snapshot())
	return statistics.StatementEntry{}
}

func TestPgStatStatementsEvictionInflight(t *testing.T) {
	for _, recreate := range []bool{false, true} {
		name := "absent"
		if recreate {
			name = "same_key_recreated"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			key := statementKey(1, 1)
			pending := store.Begin(key, key, "first admission", now)
			defer store.Release(pending)
			other := store.Begin(statementKey(1, 2), statementKey(1, 2), "pressure", now)
			defer store.Release(other)
			store.RecordExecution(other, statistics.StatementRecord{Key: statementKey(1, 2), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
			if store.Info().Dealloc != 1 || len(store.Snapshot()) != 1 {
				t.Fatal("pressure did not evict the admitted entry", store.Info(), store.Snapshot())
			}
			if recreate {
				current := store.Begin(key, key, "current representative", now.Add(time.Second))
				defer store.Release(current)
				store.RecordExecution(current, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 4, Rows: 2})
			}
			store.RecordPlan(pending, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 2, Rows: 0})
			store.RecordExecution(pending, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 6, Rows: 3})
			entry := statementEntryFor(t, store, key)
			calls, rows, total := int64(1), int64(3), float64(6)
			if recreate {
				calls, rows, total = 2, 5, 10
				if entry.Query != "current representative" {
					t.Fatal("old admission replaced current text", entry)
				}
			}
			if entry.Planning.Count != 1 || entry.Planning.Total != 2 || entry.Execution.Count != calls || entry.Rows != rows || entry.Execution.Total != total || len(store.Snapshot()) != 1 || store.Info().Dealloc != 2 {
				t.Fatal("successful post-eviction phases were lost or duplicated", entry, store.Info(), store.Snapshot())
			}
		})
	}
}

func TestPgStatStatementsClearEvictedAdmissions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter statistics.StatementFilter
		fenced bool
	}{
		{"full", statistics.StatementFilter{}, true},
		{"matching_database", statistics.StatementFilter{DatabaseID: 1}, true},
		{"matching_combined", statistics.StatementFilter{UserID: 10, DatabaseID: 1, QueryID: 1}, true},
		{"unaffected_database", statistics.StatementFilter{DatabaseID: 2}, false},
		{"unaffected_user", statistics.StatementFilter{UserID: 11}, false},
		{"unaffected_query", statistics.StatementFilter{QueryID: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			key := statementKey(1, 1)
			old := store.Begin(key, key, "old", now)
			defer store.Release(old)
			pressure := store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now)
			defer store.Release(pressure)
			store.RecordExecution(pressure, statistics.StatementRecord{Key: statementKey(2, 2), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
			// Reset must still reach a matching live admission after its entry
			// vanished. An unrelated filtered reset must not fence that admission.
			store.Clear(tc.filter, false, now.Add(time.Second))
			fresh := store.Begin(key, key, "fresh", now.Add(2*time.Second))
			defer store.Release(fresh)
			store.RecordExecution(fresh, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 2, Rows: 2})
			store.RecordPlan(old, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 7, Rows: 0})
			store.RecordExecution(old, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 6, Rows: 3})
			entry := statementEntryFor(t, store, key)
			plans, calls, rows := int64(1), int64(2), int64(5)
			if tc.fenced {
				plans, calls, rows = 0, 1, 2
			}
			if entry.Planning.Count != plans || entry.Execution.Count != calls || entry.Rows != rows || entry.Query != "fresh" {
				t.Fatal("reset fence confused with eviction", entry, tc)
			}
		})
	}
}

// These populations have strictly different usage at the victim boundary; no
// assertion depends on ordering entries whose usage is tied. REL_18_6 starts
// pending admissions at median10, decays them by .5 (completed entries by .99),
// and resets first-success usage to1 before adding the successful phase's1.
func TestPgStatStatementsEvictionStickyUsage(t *testing.T) {
	for _, tc := range []struct {
		name            string
		completedCalls  int
		completePending bool
		wantCompleted   int
	}{
		{"initial_median_and_sticky_decay", 3, false, 0},   // 10*.5=5 > (1+3)*.99=3.96
		{"first_success_two_lower_boundary", 4, false, 0},  // (1+4)*.99=4.95 < sticky5
		{"first_success_two_upper_boundary", 5, false, 10}, // (1+5)*.99=5.94 > sticky5
		{"nonsticky_decay", 8, false, 10},                  // 10*.5=5 < (1+8)*.99=8.91
		{"first_success_unsticks", 3, true, 10},            // (1+1)*.99=1.98 <3.96
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(20, now)
			for i := int64(1); i <= 10; i++ {
				pending := store.Begin(statementKey(1, i), statementKey(1, i), "pending", now)
				defer store.Release(pending)
				if tc.completePending {
					store.RecordPlan(pending, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 0})
				}
			}
			for i := int64(11); i <= 20; i++ {
				completed := store.Begin(statementKey(1, i), statementKey(1, i), "completed", now)
				defer store.Release(completed)
				for range tc.completedCalls {
					store.RecordExecution(completed, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
				}
			}
			store.Release(store.Begin(statementKey(1, 21), statementKey(1, 21), "trigger", now))
			retained := 0
			for _, entry := range store.Snapshot() {
				if entry.Key.QueryID >= 11 && entry.Key.QueryID <= 20 {
					retained++
				}
			}
			if retained != tc.wantCompleted || store.Info().Dealloc != 1 {
				t.Fatal("wrong untied usage class evicted", retained, tc.wantCompleted, store.Info(), store.Snapshot())
			}
		})
	}
}

func TestPgStatStatementsEvictionRepeatedStickyDecay(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(20, now)
	for i := int64(1); i <= 10; i++ {
		store.Release(store.Begin(statementKey(1, i), statementKey(1, i), "pending", now))
	}
	for i := int64(11); i <= 20; i++ {
		token := store.Begin(statementKey(1, i), statementKey(1, i), "first population", now)
		defer store.Release(token)
		for range 3 {
			store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
		}
	}
	// Round1: completed3.96 < sticky5, so all10 completed entries leave.
	// Median includes the victims: sorted index20/2=10 has usage5.
	store.Release(store.Begin(statementKey(1, 21), statementKey(1, 21), "new median admission", now))
	if len(store.Snapshot()) != 0 || store.Info().Dealloc != 1 {
		t.Fatal("first-round median population", store.Snapshot(), store.Info())
	}
	for i := int64(22); i <= 30; i++ {
		token := store.Begin(statementKey(1, i), statementKey(1, i), "second population", now)
		defer store.Release(token)
		for range 3 {
			store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
		}
	}
	// Round2: all11 stickies decay to2.5, below each of the9 completed
	// entries at3.96. Which10 sticky victims leave is intentionally unspecified.
	store.Release(store.Begin(statementKey(1, 31), statementKey(1, 31), "second trigger", now))
	if len(store.Snapshot()) != 9 || store.Info().Dealloc != 2 {
		t.Fatal("repeated sticky decay did not preserve the untied completed class", store.Snapshot(), store.Info())
	}
	for _, entry := range store.Snapshot() {
		if entry.Key.QueryID < 22 || entry.Key.QueryID > 30 || entry.Execution.Count != 3 {
			t.Fatal("wrong repeated-round survivor", entry)
		}
	}
}

func TestPgStatStatementsEvictionMedianUpdate(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(30, now)
	for i := int64(1); i <= 20; i++ {
		token := store.Begin(statementKey(1, i), statementKey(1, i), "completed", now)
		defer store.Release(token)
		store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
		if i > 10 {
			store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
		}
	}
	pending := make([]statistics.StatementToken, 0, 10)
	for i := int64(21); i <= 30; i++ {
		pending = append(pending, store.Begin(statementKey(1, i), statementKey(1, i), "pending", now))
	}
	defer func() {
		for _, token := range pending {
			store.Release(token)
		}
	}()
	// Round1 has three untied classes: 10 at1.98, 10 at2.97 and 10 at5.
	// The pre-removal upper median is2.97, not the post-removal median5.
	store.Release(store.Begin(statementKey(1, 31), statementKey(1, 31), "new sticky", now))
	if len(store.Snapshot()) != 10 || store.Info().Dealloc != 1 {
		t.Fatal("first median population", store.Snapshot(), store.Info())
	}
	for i, token := range pending {
		for range 20 {
			store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, int64(i)+21), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
		}
	}
	for i := int64(32); i <= 35; i++ {
		store.Release(store.Begin(statementKey(1, i), statementKey(1, i), "new sticky", now))
	}
	for i := int64(36); i <= 40; i++ {
		token := store.Begin(statementKey(1, i), statementKey(1, i), "new completed", now)
		defer store.Release(token)
		store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
	}
	// Round2 removes exactly the five new stickies at2.97*.5=1.485
	// and five new completed entries at2*.99=1.98. All ten old completed
	// entries at2.97*.99=2.9403 survive. An unchanged median10 OR a
	// post-removal median5 instead removes five of those old completed entries.
	store.Release(store.Begin(statementKey(1, 41), statementKey(1, 41), "second trigger", now))
	if len(store.Snapshot()) != 20 || store.Info().Dealloc != 2 {
		t.Fatal("median update chose the wrong untied classes", store.Snapshot(), store.Info())
	}
	for _, entry := range store.Snapshot() {
		if entry.Key.QueryID < 11 || entry.Key.QueryID > 30 {
			t.Fatal("new low-usage population survived", entry)
		}
		calls := int64(2)
		if entry.Key.QueryID > 20 {
			calls = 20
		}
		if entry.Execution.Count != calls || entry.Rows != calls {
			t.Fatal("survivor history changed", entry)
		}
	}
}

func TestPgStatStatementsClearEvictedMinmaxAdmission(t *testing.T) {
	for _, tc := range []struct {
		name   string
		filter statistics.StatementFilter
		fenced bool
	}{
		{"full", statistics.StatementFilter{}, true},
		{"matching_combined", statistics.StatementFilter{UserID: 10, DatabaseID: 1, QueryID: 1}, true},
		{"unaffected_database", statistics.StatementFilter{DatabaseID: 2}, false},
		{"unaffected_user", statistics.StatementFilter{UserID: 11}, false},
		{"unaffected_query", statistics.StatementFilter{QueryID: 2}, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			key := statementKey(1, 1)
			old := store.Begin(key, key, "old", now)
			defer store.Release(old)
			pressure := store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now)
			defer store.Release(pressure)
			store.RecordExecution(pressure, statistics.StatementRecord{Key: statementKey(2, 2), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
			store.Clear(tc.filter, true, now.Add(time.Second))
			fresh := store.Begin(key, key, "fresh", now.Add(2*time.Second))
			defer store.Release(fresh)
			store.RecordExecution(fresh, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 2, Rows: 2})
			held := statementEntryFor(t, store, key)
			store.RecordPlan(old, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 7, Rows: 0})
			store.RecordExecution(old, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 6, Rows: 3})
			entry := statementEntryFor(t, store, key)
			planMin, planMax, execMax := float64(7), float64(7), float64(6)
			if tc.fenced {
				planMin, planMax, execMax = 0, 0, 2
			}
			if entry.Planning.Count != 1 || entry.Planning.Total != 7 || entry.Planning.Min != planMin || entry.Planning.Max != planMax || entry.Execution.Count != 2 || entry.Execution.Total != 8 || entry.Execution.Min != 2 || entry.Execution.Max != execMax || entry.Rows != 5 || entry.Query != "fresh" || entry.MinMaxStatsSince != held.MinMaxStatsSince {
				t.Fatal("evicted admission lost totals or crossed minmax fence", entry, tc)
			}
			if held.Execution.Count != 1 || held.Execution.Total != 2 || held.Rows != 2 || held.Planning.Count != 0 {
				t.Fatal("old sample mutated held snapshot", held)
			}
		})
	}
}

func TestPgStatStatementsEvictionSuccessfulPhase(t *testing.T) {
	for _, planning := range []bool{false, true} {
		name := "execution_only"
		if planning {
			name = "planning_only"
		}
		t.Run(name, func(t *testing.T) {
			now := time.Unix(100, 0)
			store := statistics.NewStatements(1, now)
			key := statementKey(1, 1)
			pending := store.Begin(key, key, "admitted", now)
			defer store.Release(pending)
			pressure := store.Begin(statementKey(2, 2), statementKey(2, 2), "pressure", now)
			defer store.Release(pressure)
			store.RecordExecution(pressure, statistics.StatementRecord{Key: statementKey(2, 2), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
			held := store.Snapshot()
			if planning {
				store.RecordPlan(pending, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 4, Rows: 0})
			} else {
				store.RecordExecution(pending, statistics.StatementRecord{Key: key, Query: "successful raw", At: now, Milliseconds: 4, Rows: 3})
			}
			entry := statementEntryFor(t, store, key)
			plans, calls, rows := int64(0), int64(1), int64(3)
			if planning {
				plans, calls, rows = 1, 0, 0
			}
			if entry.Planning.Count != plans || entry.Execution.Count != calls || entry.Rows != rows || entry.Planning.Total+entry.Execution.Total != 4 || len(store.Snapshot()) != 1 || store.Info().Dealloc != 2 {
				t.Fatal("independent successful phase was lost after eviction", entry, store.Info())
			}
			if len(held) != 1 || held[0].Key.DatabaseID != 2 || held[0].Execution.Count != 1 || held[0].Rows != 1 {
				t.Fatal("successful recreation mutated held pressure snapshot", held)
			}
		})
	}
}
