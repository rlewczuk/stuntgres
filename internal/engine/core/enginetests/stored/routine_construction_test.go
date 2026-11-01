package stored_test

import (
	"context"
	"reflect"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/index"
	"stuntgres/internal/engine/storage"
	"testing"
)

func TestRoutineIndexPhysicalRecipeProvenance(t *testing.T) {
	e := engine.New()
	s := enginetest.StartSession(t, e, nil)
	for _, q := range []string{"CREATE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN $1+10", "CREATE TABLE construction_rows(v integer)", "INSERT INTO construction_rows VALUES(1),(2)", "CREATE INDEX construction_changed ON construction_rows(construction_key(v))", "CREATE INDEX construction_plain ON construction_rows(v)"} {
		enginetest.SessionSQL(t, s, q)
	}
	view := e.CatalogRead()
	parent := transformStoredRelation(t, view, "construction_rows")
	changed := transformStoredRelation(t, view, "construction_changed")
	plain := transformStoredRelation(t, view, "construction_plain")
	root := e.Storage.IndexRoots[changed.Address()]
	originalConstruction := root.Construction
	if originalConstruction == nil || !index.ConstructionMatches(root, changed) {
		t.Fatal("full build omitted actual recipe")
	}
	transport := func(read catalog.Read, idx *catalog.Relation, physical *storage.IndexRoot) *storage.IndexRoot {
		t.Helper()
		store := e.Storage.Relations[parent.Address()]
		snapshot := index.Snapshot{Sources: map[ids.ObjectAddress]index.SnapshotSource{idx.Address(): {Index: idx, Parent: parent, Store: *store, Root: physical, Frontier: ids.CommitSeq(^uint64(0))}}}
		target := transformStoredRelation(t, read, idx.Name())
		targetParent := read.MustRelation(target.SQLIndex.Parent)
		snapshot.CaptureMapping(map[ids.ObjectAddress]ids.ObjectAddress{idx.Address(): target.Address(), parent.Address(): targetParent.Address()}, nil)
		positions := make(map[index.RowIdentity]int, len(store.Versions))
		for i, row := range store.Versions {
			positions[index.RowIdentity{Row: row.Row, Version: row.Version}] = i
		}
		roots, err := snapshot.TransportRows(context.Background(), read, map[ids.ObjectAddress]*storage.Relation{targetParent.Address(): store}, map[ids.ObjectAddress]map[index.RowIdentity]int{targetParent.Address(): positions}, true)
		if err != nil {
			t.Fatal(err)
		}
		return roots[target.Address()]
	}
	imported := transport(view, changed, root)
	if !index.ConstructionMatches(imported, changed) || imported.Construction == originalConstruction {
		t.Fatal("unchanged transport lost structural mapping provenance")
	}
	destination := engine.New()
	for _, q := range []string{"CREATE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN $1+10", "CREATE TABLE construction_rows(v integer)", "CREATE INDEX construction_changed ON construction_rows(construction_key(v))"} {
		enginetest.Run(t, destination, q)
	}
	importedView := destination.CatalogRead()
	importedIndex := transformStoredRelation(t, importedView, "construction_changed")
	foreignTransport := transport(importedView, changed, root)
	if !index.ConstructionMatches(foreignTransport, importedIndex) {
		t.Fatal("unchanged cross-domain import invented construction debt")
	}
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN $1+20")
	view = e.CatalogRead()
	successor := transformStoredRelation(t, view, "construction_changed")
	if e.Storage.IndexRoots[changed.Address()] != root || root.Construction != originalConstruction || index.ConstructionMatches(root, successor) {
		t.Fatal("logical publication rewrote or laundered physical recipe")
	}
	indebted := transport(view, successor, root)
	if index.ConstructionMatches(indebted, successor) {
		t.Fatal("transport erased preexisting recipe debt")
	}
	enginetest.Run(t, destination, "CREATE OR REPLACE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN $1+20")
	importedView = destination.CatalogRead()
	importedIndex = transformStoredRelation(t, importedView, "construction_changed")
	foreignDebt := transport(importedView, successor, root)
	if index.ConstructionMatches(foreignDebt, importedIndex) {
		t.Fatal("cross-domain import laundered prior construction debt")
	}
	enginetest.SessionSQL(t, s, "INSERT INTO construction_rows VALUES(3)")
	merged := e.Storage.IndexRoots[changed.Address()]
	if merged == root || merged.Construction != originalConstruction || index.ConstructionMatches(merged, successor) {
		t.Fatal("incremental merge claimed complete successor construction")
	}
	enginetest.SessionSQL(t, s, "DELETE FROM construction_rows WHERE v=3")
	deleted := e.Storage.IndexRoots[changed.Address()]
	if deleted.Construction != originalConstruction || index.ConstructionMatches(deleted, successor) {
		t.Fatal("delete erased construction history")
	}
	sibling := e.Storage.IndexRoots[plain.Address()]
	heap := e.Storage.Relations[parent.Address()]
	enginetest.SessionSQL(t, s, "CLUSTER construction_rows USING construction_plain")
	built := e.Storage.IndexRoots[changed.Address()]
	installed := transformStoredRelation(t, e.CatalogRead(), "construction_changed")
	if built == deleted || built.Construction == originalConstruction || !index.ConstructionMatches(built, installed) || e.Storage.IndexRoots[plain.Address()] != sibling || e.Storage.Relations[parent.Address()] != heap {
		t.Fatal("selective maintenance failed complete construction ownership")
	}
	enginetest.SessionSQL(t, s, "CLUSTER construction_rows USING construction_plain")
	if e.Storage.IndexRoots[changed.Address()] != built || e.Storage.IndexRoots[plain.Address()] != sibling || root.Construction != originalConstruction || !index.ConstructionMatches(root, changed) {
		t.Fatal("unchanged maintenance rebuilt or mutated saved history")
	}
	for _, invalid := range []*storage.IndexRoot{nil, {}, {Construction: installed.SQLIndex}} {
		if index.ConstructionMatches(invalid, installed) {
			t.Fatal("unminted provenance admitted")
		}
	}
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "SAVEPOINT construction_mark")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN $1+30")
	enginetest.SessionSQL(t, s, "CLUSTER construction_rows USING construction_plain")
	privateBuilt := index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, changed.Address())
	var privateDefinition *catalog.Relation
	for _, relation := range coreReceiptPrivateRelations(t, e, s) {
		if relation.Address() == changed.Address() {
			privateDefinition = relation
		}
	}
	if privateBuilt == built || privateDefinition == nil || !index.ConstructionMatches(privateBuilt, privateDefinition) || e.Storage.IndexRoots[changed.Address()] != built {
		t.Fatal("private complete build did not retain separate current history")
	}
	enginetest.SessionSQL(t, s, "ROLLBACK TO construction_mark")
	if index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, changed.Address()) != built {
		t.Fatal("savepoint retained discarded construction")
	}
	enginetest.SessionSQL(t, s, "RELEASE construction_mark")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN $1+40")
	enginetest.SessionSQL(t, s, "CLUSTER construction_rows USING construction_plain")
	committedBuild := index.EffectiveRoot(e.Storage.IndexRoots, s.Transactions.Catalog, changed.Address())
	enginetest.SessionSQL(t, s, "COMMIT")
	if e.Storage.IndexRoots[changed.Address()] != committedBuild || committedBuild == privateBuilt || !index.ConstructionMatches(committedBuild, transformStoredRelation(t, e.CatalogRead(), "construction_changed")) {
		t.Fatal("commit lost current complete-build provenance")
	}
	enginetest.SessionSQL(t, s, "BEGIN")
	enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION construction_key(integer) RETURNS integer IMMUTABLE RETURN 1/($1-$1)")
	operation, err := s.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	result, failure := e.Execute(operation, enginetest.Statement(t, "CLUSTER construction_rows USING construction_plain"), 0, nil)
	operation.Done()
	if result.Rows != nil {
		result.Rows.Close()
	}
	diagnostic, ok := failure.(interface{ SQLState() string })
	if !ok || diagnostic.SQLState() != "22012" {
		t.Fatal("failed construction did not reach current recipe", failure)
	}
	enginetest.SessionSQL(t, s, "ROLLBACK")
	if e.Storage.IndexRoots[changed.Address()] != committedBuild || e.Storage.IndexRoots[plain.Address()] != sibling || e.Storage.Relations[parent.Address()] != heap || root.Construction != originalConstruction {
		t.Fatal("failed build changed installed/saved construction history")
	}
}

