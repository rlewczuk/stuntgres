package engine

import (
	"context"
	"fmt"
	"testing"
	"time"
)

func TestExclusionIndexRoots(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, fmt.Sprintf("CREATE TABLE t(v INT, active BOOL, CONSTRAINT ex EXCLUDE USING %s ((v+0) WITH =) WHERE(active))", method))
			sessionSQL(t, s, "INSERT INTO t VALUES(1,TRUE),(2,FALSE),(NULL,TRUE),(3,TRUE)")
			parent := e.catalog.state.MustRelation(roleTableRef(t, e, "public.t").Address)
			constraint := parent.Constraints.Exclusion[0]
			index := e.catalog.state.MustRelation(constraint.Index)
			root := e.storage.IndexRoots[constraint.Index]
			if root == nil || index.SQLIndex.Unique || !index.SQLIndex.IsExclusion() || !backingConstraintPresent(parent, index) {
				t.Fatal("exclusion does not own a real nonunique enforcement root")
			}
			metadata, _, err := projectIndexMetadata(context.Background(), e.CatalogRead(), e.catalog.Domain.Ref(e.catalog.state.Epoch(), index.Address()))
			if err != nil || !metadata.IndIsExclusion || metadata.IndIsUnique || !metadata.IndImmediate || !metadata.IndIsReady || !metadata.IndIsValid || !metadata.IndIsLive || len(metadata.IndKey) != 1 || metadata.IndKey[0] != 0 {
				t.Fatal("incorrect native exclusion index metadata", metadata, err)
			}
			if method == "btree" && len(root.Membership) != 3 {
				t.Fatal("B-tree predicate membership must include qualifying NULL rows", root.Membership)
			}
			if method == "btree" {
				if root.Hash != nil || len(root.Entries) != 3 {
					t.Fatal("wrong B-tree root", root)
				}
			} else {
				if root.Hash == nil || len(root.Entries) != 0 {
					t.Fatal("wrong HASH root", root)
				}
				entries := 0
				for _, bucket := range root.Hash.Buckets {
					entries += len(bucket)
				}
				if entries != 2 {
					t.Fatal("HASH must omit NULL keys", entries)
				}
				if root.Membership != nil {
					t.Fatal("HASH must retain its existing fallback membership authority")
				}
			}
			for _, entry := range root.Entries {
				if entry.Row == 0 || entry.Version == 0 {
					t.Fatal("unminted root identity", entry)
				}
			}
			if err := validateExclusions(e.CatalogRead(), parent, make(map[string]bool)); err != nil {
				t.Fatal(err)
			}
			if len(e.exclusionPending) != 0 {
				t.Fatal("implicit publication retained dirty rows")
			}
			uniqueClaimsEmpty(t, e)
		})
	}
}

func TestExclusionCatalogLifecycle(t *testing.T) {
	e := New()
	s := sessionStart(t, e, nil)
	sessionSQL(t, s, "CREATE TABLE t(id INT UNIQUE,v TEXT,active BOOL,CONSTRAINT ex EXCLUDE USING btree(lower(v) WITH =) INCLUDE(active) WHERE(active) DEFERRABLE INITIALLY DEFERRED)")
	sessionSQL(t, s, "INSERT INTO t VALUES(1,'A',TRUE)")
	ref := roleTableRef(t, e, "public.t")
	original := e.catalog.state.MustRelation(ref.Address)
	old := original.Constraints.Exclusion[0]
	oldRoot := e.storage.IndexRoots[old.Index]
	metadata, _, err := projectIndexMetadata(context.Background(), e.CatalogRead(), e.catalog.Domain.Ref(e.catalog.state.Epoch(), old.Index))
	if err != nil || !metadata.IndIsExclusion || metadata.IndIsUnique || metadata.IndImmediate || len(metadata.IndKey) != 2 || metadata.IndKey[0] != 0 || metadata.IndKey[1] != int16(original.Attributes[2]) {
		t.Fatal("incorrect deferred definition/INCLUDE metadata", metadata, err)
	}
	sessionSQL(t, s, "ALTER TABLE t ALTER COLUMN v TYPE VARCHAR(20)")
	parent := e.catalog.state.MustRelation(ref.Address)
	current := parent.Constraints.Exclusion[0]
	if current.OID == old.OID || current.Index == old.Index || original.Constraints.Exclusion[0].OID != old.OID {
		t.Fatal("TYPE must remint the exclusion closure without mutating its old generation")
	}
	if e.catalog.state.Get(old.Index) != nil || e.storage.IndexRoots[old.Index] != nil || e.storage.IndexRoots[current.Index] == oldRoot {
		t.Fatal("TYPE retained the retired physical index")
	}
	index := e.catalog.state.MustRelation(current.Index)
	if !backingConstraintPresent(parent, index) {
		t.Fatal("TYPE broke reciprocal ownership")
	}
	root := e.storage.IndexRoots[current.Index]
	sessionSQL(t, s, "ALTER TABLE t RENAME CONSTRAINT ex TO renamed")
	parent = e.catalog.state.MustRelation(ref.Address)
	renamed := parent.Constraints.Exclusion[0]
	if renamed.OID != current.OID || renamed.Index != current.Index || renamed.Name != "renamed" || e.storage.IndexRoots[current.Index] != root {
		t.Fatal("rename changed constraint identity or the integrity root")
	}
	index = e.catalog.state.MustRelation(current.Index)
	if index.Name() != "renamed" || !backingConstraintPresent(parent, index) {
		t.Fatal("rename broke index attachment")
	}
	if !renamed.Deferrable || !renamed.InitiallyDeferred {
		t.Fatal("rename dropped definition properties")
	}
	if _, err := CollectDepend(context.Background(), e.CatalogRead(), engineCatalogRuntime{}); err != nil {
		t.Fatal(err)
	}
	sessionSQL(t, s, "ALTER TABLE t DROP CONSTRAINT renamed")
	parent = e.catalog.state.MustRelation(ref.Address)
	if len(parent.Constraints.Exclusion) != 0 || len(parent.Constraints.Unique) != 1 || e.catalog.state.Get(current.Index) != nil || e.storage.IndexRoots[current.Index] != nil {
		t.Fatal("DROP did not retire exactly the exclusion attachment")
	}
	uniqueClaimsEmpty(t, e)
}

