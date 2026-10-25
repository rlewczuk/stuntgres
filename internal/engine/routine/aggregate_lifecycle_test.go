package routine

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"testing"
)

func TestAggregateLifecycleKind(t *testing.T) {
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: 900}
	state := catalog.NewState(1)
	for i, kind := range []catalog.RoutineKind{catalog.FunctionRoutine, catalog.ProcedureRoutine, catalog.AggregateRoutine} {
		p := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(910 + i)}, ObjectNamespace: ns, ObjectName: "pick", Signature: catalog.ProcedureSignature([]string{"23", "25", "20"}[i]), RoutineKind: kind}
		state.SetDescriptor(p.Address(), p)
		state.BindName(catalog.DescriptorNameKey(p), p.Address())
	}
	state.SetProcedureNames(catalog.RebuildProcedureNames(state.Descriptors()))
	view := catalog.NewRead(nil, state)
	for i, sig := range []catalog.ProcedureSignature{"23", "25", "20"} {
		p, count := selectUtilityRoutine(view, []ids.ObjectAddress{ns}, "pick", "aggregate", &sig)
		if count != 1 || p.Address().ObjectID != ids.OID(910+i) {
			t.Fatal("kind filtered before exact signature", sig, p, count)
		}
	}
	if utilityRoutineNoun("aggregate") != "aggregate" || utilityRoutineNoun("routine") != "function" {
		t.Fatal("utility diagnostic nouns")
	}
	if text, err := utilityRoutineSignatureDiagnostic(nil, nil, ""); err != nil || text != "" {
		t.Fatal("zero input diagnostic", text, err)
	}
}
