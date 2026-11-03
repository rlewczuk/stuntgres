package core_test

import (
	"bytes"
	"context"
	"reflect"
	"testing"
	"time"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/statistics"
)

func sharedStatsBackendSizes(t *testing.T, p *statistics.Provider, live, facts int) {
	t.Helper()
	state := reflect.ValueOf(p).Elem()
	if got := state.FieldByName("liveBackends").Len(); got != live {
		t.Fatalf("backend registrations: got %d, want live baseline %d", got, live)
	}
	if got := state.FieldByName("backends").Len(); got != facts {
		t.Fatalf("backend facts: got %d, want current participants %d", got, facts)
	}
}

func sharedStatsBackendRegistrationChurn(t *testing.T) {
	e, s, peer, ps, _, _ := sharedStatsFixture(t)
	sharedStatsBackend(e, s, 11, 13)
	sharedStatsBackend(peer, ps, 17, 19)
	a, _ := e.Statistics.ReadBackend(s.Token())
	b, _ := peer.Statistics.ReadBackend(ps.Token())
	providers := [2]*statistics.Provider{&e.Statistics, &peer.Statistics}
	for _, p := range providers {
		sharedStatsBackendSizes(t, p, 2, 1)
	}
	var retainedA, retainedB statistics.Reader
	retainedA.Configure("snapshot", 1)
	retainedB.Configure("snapshot", 1)
	at := time.Unix(100, 0)
	retainedA.ReadBackend(&e.Statistics, s.Token(), at)
	retainedB.ReadBackend(&peer.Statistics, ps.Token(), at)
	checkLive := func() {
		t.Helper()
		for _, p := range providers {
			sharedStatsBackendSizes(t, p, 2, 1)
		}
		if current, _ := e.Statistics.ReadBackend(s.Token()); current != a {
			t.Fatal("churn changed independent live A", current, a)
		}
		if current, _ := peer.Statistics.ReadBackend(ps.Token()); current != b {
			t.Fatal("churn changed independent live B", current, b)
		}
		if cached, _ := retainedA.ReadBackend(&e.Statistics, s.Token(), at); cached != a {
			t.Fatal("churn changed retained A")
		}
		if cached, _ := retainedB.ReadBackend(&peer.Statistics, ps.Token(), at); cached != b {
			t.Fatal("churn changed retained B")
		}
	}
	// Failed initialization may carry a token equal to an existing session's
	// requested ID, but it never owns that session's published registration.
	failed, err := enginetest.OpenSession(context.Background(), e, session.SessionStart{ID: s.Token().ID()})
	if err == nil || failed != nil {
		if failed != nil {
			statsFunctionRetire(t, failed)
		}
		t.Fatal("duplicate startup unexpectedly acquired live authority", err)
	}
	checkLive()
	for range 16 {
		func() {
			private, err := e.StartRestoreSession(context.Background(), e.Roles().LookupRef("postgres"))
			if err != nil {
				t.Fatal(err)
			}
			defer statsFunctionRetire(t, private)
			if private.Token().Private() == nil {
				t.Fatal("private maintenance identity absent")
			}
			for _, p := range providers {
				var w statistics.Writer
				w.Backend(p, private.Token(), statistics.Fact{Operations: 9}, statistics.Fact{Operations: 8})
				if w.Pending() {
					t.Fatal("private owner acquired reporting authority")
				}
			}
			statsFunctionRetire(t, private)
			checkLive()
			// Public TEMP objects with a collation are delegated to actual
			// private maintenance. Namespace tuples intentionally survive exit.
			actor := enginetest.StartSession(t, e, nil)
			statsFunctionExec(t, actor,
				"CREATE TEMP TABLE shared_private_lifetime(v integer)",
				`CREATE COLLATION pg_temp.shared_private_collation FROM pg_catalog."C"`)
			var namespace ids.ObjectAddress
			for _, candidate := range e.CatalogRead().TemporaryNamespaces() {
				if candidate.Temporary == actor.Token() && !candidate.TemporaryToast {
					namespace = candidate.Address()
				}
			}
			if namespace == (ids.ObjectAddress{}) {
				t.Fatal("public TEMP owner did not create its actual namespace")
			}
			before := e.CatalogRead()
			var table, collation ids.ObjectAddress
			for address, descriptor := range before.Descriptors() {
				if descriptor.Namespace() != namespace {
					continue
				}
				if address.ClassID == ids.RelationClassID && descriptor.Name() == "shared_private_lifetime" {
					table = address
				}
				if address.ClassID == ids.CollationClassID && descriptor.Name() == "shared_private_collation" {
					collation = address
				}
			}
			if table == (ids.ObjectAddress{}) || collation == (ids.ObjectAddress{}) {
				t.Fatal("actual private maintenance workload lacks its TEMP table or collation")
			}
			statsFunctionRetire(t, actor)
			for _, address := range []ids.ObjectAddress{table, collation} {
				if e.CatalogRead().Get(address) != nil {
					t.Fatal("actual private maintenance retained an owned TEMP object", address)
				}
				if before.Get(address) == nil {
					t.Fatal("cleanup mutated retained pre-end catalog backing", address)
				}
			}
			e.Mu.Lock()
			retainedStorage := e.Storage.Relations[table] != nil
			activeNamespace := actor.TemporaryNamespace
			e.Mu.Unlock()
			if retainedStorage || activeNamespace != (ids.ObjectAddress{}) {
				t.Fatal("private cleanup retained physical table storage or active namespace authority")
			}
			enginetest.AssertTemporaryNamespacePair(t, e, actor.Token(), namespace)
			checkLive()
		}()
	}
	var ended ids.SessionToken
	for i := range 32 {
		target := e
		if i%2 != 0 {
			target = peer
		}
		actor := enginetest.StartSession(t, target, nil)
		ended = actor.Token()
		for _, p := range providers {
			sharedStatsBackendSizes(t, p, 3, 1)
		}
		var pending [2]statistics.Writer
		if i%2 == 0 {
			for j, p := range providers {
				pending[j].Backend(p, ended, statistics.Fact{Operations: 11}, statistics.Fact{Operations: 13})
				pending[j].Flush(p, at, true)
				pending[j].Backend(p, ended, statistics.Fact{Operations: 3}, statistics.Fact{Operations: 4})
				if !pending[j].Pending() {
					t.Fatal("actual live report was not pending")
				}
			}
		}
		statsFunctionRetire(t, actor)
		for j, p := range providers {
			pending[j].Flush(p, at.Add(time.Second), false)
			pending[j].Backend(p, ended, statistics.Fact{Operations: 7}, statistics.Fact{Operations: 9})
			if pending[j].Pending() {
				t.Fatal("late actual-session report re-enrolled ended token")
			}
			if _, exists := p.ReadBackend(ended); exists {
				t.Fatal("ended token retained published facts")
			}
		}
		checkLive()
	}
	actor := enginetest.StartSession(t, e, nil)
	ended = actor.Token()
	var replacementPending [2]statistics.Writer
	for j, p := range providers {
		replacementPending[j].Backend(p, ended, statistics.Fact{Operations: 3}, statistics.Fact{Operations: 4})
		if !replacementPending[j].Pending() {
			t.Fatal("pre-end replacement report was not pending")
		}
	}
	statsFunctionRetire(t, actor)
	checkLive()
	// Native replacement removes history but preserves only actual live authority.
	// The old ended token remains invalid without any historical negative map.
	var dump bytes.Buffer
	if err := engine.Dump(context.Background(), e, &dump); err != nil {
		t.Fatal(err)
	}
	for _, replace := range []func() error{
		func() error { return engine.Reset(context.Background(), e) },
		func() error { return engine.Restore(context.Background(), e, bytes.NewReader(dump.Bytes())) },
	} {
		if err := replace(); err != nil {
			t.Fatal(err)
		}
		for j, p := range providers {
			replacementPending[j].Flush(p, at, true)
			var late statistics.Writer
			late.Backend(p, ended, statistics.Fact{Operations: 23}, statistics.Fact{Operations: 29})
			late.Flush(p, at, true)
			if late.Pending() {
				t.Fatal("replacement admitted ended report")
			}
			if _, exists := p.ReadBackend(ended); exists {
				t.Fatal("replacement recreated ended backend")
			}
		}
		sharedStatsBackendSizes(t, &e.Statistics, 2, 0)
		sharedStatsBackendSizes(t, &peer.Statistics, 2, 1)
		if current, _ := peer.Statistics.ReadBackend(ps.Token()); current != b {
			t.Fatal("replacement changed active peer database")
		}
		if cached, _ := retainedA.ReadBackend(&e.Statistics, s.Token(), at); cached != a {
			t.Fatal("replacement changed retained backend snapshot")
		}
	}
	sharedStatsBackend(e, s, 5, 6)
	if current, ok := e.Statistics.ReadBackend(s.Token()); !ok || current.IO.Operations != 5 || current.WAL.Operations != 6 {
		t.Fatal("native replacement lost actual live registration", current, ok)
	}
}

