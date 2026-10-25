package enginetests_test

import (
	"slices"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/sqltype"
)

// The DROP closure tests one target against each operator without building its
// dependency list; both forms must agree on every seeded operator.
func TestOperatorDependsOnMatchesDependencyAddresses(t *testing.T) {
	view := engine.New().CatalogRead()
	unrelated := []ids.ObjectAddress{
		{},
		{ClassID: ids.TypeClassID},
		{ClassID: ids.ProcedureClassID},
		{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bytea)},
		{ClassID: ids.ProcedureClassID, ObjectID: 1},
		{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID},
		{ClassID: ids.RelationClassID, ObjectID: ids.OID(sqltype.Int4)},
	}
	operators, unresolved := 0, 0
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Operator)
		if !ok {
			continue
		}
		operators++
		dependencies := routine.OperatorDependencyAddresses(view, d)
		targets := append(slices.Clone(unrelated), d.Address(), d.Namespace())
		for _, id := range [...]ids.OID{d.Left, d.Right, d.Result} {
			targets = append(targets, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id}, ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: id, SubID: 1})
		}
		for _, id := range [...]ids.OID{d.Function, d.Restriction, d.Join} {
			procedure := ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
			if _, ok := view.Find(procedure); id != 0 && !ok {
				unresolved++
			}
			targets = append(targets, procedure, ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id, SubID: 1})
		}
		for _, target := range targets {
			if got, want := routine.OperatorDependsOn(view, d, target), slices.Contains(dependencies, target); got != want {
				t.Fatalf("operator %s (%d) depends on %v = %v, want %v", d.Name(), d.Address().ObjectID, target, got, want)
			}
		}
	}
	if operators == 0 || unresolved == 0 {
		t.Fatalf("precondition: %d seeded operators, %d unresolved support procedures", operators, unresolved)
	}
}
