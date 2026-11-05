package statistics_test

import (
	"math"
	"sync"
	"testing"
	"time"

	"stuntgres/internal/engine/statistics"
)

func TestPgStatStatementsCountersAccumulator(t *testing.T) {
	now := time.Unix(100, 0)
	key, peer := statementKey(1, 1), statementKey(2, 1)
	store := statistics.NewStatements(20, now)
	token := store.Begin(key, key, "first", now)
	plan := statistics.StatementRecord{Key: key, At: now, Counters: statistics.StatementCounters{SharedRead: 1, WALRecords: 3, WALBytes: 216}}
	exec := statistics.StatementRecord{Key: key, At: now, Rows: 2, Counters: statistics.StatementCounters{SharedHit: 2, WALRecords: 1, WALBytes: 112}}
	store.RecordPlan(token, plan)
	store.RecordExecution(token, exec)
	held := store.Snapshot()
	want := statistics.StatementCounters{SharedRead: 1, SharedHit: 2, WALRecords: 4, WALBytes: 328}
	if len(held) != 1 || held[0].Counters != want || held[0].Planning.Count != 1 || held[0].Execution.Count != 1 || held[0].Rows != 2 {
		t.Fatal("successful phases", held)
	}
	store.RecordExecution(token, exec)
	want.SharedHit, want.WALRecords, want.WALBytes = 4, 5, 440
	if got := store.Snapshot()[0]; got.Counters != want || held[0].Counters.WALBytes != 328 {
		t.Fatal("addition or detached snapshot", got, held)
	}
	before := store.Info()
	store.Reset(statistics.StatementFilter{DatabaseID: 1}, true, now.Add(time.Second))
	if got := store.Snapshot()[0]; got.Counters != want || got.Execution.Min != 0 || store.Info() != before {
		t.Fatal("minmax altered work/info", got, store.Info())
	}
	bad := exec
	for _, duration := range []float64{-1, math.NaN(), math.Inf(1)} {
		bad.Milliseconds = duration
		store.RecordExecution(token, bad)
	}
	store.RecordExecution(statistics.StatementToken{}, exec)
	if got := store.Snapshot()[0].Counters; got != want {
		t.Fatal("invalid phase added work", got)
	}
	other := store.Begin(peer, peer, "peer", now)
	store.RecordExecution(other, statistics.StatementRecord{Key: peer, At: now, Counters: statistics.StatementCounters{LocalHit: 7}})
	store.Reset(statistics.StatementFilter{DatabaseID: 1}, false, now)
	store.RecordExecution(token, exec) // SQL reset preserves eligible admission.
	for _, entry := range store.Snapshot() {
		if entry.Key == key && entry.Counters != exec.Counters || entry.Key == peer && entry.Counters.LocalHit != 7 {
			t.Fatal("subset reset/reallocation", entry)
		}
	}
	store.Clear(statistics.StatementFilter{DatabaseID: 1}, false, now)
	store.RecordPlan(token, plan)
	store.RecordExecution(token, exec)
	if got := store.Snapshot(); len(got) != 1 || got[0].Key != peer {
		t.Fatal("native fence", got)
	}
	store.Release(other)
	store.RecordExecution(other, statistics.StatementRecord{Key: peer, At: now, Counters: exec.Counters})
	if got := store.Snapshot()[0].Counters; got != (statistics.StatementCounters{LocalHit: 7}) {
		t.Fatal("released token", got)
	}
	store.Clear(statistics.StatementFilter{}, false, now.Add(2*time.Second))
	if len(store.Snapshot()) != 0 || store.Info().Dealloc != 0 || held[0].Counters.WALBytes != 328 {
		t.Fatal("full clear or detached history", store.Snapshot(), held)
	}
}

func TestPgStatStatementsCountersEviction(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(2, now)
	key := statementKey(1, 1)
	token := store.Begin(key, key, "first", now)
	record := statistics.StatementRecord{Key: key, At: now, Counters: statistics.StatementCounters{WALBytes: uint64(math.MaxInt64)+123, WALRecords: 1}}
	store.RecordExecution(token, record)
	held := store.Snapshot()[0]
	store.Begin(statementKey(1, 2), statementKey(1, 2), "second", now)
	store.Begin(statementKey(1, 3), statementKey(1, 3), "third", now) // One batch removes both victims.
	if store.Info().Dealloc != 1 || len(store.Snapshot()) != 0 {
		t.Fatal("actual batch eviction", store.Info(), store.Snapshot())
	}
	store.RecordExecution(token, record)
	if got := store.Snapshot(); len(got) != 1 || got[0].Counters != record.Counters || got[0].Execution.Count != 1 || held.Counters != record.Counters {
		t.Fatal("evicted admission reallocation", got, held)
	}
	store.Clear(statistics.StatementFilter{}, false, now)
	store.RecordExecution(token, record)
	if len(store.Snapshot()) != 0 {
		t.Fatal("evicted active admission escaped clear")
	}
}

func TestPgStatStatementsCountersConcurrent(t *testing.T) {
	now := time.Unix(100, 0)
	store := statistics.NewStatements(20,now)
	key := statementKey(1,1)
	token := store.Begin(key,key,"concurrent",now)
	var joined sync.WaitGroup
	for range 4 {
		joined.Go(func() {
			for range 100 {
				store.RecordExecution(token,statistics.StatementRecord{Key:key,At:now,Counters:statistics.StatementCounters{SharedHit:1,WALRecords:1,WALBytes:72}})
				snapshot := store.Snapshot()
				if len(snapshot) != 1 || snapshot[0].Counters.SharedHit != snapshot[0].Execution.Count || snapshot[0].Counters.WALBytes != uint64(snapshot[0].Counters.WALRecords)*72 { t.Error("incoherent detached vector",snapshot); return }
			}
		})
	}
	joined.Wait()
	store.Release(token)
	if got := store.Snapshot()[0]; got.Counters.SharedHit != 400 || got.Counters.WALRecords != 400 || got.Counters.WALBytes != 28800 { t.Fatal("joined scalar additions",got) }
}
