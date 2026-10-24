package typedef

import (
	"fmt"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func stageRangeConstructors(h StageHost, namespace ids.ObjectAddress, r, m *sqltype.Descriptor) error {
	bootstrap, ok := h.StageRead().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(sqltype.Bool)})
	if !ok {
		return fmt.Errorf("missing generated constructor bootstrap owner")
	}
	for _, form := range []catalog.RangeConstructorForm{catalog.RangeConstructorTwo, catalog.RangeConstructorThree, catalog.MultirangeConstructorZero, catalog.MultirangeConstructorOne, catalog.MultirangeConstructorMany} {
		t := r
		if form >= catalog.MultirangeConstructorZero {
			t = m
		}
		provider := &catalog.RangeConstructor{Type: t, Form: form}
		signature, err := provider.Signature(h.StageRead())
		if err != nil {
			return err
		}
		d := &catalog.Procedure{ObjectAddress: ids.ObjectAddress{ClassID: ids.ProcedureClassID}, ObjectNamespace: namespace, ObjectName: t.Name(), Signature: signature, RoutineKind: catalog.FunctionRoutine, Owner: bootstrap.Owner, Result: ids.OID(t.ID()), Volatility: expr.Immutable, Strict: form >= catalog.MultirangeConstructorZero, Display: catalog.RoutineDisplay{Language: catalog.InternalRoutineLanguage, Source: provider.Source()}, RangeConstructor: provider}
		if h.StageRead().NameAddress(catalog.DescriptorNameKey(d)) != (ids.ObjectAddress{}) {
			return sqlError("42723", fmt.Sprintf("function %q already exists with same argument types", d.Name()))
		}
		d.ObjectAddress.ObjectID, err = h.AllocateOID()
		if err != nil {
			return err
		}
		d.ObjectRevision, err = h.Revision()
		if err != nil {
			return err
		}
		d.ACL = h.CreationACL(d.Owner, namespace, authorization.DefaultRoutines)
		if err = h.Stage().Create(d); err != nil {
			return err
		}
		if form == catalog.MultirangeConstructorOne {
			oid, err := h.AllocateOID()
			if err != nil {
				return err
			}
			revision, err := h.Revision()
			if err != nil {
				return err
			}
			cast := &catalog.Cast{OID: oid, ObjectRevision: revision, Source: r.ID(), Target: m.ID(), Function: d.Address(), Context: "e", Method: "f", GeneratedRange: ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(r.ID())}}
			if err := h.Stage().Create(cast); err != nil {
				return err
			}
		}
	}
	return nil
}
