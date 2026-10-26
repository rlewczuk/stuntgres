package index

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/proc"
	"testing"
)

func TestOperatorFamilySupportVisibility(t *testing.T) {
	state := catalog.NewState(1)
	address := func(id ids.OID) ids.ObjectAddress {
		return ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}
	}
	// A released identity without a current Procedure never supplies authority.
	if familySupportImplemented(catalog.NewRead(nil, state), 351) {
		t.Fatal("identity-only implementation")
	}
	native := &catalog.Procedure{ObjectAddress: address(351), RoutineKind: catalog.FunctionRoutine}
	identityOnly := &catalog.Procedure{ObjectAddress: address(3130), RoutineKind: catalog.FunctionRoutine}
	zeroProvider := &catalog.Procedure{ObjectAddress: address(90001), RoutineKind: catalog.FunctionRoutine, Builtin: &catalog.BuiltinRoutineDeclaration{}}
	provider := &catalog.Procedure{ObjectAddress: address(90002), RoutineKind: catalog.FunctionRoutine, Builtin: &catalog.BuiltinRoutineDeclaration{Providers: 1}}
	sql := &catalog.Procedure{ObjectAddress: address(90003), RoutineKind: catalog.FunctionRoutine, SQL: &proc.Definition{}}
	for _, tc := range []struct {
		d    *catalog.Procedure
		want bool
	}{{native, true}, {identityOnly, false}, {zeroProvider, false}, {provider, true}, {sql, true}} {
		state.SetDescriptor(tc.d.Address(), tc.d)
		if got := familySupportImplemented(catalog.NewRead(nil, state), tc.d.Address().ObjectID); got != tc.want {
			t.Fatal("procedure implementation fact", tc.d, got, tc.want)
		}
	}
	for slot := uint8(1); slot <= 4; slot++ {
		d := catalog.NewBrinSupportProcedure(slot, authorization.PrincipalRef{})
		state.SetDescriptor(d.Address(), &d)
		if !familySupportImplemented(catalog.NewRead(nil, state), d.Address().ObjectID) {
			t.Fatal("genuine BRIN", slot)
		}
		bad := d
		bad.Signature = ""
		state.SetDescriptor(bad.Address(), &bad)
		if familySupportImplemented(catalog.NewRead(nil, state), bad.Address().ObjectID) {
			t.Fatal("invalid BRIN identity", slot)
		}
	}
}
