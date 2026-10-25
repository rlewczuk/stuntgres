package engine

import (
	"testing"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func rangeTypeUnitGeneratedCast(t *testing.T) (catalog.Read, *catalog.Procedure, *catalog.Cast) {
	t.Helper()
	view, r, m := rangeTypeUnitFamily(t, sqltype.Int4, false)
	owner := view.MustType(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}).Owner
	provider := &catalog.RangeConstructor{Type: m, Form: catalog.MultirangeConstructorOne}
	signature, err := provider.Signature(view)
	if err != nil {
		t.Fatal(err)
	}
	procedure := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 920004}, ObjectNamespace: ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PublicNamespaceOID}, ObjectRevision: 2, ObjectName: m.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Result: ids.OID(m.ID()), Owner: owner, Volatility: expr.Immutable, Strict: true, RangeConstructor: provider, ACL: authorization.DefaultBaseline(owner, authorization.DefaultRoutines, false)}
	view.State().SetDescriptor(procedure.Address(), procedure)
	view.State().BindName(catalog.DescriptorNameKey(procedure), procedure.Address())
	procedures := catalog.ProcedureIndexEdit{Index: view.State().ProcedureNames()}
	procedures.Replace(nil, procedure)
	view.State().SetProcedureNames(procedures.Index)
	cast := &catalog.Cast{OID: 920010, ObjectRevision: 2, Source: r.ID(), Target: m.ID(), Function: procedure.Address(), Context: "e", Method: "f", GeneratedRange: ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}}
	view.State().SetDescriptor(cast.Address(), cast)
	view.State().BindName(catalog.DescriptorNameKey(cast), cast.Address())
	return view, procedure, cast
}
