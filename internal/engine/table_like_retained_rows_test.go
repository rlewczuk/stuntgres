package engine

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"
)

func TestTableLikeTemporaryRetainedRows(t *testing.T) {
	for _, temporary := range []bool{false, true} {
		name := map[bool]string{false: "permanent", true: "temporary"}[temporary]
		t.Run(name, func(t *testing.T) {
			e := New()
			owner := sessionStart(t, e, nil)
			sessionSQL(t, owner, "CREATE TEMP TABLE src(dead TEXT,a INT NOT NULL,g INT GENERATED ALWAYS AS(a*2) STORED,o OID GENERATED ALWAYS AS(tableoid) STORED,n INT,active BOOL,CONSTRAINT positive CHECK(a>0))")
			sessionSQL(t, owner, "ALTER TABLE src DROP COLUMN dead")
			sessionSQL(t, owner, "CREATE INDEX source_expression ON src((a+1))")
			sessionSQL(t, owner, "CREATE INDEX source_partial ON src((10/n)) WHERE active")
			sessionSQL(t, owner, "INSERT INTO src(a,n,active) VALUES(3,2,true)")
			source := tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "src")
			oldEpoch := e.CatalogRead().Epoch()
			generations := maps.Clone(source.generations)
			for attribute, generation := range generations {
				generation.dependencies = slices.Clone(generation.dependencies)
				generations[attribute] = generation
			}
			check := source.checkConstraints[0]
			originalChecks := slices.Clone(check.dependencies)
			indexes := []*relationDescriptor{tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "source_expression"), tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "source_partial")}
			sourceTrees := []BoundExpr{generations[3].expression, generations[3].planned, generations[4].expression, generations[4].planned, check.expression, indexes[0].sqlIndex.keys[0].derived.definition, indexes[0].sqlIndex.keys[0].derived.execution, indexes[1].sqlIndex.keys[0].derived.definition, indexes[1].sqlIndex.keys[0].derived.execution, indexes[1].sqlIndex.predicate.definition, indexes[1].sqlIndex.predicate.execution}
			rowRefs := func(nodes []BoundExpr) []ObjectRef {
				var refs []ObjectRef
				for _, node := range nodes {
					walkBoundExpression(node, func(node BoundExpr) {
						switch n := node.(type) {
						case columnExpression:
							refs = append(refs, n.origin)
						case tableOIDExpression:
							refs = append(refs, n.owner)
						}
					})
				}
				return refs
			}
			originalRefs := rowRefs(sourceTrees)
			for _, ref := range originalRefs {
				if ref.Epoch != oldEpoch || ref.Address.ObjectID != source.address.ObjectID {
					t.Fatal("invalid source row fixture", ref)
				}
			}
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().Epoch() == oldEpoch || e.CatalogRead().state.descriptors[source.address] != source {
				t.Fatal("source not retained unchanged after actual replacement")
			}
			op, err := owner.Acquire(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer op.Done()
			prefix := ""
			if temporary {
				prefix = "TEMP "
			}
			stage, destination, err := tableLikeBuild(t, e, op, "CREATE "+prefix+"TABLE clone(lead TEXT,LIKE pg_temp.src INCLUDING ALL)")
			defer e.DiscardCatalogStage(stage)
			if err != nil {
				t.Fatal("retained row-expression copy", err)
			}
			local := func(attribute int32) ObjectRef {
				return ObjectRef{owner: e.domain, Epoch: stage.base.Epoch(), Address: ObjectAddress{ClassID: RelationClassID, ObjectID: destination.address.ObjectID, SubID: attribute}}
			}
			assertTree := func(node BoundExpr) {
				t.Helper()
				refs := rowRefs([]BoundExpr{node})
				if len(refs) == 0 {
					t.Fatal("fixture lost row reference")
				}
				for _, ref := range refs {
					if ref.owner != e.domain || ref.Epoch != stage.base.Epoch() || ref.Address.ObjectID != destination.address.ObjectID {
						t.Fatal("mapped row lacks fresh destination authority", ref)
					}
				}
				if _, err := (scalarRenderer{owner: destination}).sql(context.Background(), e.stageRead(stage), node); err != nil {
					t.Fatal("mapped analyzed/execution tree not renderable", err)
				}
			}
			for attribute, old := range generations {
				copied := destination.generations[attribute]
				if copied.OID == old.OID {
					t.Fatal("generation identity retained")
				}
				assertTree(copied.expression)
				assertTree(copied.planned)
				expected := local(2)
				if attribute == 4 {
					expected = local(0)
				}
				if !slices.Contains(copied.dependencies, expected) {
					t.Fatal("generation manifest lacks current local authority", copied.dependencies, expected)
				}
			}
			copiedCheck := destination.checkConstraints[0]
			if copiedCheck.OID == check.OID {
				t.Fatal("CHECK identity retained")
			}
			assertTree(copiedCheck.expression)
			found := false
			for _, dependency := range copiedCheck.dependencies {
				found = found || dependency.ref == local(2)
			}
			if !found {
				t.Fatal("CHECK manifest lacks current local authority", copiedCheck.dependencies)
			}
			var clonedIndexes []*relationDescriptor
			for _, descriptor := range stage.descriptors {
				index, ok := descriptor.(*relationDescriptor)
				if !ok || index.sqlIndex == nil {
					continue
				}
				clonedIndexes = append(clonedIndexes, index)
				key := index.sqlIndex.keys[0].derived
				assertTree(key.definition)
				assertTree(key.execution)
				expected := local(2)
				if index.sqlIndex.predicate != nil {
					expected = local(5)
					assertTree(index.sqlIndex.predicate.definition)
					assertTree(index.sqlIndex.predicate.execution)
					if !slices.Contains(index.sqlIndex.predicate.dependencies, local(6)) {
						t.Fatal("predicate manifest lacks current destination attribute", index.sqlIndex.predicate.dependencies)
					}
				}
				if !slices.Contains(key.dependencies, expected) {
					t.Fatal("derived manifest lacks current destination attribute", key.dependencies)
				}
			}
			if len(clonedIndexes) != 2 {
				t.Fatal("indexes omitted")
			}
			if !slices.Equal(rowRefs(sourceTrees), originalRefs) || !reflect.DeepEqual(source.generations, generations) || !slices.Equal(check.dependencies, originalChecks) {
				t.Fatal("source cooked definitions mutated")
			}
			candidate, _, collision := tableLikeBuild(t, e, op, "CREATE "+prefix+"TABLE failed(LIKE pg_temp.src INCLUDING ALL,a INT)")
			if collision == nil {
				t.Fatal("duplicate copied column accepted")
			}
			for address := range candidate.descriptors {
				if e.CatalogRead().state.descriptors[address] != nil || e.storage[address] != nil || e.indexRoots[address] != nil || e.sequences[address] != nil {
					t.Fatal("failed copied candidate published a fragment", address)
				}
			}
			e.DiscardCatalogStage(candidate)
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			for _, index := range clonedIndexes {
				root := e.indexRoots[index.address]
				if root == nil || len(root.entries) != 0 {
					t.Fatal("copied root not fresh empty", root)
				}
				for _, old := range indexes {
					if root == e.indexRoots[old.address] {
						t.Fatal("copied root aliases source")
					}
				}
			}
			op.Done()
			sessionSQL(t, owner, "INSERT INTO clone(lead,a,n,active) VALUES('x',4,2,true),('y',5,0,false)")
			sessionSQL(t, owner, "UPDATE clone SET a=6 WHERE lead='x'")
			if !slices.Equal(rowRefs(sourceTrees), originalRefs) {
				t.Fatal("writes altered source tree")
			}
		})
	}
}

