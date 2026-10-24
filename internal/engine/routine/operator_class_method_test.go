package routine

import (
	"context"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestOperatorClassMethodIdentity(t *testing.T) {
	// Distinct immutable descriptors share physical mechanics, not SQL identity.
	facts := [2]*catalog.AccessMethod{
		{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 403}, ObjectName: "btree", ObjectRevision: 1, Handler: 330, Native: catalog.BtreeAccessMethod},
		{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodClassID, ObjectID: 90000}, ObjectName: "other_btree", ObjectRevision: 1, Handler: 330, Native: catalog.BtreeAccessMethod},
	}
	state := catalog.NewState(1)
	ns := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	namespace := &catalog.Namespace{ObjectAddress: ns, ObjectName: "pg_catalog", ObjectRevision: 1}
	state.SetDescriptor(ns, namespace)
	state.BindName(catalog.DescriptorNameKey(namespace), ns)
	procedures := BuiltinProcedures(authorization.PrincipalRef{})
	for i := range procedures {
		state.SetDescriptor(procedures[i].Address(), &procedures[i])
	}
	candidate := catalog.NewCandidate(catalog.NewRead(nil, state))
	for i, method := range facts {
		if err := candidate.Create(method); err != nil {
			t.Fatal(err)
		}
		family := &catalog.OperatorFamily{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: ids.OID(91000 + i)}, ObjectNamespace: ns, ObjectName: "same_family", ObjectRevision: ids.DefinitionRevision(11 + i), Method: method.Address().ObjectID}
		class := &catalog.OperatorClass{ObjectAddress: ids.ObjectAddress{ClassID: ids.OperatorClassCatalogID, ObjectID: ids.OID(92000 + i)}, ObjectNamespace: ns, ObjectName: "same_class", ObjectRevision: ids.DefinitionRevision(21 + i), Method: method.Address().ObjectID, Family: family.Address().ObjectID, InputType: 23, Default: true}
		routine := ids.OID(351)
		if i == 1 {
			routine = 181
		}
		member := &catalog.OperatorFamilySupport{ObjectAddress: ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: ids.OID(93000 + i)}, ObjectRevision: ids.DefinitionRevision(31 + i), Family: class.Family, Left: 23, Right: 23, Function: routine, Number: 1, InternalOwner: class.Address()}
		for _, d := range []catalog.Descriptor{family, class, member} {
			if err := candidate.Create(d); err != nil {
				t.Fatal(err)
			}
		}
	}
	view := catalog.NewRead(nil, candidate.State())
	var typ *sqltype.Descriptor
	for _, d := range sqltype.Builtins() {
		if d.ID() == sqltype.Int4 {
			typ = d
			break
		}
	}
	left, err := sqltype.Integer(typ, 1)
	if err != nil {
		t.Fatal(err)
	}
	right, err := sqltype.Integer(typ, 3)
	if err != nil {
		t.Fatal(err)
	}
	var retained [2]catalog.IndexDefinition
	for i, method := range facts {
		class, ok := view.LookupOperatorClass(ns, method.Address().ObjectID, "same_class")
		if !ok || class.Address().ObjectID != ids.OID(92000+i) || class.Method != method.Address().ObjectID || class.Family != ids.OID(91000+i) || class.Revision() != ids.DefinitionRevision(21+i) {
			t.Fatal("independent class selection", class)
		}
		def, ok := view.DefaultOperatorClass(method.Address().ObjectID, 23)
		if !ok || def != class {
			t.Fatal("independent default selection", def)
		}
		capture, ok := view.IndexClassForMethod(class.Address().ObjectID, method)
		if !ok {
			t.Fatal("class construction failed")
		}
		bound, err := bindClassSupport(view, capture, method)
		if err != nil {
			t.Fatal(err)
		}
		routine := uint32(351)
		if i == 1 {
			routine = 181
		}
		want := sqltype.IndexSupport{Family: uint32(91000 + i), Member: uint32(93000 + i), Routine: routine, Left: 23, Right: 23, Revision: uint64(31 + i), FamilyRevision: uint64(11 + i), RoutineRevision: 1}
		if bound.Support(1) != want {
			t.Fatal("production selector capture", bound.Support(1), want)
		}
		comparison, err := sqltype.BindIndexOrdering(typ.Usage(), bound)
		if err != nil || comparison.Support() != want {
			t.Fatal("ordering lost selected capture", comparison.Support(), err)
		}
		got, err := comparison.Compare(context.Background(), left, right, sqltype.InputEnvironment{})
		if err != nil || got != -1-i {
			t.Fatal("native selected routine comparison", got, err)
		}
		if i == 0 {
			ordinary, err := BindIndexOrdering(view, typ.Usage(), capture)
			if err != nil || ordinary.Support() != want {
				t.Fatal("compiled binder differs from fact binder", ordinary.Support(), err)
			}
		}
		retained[i] = catalog.IndexDefinition{Method: catalog.BtreeMethod, MethodOID: method.Address().ObjectID, Keys: []catalog.IndexKey{{Class: bound}}}
		if _, err := bindClassSupport(view, capture, facts[1-i]); err == nil {
			t.Fatal("crossed method fact accepted")
		}
		wrongKind := *method
		wrongKind.Native = catalog.HeapAccessMethod
		if _, err := bindClassSupport(view, capture, &wrongKind); err == nil {
			t.Fatal("table method accepted")
		}
		without := catalog.CloneState(view.State())
		without.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.AccessMethodProcedureClassID, ObjectID: ids.OID(93000 + i)})
		if _, err := bindClassSupport(catalog.NewRead(nil, without), bound, method); err == nil {
			t.Fatal("missing selected member fell back or reused retained support")
		}
		withoutProc := catalog.CloneState(view.State())
		withoutProc.RemoveDescriptor(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(routine)})
		if _, err := bindClassSupport(catalog.NewRead(nil, withoutProc), capture, method); err == nil {
			t.Fatal("missing real procedure accepted")
		}
		proc, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(routine)})
		if !ok {
			t.Fatal("missing fixture procedure")
		}
		for _, invalid := range []struct {
			name   string
			change func(*catalog.Procedure)
		}{
			{"signature", func(p *catalog.Procedure) { p.Signature = "invalid" }},
			{"result", func(p *catalog.Procedure) { p.Result = 20 }},
			{"native identity", func(p *catalog.Procedure) {
				other, _ := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: ids.OID(532 - routine)})
				p.Builtin = other.Builtin
			}},
		} {
			badProc := *proc
			invalid.change(&badProc)
			badState := catalog.CloneState(view.State())
			badState.SetDescriptor(badProc.Address(), &badProc)
			if _, err := bindClassSupport(catalog.NewRead(nil, badState), capture, method); err == nil {
				t.Fatal("invalid real procedure accepted", invalid.name)
			}
		}
		crossed := catalog.CloneState(view.State())
		bad := *class
		bad.Family = ids.OID(91001 - i)
		crossed.SetDescriptor(bad.Address(), &bad)
		if _, ok := catalog.NewRead(nil, crossed).IndexClassForMethod(bad.Address().ObjectID, method); ok {
			t.Fatal("cross-method family constructed")
		}
		if _, err := bindClassSupport(catalog.NewRead(nil, crossed), bound, method); err == nil {
			t.Fatal("crossed retained family capture accepted")
		}
		bad = *class
		bad.InputType = 20
		crossed.SetDescriptor(bad.Address(), &bad)
		if _, err := bindClassSupport(catalog.NewRead(nil, crossed), bound, method); err == nil {
			t.Fatal("crossed retained input capture accepted")
		}
	}
	if retained[0].Method != retained[1].Method || retained[0].MethodIdentity() != 403 || retained[1].MethodIdentity() != 90000 || retained[0].Keys[0].Class.SameInferenceIdentity(retained[1].Keys[0].Class) {
		t.Fatal("physical implementation collapsed exact identity")
	}
	if retained[0].Keys[0].Class.Support(1).Routine != 351 || retained[1].Keys[0].Class.Support(1).Routine != 181 {
		t.Fatal("retained support changed")
	}
}
