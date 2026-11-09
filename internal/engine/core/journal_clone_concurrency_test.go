package core_test

import (
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/engine/storage"
)

// This exercises the actual retained owner visited by foreign GIN maintenance,
// not a synthetic slot or a timing workload. The start channel precedes both
// cache accesses; the completion channel is received only after maintenance.
// Thus the detached Clone and maintenance's cache capture have no test-created
// happens-before edge, even if one goroutine finishes first.
func TestJournalCloneConcurrentGINOwner(t *testing.T) {
	for _, change := range []string{"cache-hit", "base-change", "invalidate"} {
		t.Run(change, func(t *testing.T) {
			e := engine.New()
			a, b := enginetest.StartSession(t, e, nil), enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE FUNCTION gin_clone_keys(integer[]) RETURNS integer[] IMMUTABLE RETURN $1",
				"CREATE TABLE gin_cohort(id integer,a integer[])",
				"CREATE INDEX gin_cohort_i ON gin_cohort USING gin(gin_clone_keys(a))",
				"INSERT INTO gin_cohort VALUES(1,'{2,3}')",
				"BEGIN",
				"CREATE SCHEMA clone_private",
				"CREATE OR REPLACE FUNCTION gin_clone_keys(integer[]) RETURNS integer[] IMMUTABLE LANGUAGE plpgsql AS $$BEGIN RETURN $1; END$$",
				"ALTER OPERATOR CLASS pg_catalog.array_ops USING gin RENAME TO gin_clone_array",
			} {
				enginetest.SessionSQL(t, a, q)
			}
			target := ginCoreRelation(t, e, "gin_cohort_i")
			a.Transactions.Mu.Lock()
			e.Mu.Lock()
			journal := a.Transactions.Catalog
			before := e.PrivateCatalogLocked(journal)
			base := e.Catalog.State
			version := before.CaptureDescriptorVersion()
			mark, serial := journal.DescriptorEditMark(), journal.DescriptorEditSerial()
			candidate := index.JournalIndexesOrZero(journal).Candidates[target.Address()]
			store := e.Storage.Relations[target.SQLIndex.Parent]
			generation := store.Generation
			var private *catalog.Namespace
			for _, descriptor := range before.Descriptors() {
				if schema, ok := descriptor.(*catalog.Namespace); ok && schema.Name() == "clone_private" {
					private = schema
				}
			}
			e.Mu.Unlock()
			a.Transactions.Mu.Unlock()
			if private == nil || candidate.Root == nil || candidate.Root.Gin == nil || storage.GinPendingIncidences(candidate.Root.Gin) == 0 {
				t.Fatal("missing genuine private descriptor and pending routine-source candidate")
			}
			if e.CatalogRead().Get(private.Address()) != nil {
				t.Fatal("private authority was already published")
			}
			if change == "base-change" {
				enginetest.SessionSQL(t, b, "CREATE SCHEMA clone_peer")
			}

			ready, cloned := make(chan struct{}), make(chan *session.Journal, 1)
			go func() {
				close(ready)
				cloned <- journal.Clone()
			}()
			joined := false
			t.Cleanup(func() {
				if !joined {
					<-cloned
				}
			})
			<-ready
			if change == "invalidate" {
				e.Mu.Lock()
				journal.InvalidatePrivateRead()
				e.Mu.Unlock()
			}
			ginCoreClean(t, b)
			snapshot := <-cloned
			joined = true

			a.Transactions.Mu.Lock()
			e.Mu.Lock()
			next := a.Transactions.Catalog
			after := e.PrivateCatalogLocked(next)
			nextCandidate := index.JournalIndexesOrZero(next).Candidates[target.Address()]
			current := e.Catalog.Current()
			roots, stores := e.Storage.IndexRoots, e.Storage.Relations
			sameStore := stores[target.SQLIndex.Parent] == store && store.Generation == generation
			e.Mu.Unlock()
			a.Transactions.Mu.Unlock()
			if next == journal || nextCandidate.Root == nil || nextCandidate.Root.Gin == nil || nextCandidate.Root == candidate.Root || storage.GinPendingIncidences(nextCandidate.Root.Gin) != 0 || storage.GinPendingIncidences(candidate.Root.Gin) == 0 {
				t.Fatal("cleanup failed to COW-transport the actual owner candidate")
			}
			if nextCandidate.Storage != candidate.Storage || nextCandidate.Generation != candidate.Generation || nextCandidate.Ends != candidate.Ends || nextCandidate.Length != candidate.Length || !sameStore {
				t.Fatal("cache synchronization changed retained physical authority or generation")
			}
			if len(candidate.Root.Entries) == 0 || len(nextCandidate.Root.Entries) == 0 || &candidate.Root.Entries[0] != &nextCandidate.Root.Entries[0] {
				t.Fatal("cleanup copied or discarded the retained membership backing")
			}
			if !index.RoutineSourcesMatch(map[ids.ObjectAddress]index.RootCandidate{target.Address(): nextCandidate}, current, roots, stores) || index.RoutineSourcesMatch(map[ids.ObjectAddress]index.RootCandidate{target.Address(): candidate}, current, roots, stores) {
				t.Fatal("cleanup lost live receipt authority or accepted the detached predecessor")
			}
			if snapshot.DescriptorEditMark() != mark || snapshot.DescriptorEditSerial() != serial || next.DescriptorEditMark() != mark || next.DescriptorEditSerial() != serial || version.Get(private.Address()) != private || before.Get(private.Address()) != private || after.Get(private.Address()) != private || current.Get(private.Address()) != nil {
				t.Fatal("cache capture changed immutable private history, backing or publication lifetime")
			}
			if change == "cache-hit" && (!version.Matches(after) || after.State() != before.State()) {
				t.Fatal("unchanged cache capture rebuilt the retained private generation")
			}
			if !version.Matches(before) || !version.SameAuthority(after) {
				t.Fatal("retained generation or owner/domain/epoch authority changed")
			}
			if change != "cache-hit" && after.State() == before.State() {
				t.Fatal("base change or invalidation reused a stale projection")
			}
			if change == "base-change" {
				var peer *catalog.Namespace
				for _, descriptor := range current.Descriptors() {
					if schema, ok := descriptor.(*catalog.Namespace); ok && schema.Name() == "clone_peer" {
						peer = schema
					}
				}
				if peer == nil || current.State() == base || before.Get(peer.Address()) != nil || after.Get(peer.Address()) != peer || version.Matches(after) {
					t.Fatal("committed-base replacement lost its real descriptor delta")
				}
			}
			// The clone may capture either side of a concurrent rebuild or the
			// fully invalidated slot, never a torn base/projection pair.
			validCache := snapshot.CachedBase == base && snapshot.Cached.State() == before.State()
			validCache = validCache || snapshot.CachedBase == current.State() && snapshot.Cached.State() == after.State()
			validCache = validCache || change == "invalidate" && snapshot.CachedBase == nil && snapshot.Cached.State() == nil
			if !validCache || snapshot.Cached.State() != nil && snapshot.Cached.Owner() != before.Owner() {
				t.Fatal("detached clone captured torn or foreign cached authority")
			}
			snapshot.RecordDescriptor(current.State(), private.Address(), nil)
			if snapshot.PrivateRead(current.Owner(), current.State()).Get(private.Address()) != nil || before.Get(private.Address()) != private || after.Get(private.Address()) != private {
				t.Fatal("child invalidation mutated a retained parent generation")
			}
			enginetest.SessionSQL(t, a, "COMMIT")
			if e.CatalogRead().Get(private.Address()) != private {
				t.Fatal("ordinary commit did not publish the retained private descriptor")
			}
			ginCorePending(t, e, ginCoreRelation(t, e, "gin_cohort_i"), 0, 1)
		})
	}
}
