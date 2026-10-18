package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPartialIndexPredicateTypeGeneration(t *testing.T) {
	for _, name := range []string{"definition_only", "execution_only", "both"} {
		t.Run(name, func(t *testing.T) {
			f, scope, leaf, view := partialMaskFixture(t)
			f.e.mu.Lock()
			mask := f.e.capturePartialMaskLocked(scope, leaf, view, queryRowMark{})
			f.e.mu.Unlock()
			if mask == nil {
				t.Fatal("admitted partial index did not retain a membership mask")
			}
			oldCatalog := f.e.state
			oldRoot := f.e.indexRoots[f.index.address]
			oldRows := append([]rowVersion(nil), f.store.versions...)
			oldPredicate := f.index.sqlIndex.predicate
			oldUsage := f.predicate.ResultUsage()
			for i := range oldRows {
				oldRows[i].values = append([]sqltype.Value(nil), oldRows[i].values...)
			}
			// Builtins returns fresh immutable concrete descriptors, as in the
			// existing bound-expression generation fixtures. Do not copy a
			// descriptor (or the engine) to manufacture a new generation.
			var current *sqltype.Descriptor
			for _, descriptor := range sqltype.Builtins() {
				if descriptor.ID() == sqltype.Bool {
					current = descriptor
				}
			}
			if current == nil || current == oldUsage.Type || !current.Concrete() {
				t.Fatal("fixture did not produce a distinct concrete BOOL generation")
			}
			newUsage := oldUsage
			newUsage.Type = current
			currentColumn := f.predicate
			currentColumn.Usage = newUsage
			predicate := *oldPredicate
			if name == "definition_only" {
				predicate.execution = currentColumn
			}
			if name == "execution_only" {
				predicate.definition = currentColumn
			}
			definition := *f.index.sqlIndex
			definition.predicate = &predicate
			index := *f.index
			index.sqlIndex = &definition
			beforeDefinition, beforeExecution := predicate.definition, predicate.execution
			beforeDependencies := append([]ids.ObjectRef(nil), predicate.dependencies...)
			beforeAttributes := append([]int32(nil), predicate.attributes...)
			next := cloneCatalog(oldCatalog)
			next.epoch++
			next.descriptors[index.address] = &index
			typeAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.ID())}
			typeDefinition := *next.descriptors[typeAddress].(*typeDescriptor)
			typeDefinition.typ = current
			next.descriptors[typeAddress] = &typeDefinition
			typ := func(old *sqltype.Descriptor) *sqltype.Descriptor {
				if old == oldUsage.Type {
					return current
				}
				return old
			}
			// Only the partial predicate consumes the changed generation:
			// the admitted index has a simple INTEGER key and no stored
			// defaults, generated columns, CHECKs or view expressions.
			if !catalogExpressionTypesChanged(&index, typ) {
				t.Error("predicate-only type generation was not detected")
			}
			stores := map[ids.ObjectAddress]*relationStorage{f.parent.address: f.store}
			if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
				t.Fatal(err)
			}
			parent := next.descriptors[f.parent.address].(*relationDescriptor)
			if parent.columns[1].Usage != newUsage || stores[f.parent.address] == f.store {
				t.Fatal("scalar reownership did not detach and retype the parent rows")
			}
			// A nonempty relation mapping and newer epoch must not change
			// references in the typesOnly pass.
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{f.parent.address: {ClassID: ids.RelationClassID, ObjectID: 30000}}
			if err := f.e.reownCatalogExpressions(next, mapping, true); err != nil {
				t.Fatal(err)
			}
			reowned := next.descriptors[index.address].(*relationDescriptor)
			p := reowned.sqlIndex.predicate
			if reowned == &index || p == &predicate {
				t.Error("changed predicate descriptor was reused")
			}
			if !reflect.DeepEqual(p.dependencies, beforeDependencies) || !reflect.DeepEqual(p.attributes, beforeAttributes) || reowned.sqlIndex.parent != f.parent.address {
				t.Fatal("types-only pass changed dependencies, attributes or parent identity")
			}
			for _, node := range []BoundExpr{p.definition, p.execution} {
				column := node.(columnExpression)
				if column.ResultUsage() != newUsage || column.origin != f.predicate.origin || column.scope != f.predicate.scope || column.frame != f.predicate.frame || column.slot != f.predicate.slot {
					t.Error("predicate tree retained a stale usage or changed its reference/frame", column)
				}
				for i, row := range stores[f.parent.address].versions {
					if row.values[1].Type() != current {
						t.Fatal("row retained old BOOL generation", i)
					}
					value, err := node.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: row.values})
					if err != nil || value != row.values[1] {
						t.Error("retyped predicate tree cannot evaluate its retyped row", i, value, err)
					}
				}
			}
			var preparation indexExecutionPreparation
			for i, row := range stores[f.parent.address].versions {
				matched, err := preparation.predicateMatches(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, reowned.sqlIndex, row.values, nil, nil)
				if err != nil || matched != (i == 0 || i == 3 || i == 4) {
					t.Error("reowned predicate changed TRUE/FALSE/NULL membership", i, matched, err)
				}
			}
			root, err := buildIndexRoot(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, parent, reowned.sqlIndex, stores[f.parent.address], nil)
			if err != nil {
				t.Fatal(err)
			}
			partialWantMembership(t, root, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
			partialWantMembership(t, oldRoot, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
			if f.e.state != oldCatalog || oldCatalog.descriptors[f.parent.address] != f.parent || oldCatalog.descriptors[f.index.address] != f.index || f.e.indexRoots[f.index.address] != oldRoot || f.index.sqlIndex.predicate != oldPredicate || predicate.definition != beforeDefinition || predicate.execution != beforeExecution || !reflect.DeepEqual(predicate.dependencies, beforeDependencies) || !reflect.DeepEqual(f.store.versions, oldRows) {
				t.Fatal("type-only reownership mutated a retained catalog, predicate, row or root")
			}
			for i, row := range view.data.Load().committed[f.parent.address] {
				if row.values[1].Type() != oldUsage.Type || partialMaskContains(mask, row) != (i == 0 || i == 3 || i == 4) {
					t.Fatal("retained scope lost its old row generation or membership", i)
				}
			}
			if next.descriptors[f.parent.address] != parent || next.descriptors[typeAddress] != &typeDefinition {
				t.Fatal("expression pass replaced unrelated scalar descriptors")
			}
		})
	}
}

func TestPartialIndexPredicateTypeGenerationUnchanged(t *testing.T) {
	f := newPartialIndexFixture(t)
	next := cloneCatalog(f.e.state)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }
	if catalogExpressionTypesChanged(f.index, typ) {
		t.Fatal("unchanged partial predicate reported a generation change")
	}
	if err := f.e.reownCatalogExpressions(next, nil, true); err != nil {
		t.Fatal(err)
	}
	for address, descriptor := range f.e.state.descriptors {
		if next.descriptors[address] != descriptor {
			t.Fatal("unchanged descriptor was not reused", address)
		}
	}
	if next.descriptors[f.index.address].(*relationDescriptor).sqlIndex.predicate != f.index.sqlIndex.predicate {
		t.Fatal("unchanged predicate was not reused")
	}
}