func TestTableLikeTemporaryRetainedLiterals(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TEMP TABLE anchor(a INT)")
	sessionSQL(t, owner, "CREATE TEMP TABLE src(a INT,r BOOL GENERATED ALWAYS AS(a>0 AND ('pg_temp.anchor'::regclass IS NOT NULL)) STORED,active BOOL,CONSTRAINT positive CHECK(a>0 AND ('pg_temp.anchor'::regclass IS NOT NULL)))")
	sessionSQL(t, owner, "CREATE INDEX source_partial ON src(a) WHERE active AND ('pg_temp.anchor'::regclass IS NOT NULL)")
	source := tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "src")
	anchor := tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "anchor")
	oldRef := ObjectRef{owner: e.domain, Epoch: e.CatalogRead().Epoch(), Address: anchor.address}
	oldGeneration := source.generations[2]
	oldCheck := source.checkConstraints[0]
	oldIndex := tableLikeTemporaryRelation(t, e, owner.temporaryNamespace, "source_partial")
	oldManifest := slices.Clone(oldIndex.sqlIndex.predicate.dependencies)
	if !slices.Contains(oldGeneration.dependencies, oldRef) || !slices.Contains(oldManifest, oldRef) {
		t.Fatal("fixture lacks retained external literal provider")
	}
	if err := e.Reset(context.Background()); err != nil {
		t.Fatal(err)
	}
	op, err := owner.Acquire(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer op.Done()
	stage, destination, err := tableLikeBuild(t, e, op, "CREATE TEMP TABLE clone(lead TEXT,LIKE pg_temp.src INCLUDING ALL)")
	defer e.DiscardCatalogStage(stage)
	if err != nil {
		t.Fatal(err)
	}
	generation := destination.generations[3]
	if !slices.Contains(generation.dependencies, oldRef) {
		t.Fatal("external literal manifest reminted", generation.dependencies)
	}
	assertNode := func(node BoundExpr) {
		t.Helper()
		walkBoundExpression(node, func(node BoundExpr) {
			switch n := node.(type) {
			case columnExpression:
				if n.origin.owner != e.domain || n.origin.Epoch != stage.base.Epoch() || n.origin.Address.ObjectID != destination.address.ObjectID {
					t.Fatal("local row authority not current", n.origin)
				}
			case literalExpression:
				if n.regclassRef.Address == anchor.address && n.regclassRef != oldRef {
					t.Fatal("external constant retagged", n.regclassRef)
				}
			}
		})
	}
	assertNode(generation.expression)
	assertNode(generation.planned)
	found := false
	for _, dependency := range destination.checkConstraints[0].dependencies {
		found = found || dependency.ref == oldRef
	}
	if !found {
		t.Fatal("CHECK external manifest reminted")
	}
	assertNode(destination.checkConstraints[0].expression)
	for _, descriptor := range stage.descriptors {
		index, ok := descriptor.(*relationDescriptor)
		if !ok || index.sqlIndex == nil {
			continue
		}
		if !slices.Contains(index.sqlIndex.predicate.dependencies, oldRef) {
			t.Fatal("predicate external manifest reminted")
		}
		assertNode(index.sqlIndex.predicate.definition)
		assertNode(index.sqlIndex.predicate.execution)
	}
	if !reflect.DeepEqual(source.generations[2], oldGeneration) || !reflect.DeepEqual(source.checkConstraints[0], oldCheck) || !slices.Equal(oldIndex.sqlIndex.predicate.dependencies, oldManifest) {
		t.Fatal("retained source definitions changed")
	}
	// An old external TEMP constant never borrows the mapper's fresh local-row
	// epoch, even when a permanent copied consumer would otherwise be valid.
	permanent := *destination
	permanent.namespace = ObjectAddress{ClassID: NamespaceClassID, ObjectID: PublicNamespaceOID}
	if storedReferenceEpochValid(e.stageRead(stage), &permanent, oldRef) {
		t.Fatal("permanent consumer gained stale TEMP external authority")
	}
	if err := e.ValidateObjectRef(e.stageRead(stage), oldRef); err == nil {
		t.Fatal("generic stale reference rule widened")
	}
}
