package engine

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
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
			source := tableLikeTemporaryRelation(t, e, owner.TemporaryNamespace, "src")
			oldEpoch := e.CatalogRead().Epoch()
			generations := maps.Clone(source.ColumnMeta.Generations)
			for attribute, generation := range generations {
				generation.Dependencies = slices.Clone(generation.Dependencies)
				generations[attribute] = generation
			}
			check := source.Constraints.Check[0]
			originalChecks := slices.Clone(check.Dependencies)
			indexes := []*catalog.Relation{tableLikeTemporaryRelation(t, e, owner.TemporaryNamespace, "source_expression"), tableLikeTemporaryRelation(t, e, owner.TemporaryNamespace, "source_partial")}
			sourceTrees := []expr.Expr{generations[3].Expression, generations[3].Planned, generations[4].Expression, generations[4].Planned, check.Expression, indexes[0].SQLIndex.Keys[0].Derived.Definition, indexes[0].SQLIndex.Keys[0].Derived.Execution, indexes[1].SQLIndex.Keys[0].Derived.Definition, indexes[1].SQLIndex.Keys[0].Derived.Execution, indexes[1].SQLIndex.Predicate.Definition, indexes[1].SQLIndex.Predicate.Execution}
			rowRefs := func(nodes []expr.Expr) []ids.ObjectRef {
				var refs []ids.ObjectRef
				for _, node := range nodes {
					plan.WalkExpression(node, func(node expr.Expr) {
						switch n := node.(type) {
						case plan.ColumnExpr:
							refs = append(refs, n.Origin)
						case plan.TableOIDExpr:
							refs = append(refs, n.Owner)
						}
					})
				}
				return refs
			}
			originalRefs := rowRefs(sourceTrees)
			for _, ref := range originalRefs {
				if ref.Epoch != oldEpoch || ref.Address.ObjectID != source.Address().ObjectID {
					t.Fatal("invalid source row fixture", ref)
				}
			}
			if err := e.Reset(context.Background()); err != nil {
				t.Fatal(err)
			}
			if e.CatalogRead().Epoch() == oldEpoch || e.CatalogRead().Get(source.Address()) != source {
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
			local := func(attribute int32) ids.ObjectRef {
				return e.Catalog.Domain.Ref(stage.Base.Epoch(), ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: destination.Address().ObjectID, SubID: attribute})
			}
			assertTree := func(node expr.Expr) {
				t.Helper()
				refs := rowRefs([]expr.Expr{node})
				if len(refs) == 0 {
					t.Fatal("fixture lost row reference")
				}
				for _, ref := range refs {
					if ref.Domain() != e.Catalog.Domain || ref.Epoch != stage.Base.Epoch() || ref.Address.ObjectID != destination.Address().ObjectID {
						t.Fatal("mapped row lacks fresh destination authority", ref)
					}
				}
				if _, err := (stored.ScalarRenderer{Owner: destination}).SQL(context.Background(), e.StageRead(stage), node); err != nil {
					t.Fatal("mapped analyzed/execution tree not renderable", err)
				}
			}
			for attribute, old := range generations {
				copied := destination.ColumnMeta.Generations[attribute]
				if copied.OID == old.OID {
					t.Fatal("generation identity retained")
				}
				assertTree(copied.Expression)
				assertTree(copied.Planned)
				expected := local(2)
				if attribute == 4 {
					expected = local(0)
				}
				if !slices.Contains(copied.Dependencies, expected) {
					t.Fatal("generation manifest lacks current local authority", copied.Dependencies, expected)
				}
			}
			copiedCheck := destination.Constraints.Check[0]
			if copiedCheck.OID == check.OID {
				t.Fatal("CHECK identity retained")
			}
			assertTree(copiedCheck.Expression)
			found := false
			for _, dependency := range copiedCheck.Dependencies {
				found = found || dependency.Ref == local(2)
			}
			if !found {
				t.Fatal("CHECK manifest lacks current local authority", copiedCheck.Dependencies)
			}
			var clonedIndexes []*catalog.Relation
			for _, descriptor := range stage.Descriptors() {
				index, ok := descriptor.(*catalog.Relation)
				if !ok || index.SQLIndex == nil {
					continue
				}
				clonedIndexes = append(clonedIndexes, index)
				key := index.SQLIndex.Keys[0].Derived
				assertTree(key.Definition)
				assertTree(key.Execution)
				expected := local(2)
				if index.SQLIndex.Predicate != nil {
					expected = local(5)
					assertTree(index.SQLIndex.Predicate.Definition)
					assertTree(index.SQLIndex.Predicate.Execution)
					if !slices.Contains(index.SQLIndex.Predicate.Dependencies, local(6)) {
						t.Fatal("predicate manifest lacks current destination attribute", index.SQLIndex.Predicate.Dependencies)
					}
				}
				if !slices.Contains(key.Dependencies, expected) {
					t.Fatal("derived manifest lacks current destination attribute", key.Dependencies)
				}
			}
			if len(clonedIndexes) != 2 {
				t.Fatal("indexes omitted")
			}
			if !slices.Equal(rowRefs(sourceTrees), originalRefs) || !reflect.DeepEqual(source.ColumnMeta.Generations, generations) || !slices.Equal(check.Dependencies, originalChecks) {
				t.Fatal("source cooked definitions mutated")
			}
			candidate, _, collision := tableLikeBuild(t, e, op, "CREATE "+prefix+"TABLE failed(LIKE pg_temp.src INCLUDING ALL,a INT)")
			if collision == nil {
				t.Fatal("duplicate copied column accepted")
			}
			for address := range candidate.Descriptors() {
				if e.CatalogRead().Get(address) != nil || e.Storage.Relations[address] != nil || e.Storage.IndexRoots[address] != nil || e.Storage.Sequences[address] != nil {
					t.Fatal("failed copied candidate published a fragment", address)
				}
			}
			e.DiscardCatalogStage(candidate)
			prepared, err := e.ValidateCatalogStage(context.Background(), stage)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := e.PublishCatalog(prepared, PublicationToken{Epoch: stage.Base.Epoch()}); err != nil {
				t.Fatal(err)
			}
			for _, index := range clonedIndexes {
				root := e.Storage.IndexRoots[index.Address()]
				if root == nil || len(root.Entries) != 0 {
					t.Fatal("copied root not fresh empty", root)
				}
				for _, old := range indexes {
					if root == e.Storage.IndexRoots[old.Address()] {
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
	source := tableLikeTemporaryRelation(t, e, owner.TemporaryNamespace, "src")
	anchor := tableLikeTemporaryRelation(t, e, owner.TemporaryNamespace, "anchor")
	oldRef := e.Catalog.Domain.Ref(e.CatalogRead().Epoch(), anchor.Address())
	oldGeneration := source.ColumnMeta.Generations[2]
	oldCheck := source.Constraints.Check[0]
	oldIndex := tableLikeTemporaryRelation(t, e, owner.TemporaryNamespace, "source_partial")
	oldManifest := slices.Clone(oldIndex.SQLIndex.Predicate.Dependencies)
	if !slices.Contains(oldGeneration.Dependencies, oldRef) || !slices.Contains(oldManifest, oldRef) {
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
	generation := destination.ColumnMeta.Generations[3]
	if !slices.Contains(generation.Dependencies, oldRef) {
		t.Fatal("external literal manifest reminted", generation.Dependencies)
	}
	assertNode := func(node expr.Expr) {
		t.Helper()
		plan.WalkExpression(node, func(node expr.Expr) {
			switch n := node.(type) {
			case plan.ColumnExpr:
				if n.Origin.Domain() != e.Catalog.Domain || n.Origin.Epoch != stage.Base.Epoch() || n.Origin.Address.ObjectID != destination.Address().ObjectID {
					t.Fatal("local row authority not current", n.Origin)
				}
			case plan.LiteralExpr:
				if n.RegclassRef.Address == anchor.Address() && n.RegclassRef != oldRef {
					t.Fatal("external constant retagged", n.RegclassRef)
				}
			}
		})
	}
	assertNode(generation.Expression)
	assertNode(generation.Planned)
	found := false
	for _, dependency := range destination.Constraints.Check[0].Dependencies {
		found = found || dependency.Ref == oldRef
	}
	if !found {
		t.Fatal("CHECK external manifest reminted")
	}
	assertNode(destination.Constraints.Check[0].Expression)
	for _, descriptor := range stage.Descriptors() {
		index, ok := descriptor.(*catalog.Relation)
		if !ok || index.SQLIndex == nil {
			continue
		}
		if !slices.Contains(index.SQLIndex.Predicate.Dependencies, oldRef) {
			t.Fatal("predicate external manifest reminted")
		}
		assertNode(index.SQLIndex.Predicate.Definition)
		assertNode(index.SQLIndex.Predicate.Execution)
	}
	if !reflect.DeepEqual(source.ColumnMeta.Generations[2], oldGeneration) || !reflect.DeepEqual(source.Constraints.Check[0], oldCheck) || !slices.Equal(oldIndex.SQLIndex.Predicate.Dependencies, oldManifest) {
		t.Fatal("retained source definitions changed")
	}
	// An old external TEMP constant never borrows the mapper's fresh local-row
	// epoch, even when a permanent copied consumer would otherwise be valid.
	permanent := *destination
	permanent.ObjectNamespace = ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}
	if stored.ReferenceEpochValid(e.StageRead(stage), &permanent, oldRef) {
		t.Fatal("permanent consumer gained stale TEMP external authority")
	}
	if err := e.ValidateObjectRef(e.StageRead(stage), oldRef); err == nil {
		t.Fatal("generic stale reference rule widened")
	}
}