func sharedStatsBackendTargetInheritance(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	sharedStatsBackend(e, s, 11, 13)
	statsFunctionExec(t, s, "CREATE DATABASE shared_backend_inheritance")
	target, err := e.SelectDatabase(context.Background(), "shared_backend_inheritance")
	if err != nil {
		t.Fatal(err)
	}
	sharedStatsBackendSizes(t, &target.Statistics, 1, 0)
	if _, exists := target.Statistics.ReadBackend(s.Token()); exists {
		t.Fatal("new target inherited backend monitoring history")
	}
	sharedStatsBackend(target, s, 17, 19)
	if f, ok := target.Statistics.ReadBackend(s.Token()); !ok || f.IO.Operations != 17 || f.WAL.Operations != 19 || !f.ResetAt.IsZero() {
		t.Fatal("actual target did not inherit positive authority only", f, ok)
	}
	resetStatsExec(t, s, "SELECT pg_stat_reset_backend_stats(pg_backend_pid())")
	for _, p := range []*statistics.Provider{&e.Statistics, &target.Statistics} {
		if f, ok := p.ReadBackend(s.Token()); !ok || f.IO.Operations != 0 || f.WAL.Operations != 0 || f.ResetAt.IsZero() {
			t.Fatal("inherited-partition PID reset", f, ok)
		}
	}
	statsFunctionRetire(t, s)
	for _, p := range []*statistics.Provider{&e.Statistics, &target.Statistics} {
		sharedStatsBackendSizes(t, p, 0, 0)
	}
}
