package engine

import (
	"context"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPartialIndexPredicateTypeGeneration(t *testing.T) {
	for _, name := range []string{"definition_only", "execution_only", "both"} {
		t.Run(name, func(t *testing.T) {
			f, scope, leaf, view := partialMaskFixture(t)
			f.e.mu.Lock()
			mask := f.e.capturePartialMaskLocked(scope, leaf, view, plan.RowMark{})
			f.e.mu.Unlock()
			if mask == nil {
				t.Fatal("admitted partial index did not retain a membership mask")
			}
			oldCatalog := f.e.catalog.state
			oldRoot := f.e.storage.IndexRoots[f.index.Address()]
			oldRows := append([]storage.RowVersion(nil), f.store.Versions...)
			oldPredicate := f.index.SQLIndex.Predicate
			oldUsage := f.predicate.ResultUsage()
			for i := range oldRows {
				oldRows[i].Values = append([]sqltype.Value(nil), oldRows[i].Values...)
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
				predicate.Execution = currentColumn
			}
			if name == "execution_only" {
				predicate.Definition = currentColumn
			}
			definition := *f.index.SQLIndex
			definition.Predicate = &predicate
			index := *f.index
			index.SQLIndex = &definition
			beforeDefinition, beforeExecution := predicate.Definition, predicate.Execution
			beforeDependencies := append([]ids.ObjectRef(nil), predicate.Dependencies...)
			beforeAttributes := append([]int32(nil), predicate.Attributes...)
			next := catalog.CloneState(oldCatalog)
			next.SetEpoch(next.Epoch() + 1)
			next.SetDescriptor(index.Address(), &index)
			typeAddress := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(current.ID())}
			typeDefinition := *next.MustType(typeAddress)
			typeDefinition.SQLType = current
			next.SetDescriptor(typeAddress, &typeDefinition)
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
			stores := map[ids.ObjectAddress]*storage.Relation{f.parent.Address(): f.store}
			if err := reownCatalogScalarTypes(next, stores, nil); err != nil {
				t.Fatal(err)
			}
			parent := next.MustRelation(f.parent.Address())
			if parent.Columns[1].Usage != newUsage || stores[f.parent.Address()] == f.store {
				t.Fatal("scalar reownership did not detach and retype the parent rows")
			}
			// A nonempty relation mapping and newer epoch must not change
			// references in the typesOnly pass.
			mapping := map[ids.ObjectAddress]ids.ObjectAddress{f.parent.Address(): {ClassID: ids.RelationClassID, ObjectID: 30000}}
			if err := f.e.reownCatalogExpressions(next, mapping, true); err != nil {
				t.Fatal(err)
			}
			reowned := next.MustRelation(index.Address())
			p := reowned.SQLIndex.Predicate
			if reowned == &index || p == &predicate {
				t.Error("changed predicate descriptor was reused")
			}
			if !reflect.DeepEqual(p.Dependencies, beforeDependencies) || !reflect.DeepEqual(p.Attributes, beforeAttributes) || reowned.SQLIndex.Parent != f.parent.Address() {
				t.Fatal("types-only pass changed dependencies, attributes or parent identity")
			}
			for _, node := range []expr.Expr{p.Definition, p.Execution} {
				column := node.(plan.ColumnExpr)
				if column.ResultUsage() != newUsage || column.Origin != f.predicate.Origin || column.Scope != f.predicate.Scope || column.Frame != f.predicate.Frame || column.Slot != f.predicate.Slot {
					t.Error("predicate tree retained a stale usage or changed its reference/frame", column)
				}
				for i, row := range stores[f.parent.Address()].Versions {
					if row.Values[1].Type() != current {
						t.Fatal("row retained old BOOL generation", i)
					}
					value, err := node.Eval(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1, Row: row.Values}})
					if err != nil || value != row.Values[1] {
						t.Error("retyped predicate tree cannot evaluate its retyped row", i, value, err)
					}
				}
			}
			var preparation indexExecutionPreparation
			for i, row := range stores[f.parent.Address()].Versions {
				matched, err := preparation.predicateMatches(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}, parent, reowned.SQLIndex, row.Values, nil, nil)
				if err != nil || matched != (i == 0 || i == 3 || i == 4) {
					t.Error("reowned predicate changed TRUE/FALSE/NULL membership", i, matched, err)
				}
			}
			root, err := buildIndexRoot(&EvalContext{EvalStatement: evalStatement{Context: context.Background()}, EvalFrame: evalFrame{Scope: 1, Frame: 1}}, parent, reowned.SQLIndex, stores[f.parent.Address()], nil)
			if err != nil {
				t.Fatal(err)
			}
			partialWantMembership(t, root, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
			partialWantMembership(t, oldRoot, map[ids.RowID]ids.VersionID{1: 1, 4: 4, 5: 5})
			if f.e.catalog.state != oldCatalog || oldCatalog.Get(f.parent.Address()) != f.parent || oldCatalog.Get(f.index.Address()) != f.index || f.e.storage.IndexRoots[f.index.Address()] != oldRoot || f.index.SQLIndex.Predicate != oldPredicate || predicate.Definition != beforeDefinition || predicate.Execution != beforeExecution || !reflect.DeepEqual(predicate.Dependencies, beforeDependencies) || !reflect.DeepEqual(f.store.Versions, oldRows) {
				t.Fatal("type-only reownership mutated a retained catalog, predicate, row or root")
			}
			for i, row := range view.Load().Committed[f.parent.Address()] {
				if row.Values[1].Type() != oldUsage.Type || exec.PartialMaskContains(mask, row) != (i == 0 || i == 3 || i == 4) {
					t.Fatal("retained scope lost its old row generation or membership", i)
				}
			}
			if next.Get(f.parent.Address()) != parent || next.Get(typeAddress) != &typeDefinition {
				t.Fatal("expression pass replaced unrelated scalar descriptors")
			}
		})
	}
}

func TestPartialIndexPredicateTypeGenerationUnchanged(t *testing.T) {
	f := newPartialIndexFixture(t)
	next := catalog.CloneState(f.e.catalog.state)
	typ := func(old *sqltype.Descriptor) *sqltype.Descriptor { return old }
	if catalogExpressionTypesChanged(f.index, typ) {
		t.Fatal("unchanged partial predicate reported a generation change")
	}
	if err := f.e.reownCatalogExpressions(next, nil, true); err != nil {
		t.Fatal(err)
	}
	for address, descriptor := range f.e.catalog.state.Descriptors() {
		if next.Get(address) != descriptor {
			t.Fatal("unchanged descriptor was not reused", address)
		}
	}
	if next.MustRelation(f.index.Address()).SQLIndex.Predicate != f.index.SQLIndex.Predicate {
		t.Fatal("unchanged predicate was not reused")
	}
}
