package statistics_test

import (
	"stuntgres/internal/engine/statistics"
	"sync"
	"testing"
	"time"
)

func statementKey(db uint32, query int64) statistics.StatementKey {
	return statistics.StatementKey{UserID: 10, DatabaseID: db, QueryID: query, TopLevel: true}
}
func TestPgStatStatementsAccumulator(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(100, now)
	token := store.Begin(statementKey(1, 1), statementKey(1, 1), "SELECT $1", now)
	defer store.Release(token)
	store.RecordPlan(token, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 2, Rows: 0})
	store.RecordPlan(token, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 4, Rows: 0})
	store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 2, Rows: 2})
	store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 4, Rows: 1})
	snapshot := store.Snapshot()
	if len(snapshot) != 1 {
		t.Fatal(snapshot)
	}
	e := snapshot[0]
	for _, m := range []statistics.StatementMoments{e.Planning, e.Execution} {
		if m.Count != 2 || m.Total != 6 || m.Mean != 3 || m.Min != 2 || m.Max != 4 || m.Stddev() != 1 {
			t.Fatal(m)
		}
	}
	if e.Rows != 3 || e.Query != "SELECT $1" || e.StatsSince != now {
		t.Fatal(e)
	}
	store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 8, Rows: 0})
	if snapshot[0].Execution.Count != 2 {
		t.Fatal("snapshot mutated")
	}
	single := store.Begin(statementKey(1, 2), statementKey(1, 2), "SELECT $2", now)
	defer store.Release(single)
	store.RecordExecution(single, statistics.StatementRecord{Key: statementKey(1, 2), Query: "successful raw", At: now, Milliseconds: 0, Rows: 0})
	for _, e := range store.Snapshot() {
		if e.Key.QueryID == 2 && (e.Execution.Count != 1 || e.Execution.Stddev() != 0 || e.Execution.Min != 0 || e.Execution.Max != 0) {
			t.Fatal(e)
		}
	}
}
func TestPgStatStatementsClear(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(100, now)
	a := store.Begin(statementKey(1, 1), statementKey(1, 1), "first", now)
	defer store.Release(a)
	b := store.Begin(statementKey(2, 1), statementKey(2, 1), "peer", now)
	defer store.Release(b)
	store.RecordExecution(a, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 2, Rows: 2})
	store.RecordExecution(b, statistics.StatementRecord{Key: statementKey(2, 1), Query: "successful raw", At: now, Milliseconds: 4, Rows: 3})
	held := store.Snapshot()
	later := now.Add(time.Second)
	if n := store.Clear(statistics.StatementFilter{DatabaseID: 1}, true, later); n != 1 {
		t.Fatal(n)
	}
	store.RecordExecution(a, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 10, Rows: 1})
	for _, e := range store.Snapshot() {
		if e.Key.DatabaseID == 1 && (e.Execution.Count != 2 || e.Execution.Total != 12 || e.Execution.Min != 0 || e.Execution.Max != 0 || e.MinMaxStatsSince != later) {
			t.Fatal(e)
		}
	}
	fresh := store.Begin(statementKey(1, 1), statementKey(1, 1), "ignored", later)
	defer store.Release(fresh)
	store.RecordExecution(fresh, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 6, Rows: 1})
	for _, e := range store.Snapshot() {
		if e.Key.DatabaseID == 1 && (e.Execution.Min != 6 || e.Execution.Max != 6 || e.Query != "first") {
			t.Fatal(e)
		}
	}
	if n := store.Clear(statistics.StatementFilter{DatabaseID: 1}, false, later); n != 1 {
		t.Fatal(n)
	}
	successor := store.Begin(statementKey(1, 1), statementKey(1, 1), "replacement", later)
	defer store.Release(successor)
	store.RecordPlan(a, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 100, Rows: 0})
	store.RecordExecution(a, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 100, Rows: 100})
	store.RecordExecution(successor, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
	for _, e := range store.Snapshot() {
		if e.Key.DatabaseID == 1 && (e.Execution.Count != 1 || e.Rows != 1 || e.Planning.Count != 0) {
			t.Fatal("resurrected", e)
		}
		if e.Key.DatabaseID == 2 && (e.Execution.Count != 1 || e.Rows != 3) {
			t.Fatal("peer cleared", e)
		}
	}
	if len(held) != 2 {
		t.Fatal("held capture changed")
	}
	store.Clear(statistics.StatementFilter{}, false, later)
	store.RecordExecution(b, statistics.StatementRecord{Key: statementKey(2, 1), Query: "successful raw", At: now, Milliseconds: 100, Rows: 100})
	if len(store.Snapshot()) != 0 || store.Info().StatsReset != later || store.Info().Dealloc != 0 {
		t.Fatal(store.Info(), store.Snapshot())
	}
}
func TestPgStatStatementsEviction(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(20, now)
	hot := store.Begin(statementKey(1, 1), statementKey(1, 1), "hot", now)
	defer store.Release(hot)
	for range 100 {
		store.RecordExecution(hot, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
	}
	for i := int64(2); i <= 21; i++ {
		token := store.Begin(statementKey(1, i), statementKey(1, i), "cold", now)
		defer store.Release(token)
		store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, i), Query: "successful raw", At: now, Milliseconds: 1, Rows: 1})
	}
	if len(store.Snapshot()) != 11 || store.Info().Dealloc != 1 {
		t.Fatal(len(store.Snapshot()), store.Info())
	}
	found := false
	for _, e := range store.Snapshot() {
		if e.Key.QueryID == 1 {
			found = true
			if e.Execution.Count != 100 {
				t.Fatal(e)
			}
		}
	}
	if !found {
		t.Fatal("hot entry evicted")
	}
}
func TestPgStatStatementsAccumulatorConcurrent(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(100, now)
	token := store.Begin(statementKey(1, 1), statementKey(1, 1), "concurrent", now)
	defer store.Release(token)
	var group sync.WaitGroup
	for range 4 {
		group.Go(func() {
			for range 1000 {
				store.RecordExecution(token, statistics.StatementRecord{Key: statementKey(1, 1), Query: "successful raw", At: now, Milliseconds: 2, Rows: 1})
				snapshot := store.Snapshot()
				if len(snapshot) != 1 {
					t.Error(snapshot)
					return
				}
			}
		})
	}
	group.Wait()
	e := store.Snapshot()[0]
	if e.Execution.Count != 4000 || e.Rows != 4000 || e.Execution.Total != 8000 || e.Execution.Stddev() != 0 {
		t.Fatal(e)
	}
}