func TestExclusionStrongRowKeys(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(k INT,CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =))")
			op := transactionOperation(t, s)
			defer op.Done()
			scope, err := e.StartStatement(op)
			if err != nil {
				t.Fatal(err)
			}
			defer scope.Finish(nil)
			if err := scope.gate(statement(t, "INSERT INTO t VALUES(1)")); err != nil {
				t.Fatal(err)
			}
			binding, err := scope.bindUniqueWrite(roleTableRef(t, e, "public.t"))
			if err != nil {
				t.Fatal(err)
			}
			if len(binding.keys) != 1 || len(binding.keySlots) != 0 {
				t.Fatal("exclusion strengthened KEY SHARE/NO KEY UPDATE row semantics", binding.keySlots)
			}
		})
	}
}

func TestExclusionAlterFirstIndexPublication(t *testing.T) {
	for _, method := range []string{"btree", "hash"} {
		t.Run(method, func(t *testing.T) {
			e := New()
			s := sessionStart(t, e, nil)
			sessionSQL(t, s, "CREATE TABLE t(k INT)")
			sessionSQL(t, s, "INSERT INTO t VALUES(1)")
			ref := roleTableRef(t, e, "public.t")
			original := e.catalog.state.MustRelation(ref.Address)
			store := e.storage.Relations[ref.Address]
			row := store.Versions[0]
			if len(e.storage.IndexRoots) != 0 {
				t.Fatal("fixture must add the first actual index")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			op, err := s.Acquire(ctx)
			if err != nil {
				t.Fatal(err)
			}
			result, err := e.Execute(op, statement(t, "ALTER TABLE t ADD CONSTRAINT ex EXCLUDE USING "+method+"(k WITH =)"), 0, nil)
			op.Done()
			if err != nil || result.Count != 0 || result.Rows != nil {
				t.Fatal("first-index publication failed", result, err)
			}
			parent := e.catalog.state.MustRelation(ref.Address)
			if len(parent.Constraints.Exclusion) != 1 || !parent.History.HasIndexes || len(original.Constraints.Exclusion) != 0 || store.Versions[0].Row != row.Row || store.Versions[0].Version != row.Version {
				t.Fatal("ALTER did not preserve its immutable predecessor and actual attachment")
			}
			constraint := parent.Constraints.Exclusion[0]
			index := e.catalog.state.MustRelation(constraint.Index)
			root := e.storage.IndexRoots[constraint.Index]
			if !backingConstraintPresent(parent, index) || root == nil {
				t.Fatal("missing actual first exclusion root")
			}
			if method == "btree" {
				if len(root.Entries) != 1 || root.Entries[0].Row != row.Row || root.Entries[0].Version != row.Version {
					t.Fatal("B-tree lost predecessor row/version")
				}
			} else {
				if root.Hash == nil {
					t.Fatal("missing HASH provider root")
				}
				count := 0
				for _, bucket := range root.Hash.Buckets {
					for _, entry := range bucket {
						count++
						if entry.Row != row.Row || entry.Version != row.Version {
							t.Fatal("HASH lost predecessor row/version")
						}
					}
				}
				if count != 1 {
					t.Fatal("wrong HASH first-index membership", count)
				}
			}
			if len(e.exclusionPending) != 0 {
				t.Fatal("ALTER publication retained dirty ownership")
			}
			uniqueClaimsEmpty(t, e)
		})
	}
}
