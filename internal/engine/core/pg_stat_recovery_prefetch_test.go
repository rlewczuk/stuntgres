package core_test

import (
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/statistics"
)

func TestPGStatRecoveryPrefetchInitialization(t *testing.T) {
	// Observe the completed owner before starting any session, preparing a
	// statement, describing a source or executing a monitoring read.
	lower := time.Now().UTC().Truncate(time.Microsecond)
	e := engine.New()
	upper := time.Now().UTC()
	fact := e.Statistics.ReadShared(statistics.RecoveryPrefetch)
	if fact.ResetAt.IsZero() || fact.ResetAt.Before(lower) || fact.ResetAt.After(upper) || fact.ResetAt.Location() != time.UTC || fact.ResetAt.Nanosecond()%1000 != 0 {
		t.Fatal("prefetch was not initialized at genuine owner construction", lower, fact, upper)
	}
	if fact != (statistics.SharedFact{ResetAt: fact.ResetAt}) {
		t.Fatal("startup fabricated prefetch activity", fact)
	}
	for kind := statistics.SharedKind(0); kind < statistics.SharedKindCount; kind++ {
		if kind == statistics.Archiver {
			started := time.UnixMicro(e.Cluster.StartedAt + 946684800000000).UTC()
			if got := e.Statistics.ReadShared(kind); got != (statistics.SharedFact{ResetAt: started}) {
				t.Fatal("archiver startup must contain only genuine cluster reset", got, started)
			}
			continue
		}
		if kind != statistics.RecoveryPrefetch && e.Statistics.ReadShared(kind) != (statistics.SharedFact{}) {
			t.Fatal("prefetch startup initialized another shared family", kind, e.Statistics.ReadShared(kind))
		}
	}
	for kind := statistics.SLRUKind(0); kind < statistics.SLRUCount; kind++ {
		if e.Statistics.ReadSLRU(kind) != (statistics.SharedFact{}) {
			t.Fatal("prefetch startup initialized SLRU", kind)
		}
	}
	if got := e.Statistics.Read(statistics.Key{}); got != (statistics.Fact{}) {
		t.Fatal("targeted initialization changed unrelated default/generation", got)
	}
	s := enginetest.StartSession(t, e, nil)
	enginetest.PreparedDescription(t, e, s, "SELECT * FROM pg_catalog.pg_stat_recovery_prefetch")
	if got := e.Statistics.ReadShared(statistics.RecoveryPrefetch); got != fact {
		t.Fatal("Prepare/Describe changed initialized provider", got, fact)
	}
	for range 3 {
		if got := e.Statistics.ReadShared(statistics.RecoveryPrefetch); got != fact {
			t.Fatal("read sampled a new reset time", got, fact)
		}
	}
	// The provider itself remains zero-value-ready: construction ownership,
	// rather than first read, is responsible for the engine's metadata.
	var zero statistics.Provider
	if got := zero.ReadShared(statistics.RecoveryPrefetch); got != (statistics.SharedFact{}) {
		t.Fatal("zero provider invented initialization metadata", got)
	}
}
