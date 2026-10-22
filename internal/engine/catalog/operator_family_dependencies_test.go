package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func operatorFamilyDependencyCases(t *testing.T, view Read) {
	t.Helper()
	// With no type descriptor installed, the real comparator's formal signature
	// alone must cover its integer pair; direct type edges would be redundant.
	covered := &OperatorFamilySupport{Family: 1976, Left: 23, Right: 23, Number: 1, Function: 351}
	if deps := OperatorFamilyDependencies(view, covered); len(deps) != 2 {
		t.Fatal("formal-covered association emitted direct type dependency", deps)
	}
	state := CloneState(view.State())
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			d := &Type{SQLType: typ, ObjectRevision: 1}
			state.SetDescriptor(d.Address(), d)
			break
		}
	}
	enum, err := sqltype.Enum(90011, "family_dependency_type", "pg_catalog.family_dependency_type", []string{"a", "b"})
	if err != nil {
		t.Fatal(err)
	}
	typ := &Type{SQLType: enum, ObjectRevision: 1}
	state.SetDescriptor(typ.Address(), typ)
	v := view.WithState(state)
	// A genuine optional callback does not accept integer, but the pinned type
	// still needs no direct dependency.
	pinned := &OperatorFamilySupport{Family: 1976, Left: 23, Right: 23, Number: 2, Function: 3130}
	if deps := OperatorFamilyDependencies(v, pinned); len(deps) != 2 {
		t.Fatal("pinned association dependency", deps)
	}
	explicit := &OperatorFamilySupport{Family: 1976, Left: 90011, Right: 90011, Number: 1, Function: 351}
	deps := OperatorFamilyDependencies(v, explicit)
	if len(deps) != 3 || deps[2].Address != (ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: 90011}) || deps[2].Kind != 'a' {
		t.Fatal("explicit equal associated pair must emit one AUTO edge", deps)
	}
	ordering := &OperatorFamilyOperator{Family: 3550, Left: 23, Right: 23, Number: 1, Purpose: 'o', Operator: 97, Method: 783, SortFamily: 1976}
	deps = OperatorFamilyDependencies(v, ordering)
	if len(deps) != 3 || deps[2].Address != (ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: 1976}) || deps[2].Kind != 'a' {
		t.Fatal("loose sort family must be AUTO", deps)
	}
}
