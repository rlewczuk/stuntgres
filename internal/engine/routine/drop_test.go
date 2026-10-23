package routine

import (
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
)

func TestRoutineDropSelection(t *testing.T) {
	early := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}
	later := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 901}
	state := catalog.NewState(1)
	add := func(id ids.OID, ns ids.ObjectAddress, signature catalog.ProcedureSignature, kind catalog.RoutineKind) *catalog.Procedure {
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}, ObjectNamespace: ns, ObjectName: "pick", Signature: signature, RoutineKind: kind}
		state.SetDescriptor(p.Address(), p)
		state.BindName(catalog.DescriptorNameKey(p), p.Address())
		return p
	}
	f := add(910, early, "23", catalog.FunctionRoutine)
	p := add(911, later, "23", catalog.ProcedureRoutine)
	w := add(912, later, "25", catalog.WindowRoutine)
	a := add(913, later, "20", catalog.AggregateRoutine)
	state.SetProcedureNames(catalog.RebuildProcedureNames(state.Descriptors()))
	view := catalog.NewRead(nil, state)
	path := []ids.ObjectAddress{early, later}
	for _, tc := range []struct {
		kind      string
		signature *catalog.ProcedureSignature
		want      *catalog.Procedure
		count     int
	}{
		{"procedure", nil, p, 1},
		{"procedure", new(catalog.ProcedureSignature("23")), f, 1},
		{"function", nil, a, 3},
		{"routine", nil, a, 3},
		{"function", new(catalog.ProcedureSignature("25")), w, 1},
		{"routine", new(catalog.ProcedureSignature("20")), a, 1},
		{"routine", new(catalog.ProcedureSignature("")), nil, 0},
	} {
		got, count := selectDropRoutine(view, path, "pick", tc.kind, tc.signature)
		if count != tc.count || count == 1 && got != tc.want {
			t.Fatalf("%s: got %p/%d want %p/%d", tc.kind, got, count, tc.want, tc.count)
		}
	}
}

func TestRoutineDropCatalogIsolation(t *testing.T) {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}
	state := catalog.NewState(1)
	var procedures []*catalog.Procedure
	for i, signature := range []catalog.ProcedureSignature{"23", "25"} {
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(910 + i)}, ObjectNamespace: ns, ObjectName: "pick", Signature: signature, SQL: &proc.Definition{}}
		state.SetDescriptor(p.Address(), p)
		state.BindName(catalog.DescriptorNameKey(p), p.Address())
		procedures = append(procedures, p)
	}
	state.SetProcedureNames(catalog.RebuildProcedureNames(state.Descriptors()))
	old := state.ProcedureNames()
	edit := catalog.ProcedureIndexEdit{Index: old}
	edit.Replace(procedures[0], nil)
	if len(old.Buckets[catalog.ProcedureNameKey{Namespace: ns, Name: "pick"}]) != 2 || old.DynamicNames["pick"] != 2 {
		t.Fatal("retained index mutated")
	}
	if len(edit.Index.Buckets[catalog.ProcedureNameKey{Namespace: ns, Name: "pick"}]) != 1 || edit.Index.DynamicNames["pick"] != 1 {
		t.Fatal("survivor membership lost")
	}
	edit.Replace(procedures[1], nil)
	if edit.Index.DynamicNames["pick"] != 0 || len(edit.Index.Buckets[catalog.ProcedureNameKey{Namespace: ns, Name: "pick"}]) != 0 {
		t.Fatal("last-name gate retained")
	}
}
