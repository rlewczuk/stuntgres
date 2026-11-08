package analyze

import (
	"slices"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func groupDependencyBinder(t *testing.T) (*Binder, []expr.Expr) {
	t.Helper()
	b, namespace := tableOIDBinder(t)
	integer, _ := b.BindingCatalog().LookupType(sqltype.Int4)
	parent := b.Query.Sources[0].Relation
	parent.Attributes = []int32{2, 7, 9}
	parent.Columns = []catalog.Column{{Name: "a", Usage: integer.Usage()}, {Name: "b", Usage: integer.Usage()}, {Name: "payload", Usage: integer.Usage()}}
	parent.Constraints.Unique = []catalog.UniqueConstraint{{OID: 23000, Name: "composite_pk", Primary: true, Attributes: []int32{2, 7}}}
	// Two admitted aliases of the very same descriptor and reference.
	b.Query.Sources[1] = b.Query.Sources[0]
	namespace.Qualifiers[1].Ref = namespace.Qualifiers[0].Ref
	for occurrence := range 2 {
		for physical, descriptor := range parent.Columns {
			column := QueryColumn{Descriptor: descriptor, Slot: occurrence*3 + physical, Leaf: occurrence, Physical: physical}
			namespace.Columns = append(namespace.Columns, column)
			namespace.Frame = append(namespace.Frame, descriptor)
			namespace.Qualifiers[occurrence].Columns = append(namespace.Qualifiers[occurrence].Columns, column)
		}
	}
	columns := make([]expr.Expr, len(namespace.Columns))
	for i, column := range namespace.Columns {
		columns[i] = b.QueryColumnAt(column, parser.Position{Character: 8 + i})
	}
	return b, columns
}

func TestGroupDependencyPrimaryKeyProof(t *testing.T) {
	for _, test := range []struct {
		name                          string
		keys                          []int
		primary, deferrable, accepted bool
	}{
		{name: "complete_reordered", keys: []int{1, 0}, primary: true, accepted: true},
		{name: "superset", keys: []int{0, 1, 2}, primary: true, accepted: true},
		{name: "incomplete", keys: []int{0}, primary: true},
		{name: "alias_collision", keys: []int{0, 4}, primary: true},
		{name: "unique_not_null", keys: []int{0, 1}},
		{name: "deferrable_primary", keys: []int{0, 1}, primary: true, deferrable: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			b, columns := groupDependencyBinder(t)
			parent := b.Query.Sources[0].Relation
			parent.Constraints.Unique[0].Primary = test.primary
			parent.Constraints.Unique[0].Deferrable = test.deferrable
			var keys []expr.Expr
			for _, key := range test.keys {
				keys = append(keys, columns[key])
			}
			b.Grouping.dependency = &groupDependencyState{keys: keys}
			accepted := b.proveGroupOccurrence(0)
			if accepted != test.accepted {
				t.Fatal("primary-key proof", accepted)
			}
			if !accepted {
				if len(b.Grouping.dependency.proofs) != 0 {
					t.Fatal("rejected proof retained authority")
				}
				return
			}
			proof := b.Grouping.dependency.proofs[0]
			if proof.Parent != b.Query.Sources[0].Ref || proof.Constraint.ObjectID != 23000 || proof.Constraint.ClassID != 2606 || proof.Frame != 1 || proof.Occurrence != 0 || !slices.Equal(proof.Attributes, []int32{2, 7}) {
				t.Fatal("wrong authentic stable-key identity", proof)
			}
			if b.proveGroupOccurrence(1) {
				t.Fatal("same descriptor transferred proof to another alias")
			}
			if !b.proveGroupOccurrence(0) || len(b.Grouping.dependency.proofs) != 1 {
				t.Fatal("occurrence proof was not reused")
			}
			captures := 0
			for _, dependency := range b.Dependencies {
				if dependency.Ref == proof.Parent {
					captures++
					if dependency.Revision != parent.Revision() || dependency.Purpose != catalog.DefinitionDependency {
						t.Fatal("parent capture lost defining stamp")
					}
				}
				if dependency.Ref.Address.ClassID == 2606 {
					t.Fatal("attached constraint entered descriptor manifest")
				}
			}
			if captures != 1 {
				t.Fatal("parent capture not deduplicated", captures)
			}
			parent.Constraints.Unique[0].Attributes[0] = 99
			if proof.Attributes[0] != 2 || !b.SameGroupExpression(keys[0], columns[test.keys[0]]) {
				t.Fatal("proof borrowed mutable attribute backing or changed keys")
			}
		})
	}
	t.Run("frame_computed_and_derived_boundaries", func(t *testing.T) {
		b, columns := groupDependencyBinder(t)
		wrongFrame := columns[1].(plan.ColumnExpr)
		wrongFrame.Frame = 2
		for _, key := range []expr.Expr{wrongFrame, plan.GroupExpr{Info: expr.InfoOf(columns[1]), Frame: 1, Slot: 1}} {
			b.Grouping.dependency = &groupDependencyState{keys: []expr.Expr{columns[0], key}}
			if b.proveGroupOccurrence(0) {
				t.Fatal("nonordinary key supplied membership")
			}
		}
		b.Grouping.dependency = &groupDependencyState{keys: columns[:2]}
		derived := QueryColumn{Descriptor: b.Query.Namespace.Frame[2], Slot: 6, Leaf: -1}
		b.Query.Namespace.Columns = append(b.Query.Namespace.Columns, derived)
		b.Query.Namespace.Frame = append(b.Query.Namespace.Frame, derived.Descriptor)
		if _, ok := b.dependentGroupExpression(plan.ColumnExpr{Info: expr.InfoOf(columns[2]), Origin: columns[2].(plan.ColumnExpr).Origin, Scope: 1, Frame: 1, Slot: 6}); ok {
			t.Fatal("derived output origin manufactured a base occurrence")
		}
		for _, kind := range []catalog.RelationKind{catalog.ViewRelation, catalog.MaterializedViewRelation} {
			b.Query.Sources[0].Relation.RelationKind = kind
			b.Grouping.dependency = &groupDependencyState{keys: columns[:2]}
			if b.proveGroupOccurrence(0) {
				t.Fatal("non-base relation inherited key privilege")
			}
		}
	})
	t.Run("system_occurrence_and_repeated_payload", func(t *testing.T) {
		b, columns := groupDependencyBinder(t)
		keys := columns[:2]
		b.Grouping.dependency = &groupDependencyState{keys: keys}
		system, err := b.querySystemAttribute("tableoid", b.Query.Namespace, &b.Query.Namespace.Qualifiers[0], nil, 1, parser.Position{Character: 20})
		if err != nil {
			t.Fatal(err)
		}
		first, err := b.RewriteGroup(system, keys)
		if err != nil {
			t.Fatal(err)
		}
		second, err := b.RewriteGroup(system, keys)
		if err != nil || first.(plan.GroupExpr).Slot != second.(plan.GroupExpr).Slot || len(b.Grouping.dependency.dependent) != 1 {
			t.Fatal("system payload failed to intern", err)
		}
		other := system.(plan.SystemAttributeExpr)
		other.QueryOccurrence = 2
		if _, err := b.RewriteGroup(other, keys); err == nil || err.(*sqlstate.Error).Code != "42803" {
			t.Fatal("system source used owner OID instead of occurrence", err)
		}
		old := b.Grouping.dependency
		restore := b.EnterQuery(false)
		if b.Grouping.dependency != nil {
			t.Fatal("proof cache escaped its query frame")
		}
		restore()
		if b.Grouping.dependency != old {
			t.Fatal("outer proof cache lost at query boundary")
		}
	})
}