func TestRoutineIndexUnchangedSlotConstructionTransport(t *testing.T) {
	for _, partial := range []bool{false, true} {
		for _, indebted := range []bool{false, true} {
			name := "clean"
			if indebted {
				name = "indebted"
			}
			if partial {
				name = "partial/" + name
			}
			t.Run(name, func(t *testing.T) {
				e := engine.New()
				s := enginetest.StartSession(t, e, nil)
				indexDDL := "CREATE INDEX tail_layout_index ON tail_layout_rows(tail_layout_key(v))"
				if partial {
					indexDDL += " WHERE v>0"
				}
				for _, q := range []string{
					"CREATE FUNCTION tail_layout_key(integer) RETURNS integer IMMUTABLE RETURN $1+10",
					"CREATE TABLE tail_layout_rows(v integer)",
					"INSERT INTO tail_layout_rows VALUES(1),(2)",
					indexDDL,
				} {
					enginetest.SessionSQL(t, s, q)
				}
				clusterName := "tail_layout_index"
				var ordinary *catalog.Relation
				var ordinaryRoot *storage.IndexRoot
				if partial {
					// Partial recipes still participate in sibling maintenance,
					// but cannot themselves be selected as clustering indexes.
					enginetest.SessionSQL(t, s, "CREATE INDEX tail_layout_cluster ON tail_layout_rows(v)")
					ordinary = transformStoredRelation(t, e.CatalogRead(), "tail_layout_cluster")
					ordinaryRoot = e.Storage.IndexRoots[ordinary.Address()]
					clusterName = "tail_layout_cluster"
				}
				original := transformStoredRelation(t, e.CatalogRead(), "tail_layout_index")
				root := e.Storage.IndexRoots[original.Address()]
				history := root.Construction
				if !index.ConstructionMatches(root, original) {
					t.Fatal("complete build lacks clean construction")
				}
				if indebted {
					enginetest.SessionSQL(t, s, "CREATE OR REPLACE FUNCTION tail_layout_key(integer) RETURNS integer IMMUTABLE RETURN $1+20")
				}
				before := transformStoredRelation(t, e.CatalogRead(), "tail_layout_index")
				if index.ConstructionMatches(root, before) == indebted {
					t.Fatal("incorrect initial construction debt")
				}
				enginetest.SessionSQL(t, s, "ALTER TABLE tail_layout_rows ADD COLUMN unrelated_tail integer")
				after := transformStoredRelation(t, e.CatalogRead(), "tail_layout_index")
				next := e.Storage.IndexRoots[after.Address()]
				if before.SQLIndex.Keys[0].Derived == after.SQLIndex.Keys[0].Derived {
					t.Fatal("fixture did not remint unrelated-layout metadata")
				}
				if index.ConstructionMatches(next, after) == indebted {
					t.Fatal("unchanged referenced slots invented or laundered construction debt")
				}
				if !index.ConstructionMatches(root, original) || root.Construction != history {
					t.Fatal("layout transport mutated historical construction")
				}
				oldPayload, newPayload := *root, *next
				oldPayload.Construction, newPayload.Construction = nil, nil
				if !reflect.DeepEqual(oldPayload, newPayload) {
					t.Fatal("unrelated tail changed physical key/order/membership payload")
				}
				if len(root.Entries) != 0 && &root.Entries[0] != &next.Entries[0] {
					t.Fatal("unchanged slot transport copied physical entries")
				}
				if index.ConstructionMatches(&storage.IndexRoot{}, after) || index.ConstructionMatches(&storage.IndexRoot{Construction: after.SQLIndex}, after) {
					t.Fatal("layout transport loosened forged/missing root admission")
				}
				if ordinary != nil && e.Storage.IndexRoots[ordinary.Address()] != ordinaryRoot {
					t.Fatal("unrelated layout transport changed the ordinary clustering root")
				}
				enginetest.SessionSQL(t, s, "CLUSTER tail_layout_rows USING "+clusterName)
				maintained := e.Storage.IndexRoots[after.Address()]
				current := transformStoredRelation(t, e.CatalogRead(), "tail_layout_index")
				if !index.ConstructionMatches(maintained, current) {
					t.Fatal("required maintenance did not produce clean construction")
				}
				if indebted && maintained == next {
					t.Fatal("indebted transported recipe skipped required rebuild")
				}
				if !indebted && maintained != next {
					t.Fatal("clean logical transport manufactured physical rebuild debt")
				}
				if ordinary != nil {
					currentOrdinary := transformStoredRelation(t, e.CatalogRead(), clusterName)
					if e.Storage.IndexRoots[ordinary.Address()] != ordinaryRoot || !index.ConstructionMatches(ordinaryRoot, currentOrdinary) {
						t.Fatal("partial sibling maintenance rebuilt the clean ordinary clustering root")
					}
				}
			})
		}
	}
}
