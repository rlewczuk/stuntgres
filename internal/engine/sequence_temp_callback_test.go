package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/session"
)

func TestSequencePreparedPeerTemporaryNamespace(t *testing.T) {
	for _, scenario := range []string{"Commit", "Rollback", "ExistingNamespace", "Exhaustion"} {
		t.Run(scenario, func(t *testing.T) {
			e := New()
			a, b := sessionStart(t, e, nil), sessionStart(t, e, nil)
			if scenario == "ExistingNamespace" {
				sessionSQL(t, b, "CREATE TEMP SEQUENCE prior_temp")
			}
			for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE TABLE public.sentinel(v INT)", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
				sessionSQL(t, a, q)
			}
			if scenario == "Exhaustion" {
				state := catalog.CloneState(e.Catalog.State)
				state.SetNamespaceGeneration(^uint64(0))
				e.Catalog.PublishLocked(state)
			}
			ast, description := sequencePreparedDescription(t, e, a, "SELECT nextval('s')")
			held := func(want int64) {
				t.Helper()
				r, err := sequencePreparedExecute(e, a, ast, &description)
				if err != nil {
					t.Fatal(err)
				}
				got := tuples(r)
				r.Rows.Close()
				if len(got) != 1 || got[0][0] != want {
					t.Fatal("peer namespace identity", got, want)
				}
			}
			held(10)
			sessionSQL(t, a, "CREATE SEQUENCE a.s START 100 CACHE 1")
			held(11)
			before := e.Catalog.State
			sessionSQL(t, b, "BEGIN")
			sessionSQL(t, b, "CREATE TEMP SEQUENCE unrelated_temp")
			e.Mu.Lock()
			private := e.PrivateCatalogLocked(b.Transactions.Catalog).State()
			e.Mu.Unlock()
			if scenario == "Rollback" {
				sessionSQL(t, b, "ROLLBACK")
			} else if scenario == "Exhaustion" {
				sessionSQL(t, b, "INSERT INTO public.sentinel VALUES(7)")
				op, err := b.Acquire(context.Background())
				if err != nil {
					t.Fatal(err)
				}
				_, err = e.Execute(op, statement(t, "COMMIT"), 0, nil)
				op.Done()
				if err == nil || err.Error() != "stuntgres: namespace invalidation generation exhausted" {
					t.Fatal("exhausted namespace accepted combined publication", err)
				}
				if e.Catalog.State != before || b.TemporaryNamespace != (ids.ObjectAddress{}) {
					t.Fatal("failed commit published catalog/session namespace")
				}
				if got := tuples(sessionSQL(t, a, "SELECT v FROM public.sentinel")); len(got) != 0 {
					t.Fatal("failed namespace commit published rows", got)
				}
			} else {
				sessionSQL(t, b, "COMMIT")
			}
			if before.NamespaceGeneration() != private.NamespaceGeneration() {
				t.Fatal("publication mutated retained committed or private catalog")
			}
			if scenario == "Commit" {
				held(100)
				sequenceScalar(t, a, "SELECT last_value FROM b.s", 11)
				sequenceScalar(t, a, "SELECT last_value FROM a.s", 100)
				for _, name := range []string{"a.s", "b.s"} {
					got := tuples(sessionSQL(t, a, "SELECT is_called FROM "+name))
					if len(got) != 1 || got[0][0] != true {
						t.Fatal("called state", name, got)
					}
				}
				sequenceScalar(t, a, "SELECT nextval('s')", 101)
				sequenceScalar(t, a, "SELECT nextval('b.s')", 12)
			} else {
				held(12)
				sequenceScalar(t, a, "SELECT last_value FROM b.s", 12)
				sequenceScalar(t, a, "SELECT nextval('s')", 100)
				sequenceScalar(t, a, "SELECT nextval('b.s')", 13)
			}
		})
	}
}

func TestSequenceTemporaryRetirementPreservesPeerAnalysis(t *testing.T) {
	e := New()
	a, b, c := sessionStart(t, e, nil), sessionStart(t, e, nil), sessionStart(t, e, nil)
	for _, q := range []string{"CREATE SCHEMA a", "CREATE SCHEMA b", "CREATE SEQUENCE b.s START 10 CACHE 1", "SET search_path TO a,b"} {
		sessionSQL(t, a, q)
	}
	sessionSQL(t, b, "CREATE TEMP SEQUENCE owned_temp START 30")
	sessionSQL(t, c, "CREATE TEMP SEQUENCE peer_temp START 40")
	state := catalog.CloneState(e.Catalog.State)
	state.SetNamespaceGeneration(^uint64(0))
	state.SetEpoch(ids.CatalogEpoch(^uint64(0)))
	e.Catalog.PublishLocked(state)
	_, bd := sequencePreparedDescription(t, e, b, "SELECT nextval('owned_temp')")
	owned := bd.Sequences.Constants[0].Ref.Address
	namespace := b.TemporaryNamespace
	retained := e.CatalogRead()
	generators, storage := e.Storage.Sequences, e.Storage.Relations
	snapshot := sessionSQL(t, b, "SELECT last_value FROM owned_temp")
	ast, description := sequencePreparedDescription(t, e, a, "SELECT nextval('s')")
	held := func(want int64) {
		t.Helper()
		r, err := sequencePreparedExecute(e, a, ast, &description)
		if err != nil {
			t.Fatal(err)
		}
		got := tuples(r)
		r.Rows.Close()
		if len(got) != 1 || got[0][0] != want {
			t.Fatal("retirement invalidated peer capture", got, want)
		}
	}
	held(10)
	sessionSQL(t, a, "CREATE SEQUENCE a.s START 100 CACHE 1")
	held(11)
	b.RequestEnd(session.CleanClose)
	if err := b.WaitEnd(context.Background()); err != nil {
		t.Fatal("terminal authority prevented physical cleanup", err)
	}
	if e.Catalog.State.Epoch() != state.Epoch() || e.Catalog.State.NamespaceGeneration() != state.NamespaceGeneration() {
		t.Fatal("logical retirement changed global callback or epoch authority")
	}
	assertTemporaryNamespacePair(t, e, b.SessionToken, namespace)
	if _, ok := e.Catalog.State.Find(owned); ok || e.Storage.Sequences[owned] != nil || e.Storage.Relations[owned] != nil {
		t.Fatal("ended session leaked temporary relation or generator")
	}
	if _, ok := retained.Descriptor(owned); !ok || generators[owned] == nil || len(storage) != len(e.Storage.Relations) {
		t.Fatal("cleanup mutated retained catalog or generator map")
	}
	got := tuples(snapshot)
	if len(got) != 1 || got[0][0] != int64(30) {
		t.Fatal("cleanup lost retained sequence row", got)
	}
	held(12)
	sequenceScalar(t, a, "SELECT nextval('s')", 100)
	sequenceScalar(t, c, "SELECT nextval('peer_temp')", 40)
}
