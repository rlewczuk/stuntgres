package core_test

import (
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/sqlstate"
)

func TestClusterUnpopulatedMaterializedView(t *testing.T) {
	for _, lifecycle := range []string{"created_no_data", "refresh_no_data"} {
		for _, form := range []string{"unmarked_using", "marked_using", "remembered", "bare"} {
			t.Run(lifecycle+"/"+form, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				for _, q := range []string{
					"CREATE TABLE cluster_source(k int)",
					"INSERT INTO cluster_source VALUES(2),(1)",
					"CREATE INDEX cluster_source_idx ON cluster_source(k)",
					"CREATE MATERIALIZED VIEW cluster_empty AS SELECT k FROM cluster_source WITH NO DATA",
					"CREATE INDEX cluster_empty_a ON cluster_empty(k)",
					"CREATE INDEX cluster_empty_b ON cluster_empty(k DESC)",
				} {
					enginetest.SessionSQL(t, s, q)
				}
				if lifecycle == "refresh_no_data" {
					enginetest.SessionSQL(t, s, "REFRESH MATERIALIZED VIEW cluster_empty")
					enginetest.SessionSQL(t, s, "CLUSTER cluster_empty USING cluster_empty_a")
					enginetest.SessionSQL(t, s, "REFRESH MATERIALIZED VIEW cluster_empty WITH NO DATA")
					enginetest.SessionSQL(t, s, "ALTER MATERIALIZED VIEW cluster_empty SET WITHOUT CLUSTER")
				}
				if form != "unmarked_using" {
					enginetest.SessionSQL(t, s, "ALTER MATERIALIZED VIEW cluster_empty CLUSTER ON cluster_empty_a")
				}
				parent := enginetest.CatalogRef(t, e, e.CatalogRead(), "cluster_empty").Address
				view := e.CatalogRead()
				descriptors := make(map[ids.ObjectAddress]*catalog.Relation)
				definitions := make(map[ids.ObjectAddress]catalog.IndexDefinition)
				for address, relation := range view.Relations() {
					descriptors[address] = relation
					if relation.SQLIndex != nil {
						definitions[address] = *relation.SQLIndex
					}
				}
				stores := make(map[ids.ObjectAddress]*storage.Relation)
				for address, store := range e.Storage.Relations {
					stores[address] = store
				}
				roots := make(map[ids.ObjectAddress]*storage.IndexRoot)
				for address, root := range e.Storage.IndexRoots {
					roots[address] = root
				}
				before := stores[parent]
				if before == nil || len(before.Versions) != 0 || view.MustRelation(parent).Populated {
					t.Fatal("fixture did not reach genuine unpopulated materialized storage")
				}
				generation, ends := before.Generation, before.Ends
				versions := before.Versions
				parentRevision := view.MustRelation(parent).Revision()
				query := "CLUSTER cluster_empty USING cluster_empty_b"
				if form == "remembered" {
					query = "CLUSTER cluster_empty"
				} else if form == "bare" {
					query = "CLUSTER"
				}
				// Exercise the ordinary statement/transaction publication path, not
				// a candidate helper or a manually manufactured skipped descriptor.
				enginetest.SessionSQL(t, s, query)
				after := e.CatalogRead()
				if after.MustRelation(parent).Populated || after.MustRelation(parent).Revision() != parentRevision {
					t.Error("skipped CLUSTER changed population or parent revision")
				}
				for address, descriptor := range descriptors {
					if after.MustRelation(address) != descriptor {
						t.Error("skipped CLUSTER replaced catalog descriptor", descriptor.Name())
					}
					if definition, ok := definitions[address]; ok && !reflect.DeepEqual(*after.MustRelation(address).SQLIndex, definition) {
						t.Error("skipped CLUSTER changed index definition/preference", descriptor.Name())
					}
				}
				if len(e.Storage.Relations) != len(stores) || len(e.Storage.IndexRoots) != len(roots) {
					t.Error("skipped CLUSTER changed storage membership")
				}
				for address, store := range stores {
					if e.Storage.Relations[address] != store {
						t.Error("skipped CLUSTER replaced heap backing", address)
					}
				}
				if current := e.Storage.Relations[parent]; current == nil || current.Generation != generation || current.Ends != ends || !storage.SameVersionRoot(current.Versions, versions) {
					t.Error("skipped CLUSTER changed heap generation, end root or version backing")
				}
				for address, root := range roots {
					if e.Storage.IndexRoots[address] != root {
						t.Error("skipped CLUSTER replaced index root", address)
					}
				}
				// A later genuine population must still run the original recipe and
				// preserve the old preference, rather than exposing a phantom mark.
				enginetest.SessionSQL(t, s, "REFRESH MATERIALIZED VIEW cluster_empty")
				if !e.CatalogRead().MustRelation(parent).Populated || !reflect.DeepEqual(clusterStorageIDs(e.Storage.Relations[parent]), []int64{2, 1}) {
					t.Fatal("skipped CLUSTER damaged later population or source values")
				}
				for address, definition := range definitions {
					if e.CatalogRead().MustRelation(address).SQLIndex.Clustered != definition.Clustered {
						t.Error("later refresh exposed changed remembered selection", address)
					}
				}
			})
		}
	}
}

