package core_test

import (
	"context"
	"fmt"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func TestStatsResetReplicationSharedEntryVisibility(t *testing.T) {
	for _, scenario := range []string{"staged_drop", "local_provisional", "peer_provisional"} {
		for _, prepared := range []bool{false, true} {
			for _, commit := range []bool{false, true} {
				t.Run(fmt.Sprintf("%s/prepared=%v/commit=%v", scenario, prepared, commit), func(t *testing.T) {
					resetStatsSharedEntryVisibility(t, scenario, prepared, commit)
				})
			}
		}
	}
}

// Inspect only the existing test-local full snapshot. No second inventory or
// public inspection capability is introduced for invisible shared records.
func resetStatsSharedEntryCount(provider *statistics.Provider, key statistics.Key) int {
	var snapshot statistics.Reader
	snapshot.Configure("snapshot", 1)
	snapshot.Read(provider, key, time.Unix(200, 0).UTC())
	return reflect.ValueOf(&snapshot).Elem().FieldByName("cache").Len()
}

func resetStatsSharedCatalogMissing(t *testing.T, e *core.Engine, actor *session.Session, key statistics.Key) {
	t.Helper()
	op, err := actor.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	missing := err == nil && auth.Catalog.Get(key.Object.Address) == nil
	e.Mu.Unlock()
	if err != nil || !missing {
		t.Fatal("fixture did not establish caller-private catalog invisibility", key, err)
	}
}

func resetStatsSharedEntryVisibility(t *testing.T, scenario string, prepared, commit bool) {
	t.Helper()
	e, resetter, peer, peerActor, keys := resetStatsFixture(t)
	localControls := resetStatsControls(t, e, resetter)
	peerControls := resetStatsControls(t, peer, peerActor)
	oldStamp := time.Unix(100, 0).UTC()
	captureAt := time.Unix(200, 0).UTC()

	owner, creator, target, name := e, resetter, keys[0], "reset_target_a"
	if scenario != "staged_drop" {
		name = "reset_visibility_provisional"
		if scenario == "peer_provisional" {
			owner, creator = peer, peerActor
		} else {
			creator = enginetest.StartSession(t, e, nil)
		}
		statsFunctionExec(t, creator, "BEGIN")
		target = resetStatsCreate(t, owner, creator, name)
	}

	var descriptor catalog.Descriptor
	if scenario == "staged_drop" {
		descriptor = owner.CatalogRead().Get(target.Object.Address)
	} else {
		descriptor = creator.Transactions.Catalog.Descriptors[target.Object.Address]
		if owner.CatalogRead().Get(target.Object.Address) != nil || e.CatalogRead().Get(target.Object.Address) != nil {
			t.Fatal("provisional descriptor became committed/projected before reset")
		}
		resetStatsSharedCatalogMissing(t, e, resetter, target)
	}
	if _, ok := descriptor.(*catalog.Subscription); !ok {
		t.Fatal("fixture lacks genuine subscription descriptor", descriptor)
	}
	if !owner.Statistics.ResetEntry(target, oldStamp) {
		t.Fatal("genuine creation lacks its shared entry")
	}
	before := resetStatsSeed(owner, target, 1)
	if before.ResetAt != oldStamp {
		t.Fatal("fixture lacks independent old reset timestamp", before)
	}

	type control struct {
		engine *core.Engine
		key    statistics.Key
		fact   statistics.Fact
	}
	var controls []control
	var sameProviderControl statistics.Key
	var sameProviderFact statistics.Fact
	for i, key := range keys {
		if key == target {
			continue
		}
		controlOwner := e
		if i == 2 {
			controlOwner = peer
		}
		start := int64(31)
		if i == 1 {
			start = 11
		}
		if i == 2 {
			start = 21
		}
		if scenario == "peer_provisional" {
			if i == 1 {
				start = 21
			}
			if i == 2 {
				start = 11
			}
		}
		controlOwner.Statistics.ResetEntry(key, oldStamp)
		fact := resetStatsSeed(controlOwner, key, start)
		controls = append(controls, control{controlOwner, key, fact})
		if controlOwner == owner {
			sameProviderControl, sameProviderFact = key, fact
		}
	}
	if sameProviderControl == (statistics.Key{}) {
		t.Fatal("fixture lacks unrelated positive same-provider subscription")
	}

	independent := engine.New()
	independentActor := enginetest.StartSession(t, independent, nil)
	independentKey := resetStatsCreate(t, independent, independentActor, "reset_visibility_independent")
	independent.Statistics.ResetEntry(independentKey, oldStamp)
	independentFact := resetStatsSeed(independent, independentKey, 41)
	assertControls := func() {
		t.Helper()
		for _, c := range controls {
			if got := c.engine.Statistics.Read(c.key); got != c.fact {
				t.Fatal("named reset/lifetime changed unrelated subscription", c.key, got, c.fact)
			}
		}
		resetStatsWantControls(t, e, localControls)
		resetStatsWantControls(t, peer, peerControls)
		if got := independent.Statistics.Read(independentKey); got != independentFact {
			t.Fatal("named reset crossed independent cluster", got, independentFact)
		}
	}

	var cache, snapshot, none statistics.Reader
	cache.Configure("cache", 1)
	snapshot.Configure("snapshot", 1)
	none.Configure("none", 1)
	for _, reader := range []*statistics.Reader{&cache, &snapshot} {
		if reader.Read(&owner.Statistics, target, captureAt) != before || reader.Read(&owner.Statistics, sameProviderControl, captureAt) != sameProviderFact {
			t.Fatal("fixture did not capture positive target and control")
		}
	}
	if !statsLifetimeSnapshotContains(&snapshot, target) || snapshot.Timestamp() != captureAt {
		t.Fatal("fixture lacks original full shared-key capture")
	}
	entriesBefore := resetStatsSharedEntryCount(&owner.Statistics, target)
	var late statistics.Writer
	late.Add(&owner.Statistics, target, resetStatsVector(2))

	if scenario == "staged_drop" {
		statsFunctionExec(t, creator, "BEGIN", "DROP SUBSCRIPTION "+name)
		resetStatsSharedCatalogMissing(t, e, resetter, target)
		if d, present := creator.Transactions.Catalog.Descriptors[target.Object.Address]; !present || d != nil {
			t.Fatal("fixture lacks actual private DROP tombstone")
		}
		if got := owner.Statistics.Read(target); got != before {
			t.Fatal("staged DROP prematurely retired shared statistics", got, before)
		}
	}

	projected := statistics.Key{Object: e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), target.Object.Address), Category: statistics.Subscription}
	if owner != e {
		resetStatsAbsent(t, e, projected)
	}
	ast, description := enginetest.PreparedDescription(t, e, resetter, "SELECT pg_stat_reset_subscription_stats($1::oid)")
	defer e.RetirePreparedDescription(&description)
	if got := owner.Statistics.Read(target); got != before {
		t.Fatal("Prepare reset an invisible shared entry", got, before)
	}
	argument := fmt.Sprint(target.Object.Address.ObjectID)
	lower := time.Now().UTC().Truncate(time.Microsecond)
	if prepared {
		if err := resetStatsPrepared(e, resetter, ast, &description, &argument); err != nil {
			t.Fatal(err)
		}
	} else {
		resetStatsExec(t, resetter, "SELECT pg_stat_reset_subscription_stats("+argument+"::oid)")
	}
	upper := time.Now().UTC()
	stamp := resetStatsZero(t, owner, target)
	zero := statistics.Fact{ResetAt: stamp, Generation: before.Generation}
	if stamp == oldStamp || stamp.Before(lower) || stamp.After(upper) || stamp.Location() != time.UTC || none.Read(&owner.Statistics, target, upper) != zero {
		t.Fatal("named reset did not replace old shared counters/stamp within call bounds", before, zero, lower, upper)
	}
	if resetStatsSharedEntryCount(&owner.Statistics, target) != entriesBefore {
		t.Fatal("named reset changed shared record membership")
	}
	var fresh statistics.Reader
	fresh.Configure("snapshot", 1)
	fresh.Read(&owner.Statistics, target, upper)
	if !statsLifetimeSnapshotContains(&fresh, target) || !late.Pending() {
		t.Fatal("named reset lost actual stored key or pending authority")
	}
	if cache.Read(&owner.Statistics, target, upper) != before || snapshot.Read(&owner.Statistics, target, upper) != before || snapshot.Timestamp() != captureAt {
		t.Fatal("named reset invalidated retained counters/stamp/snapshot timestamp")
	}
	if cache.Read(&owner.Statistics, sameProviderControl, upper) != sameProviderFact || snapshot.Read(&owner.Statistics, sameProviderControl, upper) != sameProviderFact {
		t.Fatal("retained same-provider control changed")
	}
	if owner != e {
		resetStatsAbsent(t, e, projected)
	}
	assertControls()

	// Absence is a shared-record no-op through the very same held definition.
	missing := target
	missing.Object.Address.ObjectID = 4294967294
	absentArgument := "4294967294"
	if err := resetStatsPrepared(e, resetter, ast, &description, &absentArgument); err != nil {
		t.Fatal(err)
	}
	resetStatsAbsent(t, owner, missing)
	if owner.Statistics.Read(target) != zero || resetStatsSharedEntryCount(&owner.Statistics, target) != entriesBefore {
		t.Fatal("absent OID changed existing target or allocated a record")
	}

	terminal := "ROLLBACK"
	if commit {
		terminal = "COMMIT"
	}
	statsFunctionExec(t, creator, terminal)
	keep := scenario == "staged_drop" && !commit || scenario != "staged_drop" && commit
	cacheExpected := before
	if keep {
		if owner.CatalogRead().Get(target.Object.Address) != descriptor || resetStatsKey(t, owner, creator, name) != target || owner.Statistics.Read(target) != zero {
			t.Fatal("creator outcome lost original identity or immediate reset state")
		}
		cache.Clear()
		if cache.Read(&owner.Statistics, target, upper) != zero {
			t.Fatal("explicit reader clear failed to expose reset")
		}
		cacheExpected = zero
		statsFunctionExec(t, creator, "BEGIN", "DROP SUBSCRIPTION "+name, "COMMIT")
	}
	resetStatsAbsent(t, owner, target)
	if owner.CatalogRead().Get(target.Object.Address) != nil || resetStatsSharedEntryCount(&owner.Statistics, target) != entriesBefore-1 {
		t.Fatal("actual commit/abort boundary retained retired record/descriptor")
	}
	if !late.Pending() {
		t.Fatal("reset unexpectedly consumed a foreign pending batch")
	}
	late.Flush(&owner.Statistics, time.Now(), true)
	late.Add(&owner.Statistics, target, resetStatsVector(51))
	late.Flush(&owner.Statistics, time.Now(), true)
	resetStatsAbsent(t, owner, target)
	if err := resetStatsPrepared(e, resetter, ast, &description, &argument); err != nil {
		t.Fatal(err)
	}
	if err := resetStatsPrepared(e, resetter, ast, &description, &absentArgument); err != nil {
		t.Fatal(err)
	}
	resetStatsAbsent(t, owner, target)
	resetStatsAbsent(t, owner, missing)
	if resetStatsSharedEntryCount(&owner.Statistics, target) != entriesBefore-1 {
		t.Fatal("retired/absent named reset allocated shared records")
	}
	if snapshot.Read(&owner.Statistics, target, upper) != before || snapshot.Timestamp() != captureAt || cache.Read(&owner.Statistics, target, upper) != cacheExpected {
		t.Fatal("retirement or late reset mutated retained reader values")
	}
	snapshot.Clear()
	snapshot.Read(&owner.Statistics, target, upper)
	if statsLifetimeSnapshotContains(&snapshot, target) {
		t.Fatal("fresh capture retained retired identity")
	}
	if owner != e {
		resetStatsAbsent(t, e, projected)
	}
	assertControls()
}