// Public SQL admits partial B-trees and plain hash indexes separately, not
// partial hash declarations. Compose their genuine captured descriptor facts
// only in a discarded private stage to exercise shared eligibility precedence;
// this is not an execution or public-support claim for partial hash indexes.
func TestClusterUnpopulatedIndexEligibility(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{
		"CREATE TABLE cluster_source(k int)",
		"CREATE MATERIALIZED VIEW cluster_empty AS SELECT k FROM cluster_source WITH NO DATA",
		"CREATE INDEX hash_idx ON cluster_empty USING hash(k)",
		"CREATE INDEX partial_idx ON cluster_empty(k) WHERE k>0",
	} {
		enginetest.SessionSQL(t, s, q)
	}
	read := e.CatalogRead()
	parent := read.MustRelation(enginetest.CatalogRef(t, e, read, "cluster_empty").Address)
	hash := read.MustRelation(enginetest.CatalogRef(t, e, read, "hash_idx").Address)
	partial := read.MustRelation(enginetest.CatalogRef(t, e, read, "partial_idx").Address)
	originalRoot, originalStore := e.Storage.IndexRoots[hash.Address()], e.Storage.Relations[parent.Address()]
	stage := e.BeginCatalogStage(read)
	defer e.DiscardCatalogStage(stage)
	combined := *hash
	definition := *hash.SQLIndex
	definition.Predicate = partial.SQLIndex.Predicate
	combined.SQLIndex = &definition
	if err := table.BumpAlterRevision(hash, &combined); err != nil {
		t.Fatal(err)
	}
	if err := stage.Replace(&combined); err != nil {
		t.Fatal(err)
	}
	selected, err := table.ClusterIndex(stage.StageRead(), parent, hash.Name())
	var diagnostic *sqlstate.Error
	want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "0A000", Message: `cannot cluster on index "hash_idx" because access method does not support clustering`}
	if selected != nil || !errors.As(err, &diagnostic) || !reflect.DeepEqual(diagnostic, want) {
		t.Fatal("method must precede predicate eligibility", selected, err)
	}
	if e.CatalogRead().State() != read.State() || e.CatalogRead().MustRelation(hash.Address()) != hash || hash.SQLIndex.Predicate != nil || partial.SQLIndex.Predicate == nil || e.Storage.Relations[parent.Address()] != originalStore || e.Storage.IndexRoots[hash.Address()] != originalRoot {
		t.Fatal("discarded eligibility candidate changed genuine descriptors/storage")
	}
}

func TestClusterUnpopulatedLockPreservation(t *testing.T) {
	for _, tc := range []struct {
		name   string
		mode   lock.Mode
		scoped bool
	}{
		{"new_heap", 0, false},
		{"access_share", lock.AccessShare, false},
		{"share", lock.Share, false},
		{"access_exclusive", lock.AccessExclusive, false},
		{"scoped_access_share", lock.AccessShare, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			e := engine.New()
			s := enginetest.StartSession(t, e, nil)
			for _, q := range []string{
				"CREATE TABLE cluster_source(k int)",
				"CREATE MATERIALIZED VIEW cluster_empty AS SELECT k FROM cluster_source WITH NO DATA",
				"CREATE INDEX cluster_empty_a ON cluster_empty(k)",
				"CREATE INDEX cluster_empty_b ON cluster_empty(k DESC)",
				"BEGIN",
			} {
				enginetest.SessionSQL(t, s, q)
			}
			defer enginetest.SessionSQL(t, s, "ROLLBACK")
			ticket := s.Transactions.Ticket
			ref := func(name string) lock.Resource {
				return lock.Resource{ObjectRef: enginetest.CatalogRef(t, e, e.CatalogRead(), name)}
			}
			heap, selected, sibling, unrelated := ref("cluster_empty"), ref("cluster_empty_a"), ref("cluster_empty_b"), ref("cluster_source")
			if tc.scoped {
				var contribution lock.RelationContribution
				request, err := e.Locks.StartAcquireRelation(t.Context(), ticket, heap, tc.mode, lock.Policy{}, &contribution)
				if err != nil || request != nil {
					t.Fatal("unexpected scoped heap wait", request, err)
				}
				defer e.Locks.ReleaseRelation(&contribution)
			} else if tc.mode != 0 {
				if err := e.Locks.Acquire(t.Context(), ticket, heap, tc.mode); err != nil {
					t.Fatal(err)
				}
			}
			for resource, mode := range map[lock.Resource]lock.Mode{selected: lock.AccessShare, sibling: lock.AccessShare, unrelated: lock.Share} {
				if err := e.Locks.Acquire(t.Context(), ticket, resource, mode); err != nil {
					t.Fatal(err)
				}
			}
			before := e.Locks.Checkpoint(ticket)
			enginetest.SessionSQL(t, s, "CLUSTER cluster_empty USING cluster_empty_a")
			after := e.Locks.Checkpoint(ticket)
			if !reflect.DeepEqual(after[heap], before[heap]) {
				t.Error("skip did not restore exact earlier heap ordinary modes", before[heap], after[heap])
			}
			for resource, previous := range before {
				expected := previous
				if !reflect.DeepEqual(after[resource], expected) {
					t.Error("skip changed an unrelated/prior contribution", resource, expected, after[resource])
				}
			}
			if after[selected].Mode != lock.AccessShare || after[sibling].Mode != lock.AccessShare || after[unrelated].Mode != lock.Share {
				t.Error("skip changed prior index or unrelated modes", after)
			}
			actual := lock.Mode(0)
			if entry := e.Locks.Inspect(ticket).Resources[heap]; entry != nil {
				actual = entry.Holders[ticket].Mode
			}
			if actual != tc.mode {
				t.Error("skip lost scoped ownership or retained its new heap AX", actual, tc.mode)
			}
		})
	}
}
