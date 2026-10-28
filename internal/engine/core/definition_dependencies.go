package core

import (
	"strconv"
	"strings"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// visitDefinitionProviders walks the concrete identities consumed by definition
// stages. Its immutable read and visitor belong to the engine, never user code.
// SQL dependency rows and these internal resolution observations serve different
// purposes: pinned providers still need exact generation observations.
func visitDefinitionProviders(view catalog.Read, d catalog.Descriptor, visit func(ids.ObjectAddress) error) error {
	one := func(class, id ids.OID) error {
		if id == 0 {
			return nil
		}
		return visit(ids.ObjectAddress{ClassID: class, ObjectID: id})
	}
	procedure := func(id ids.OID) error {
		if id == 0 {
			return nil
		}
		if err := one(ids.ProcedureClassID, id); err != nil {
			return err
		}
		if p, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: id}); ok {
			if err := one(ids.TypeClassID, p.Result); err != nil {
				return err
			}
			for token := range strings.SplitSeq(string(p.Signature), ",") {
				n, err := strconv.ParseUint(token, 10, 32)
				if err == nil {
					if err = one(ids.TypeClassID, ids.OID(n)); err != nil {
						return err
					}
				}
			}
		}
		return nil
	}
	methodProvider := func(id ids.OID) error {
		if id == 0 {
			return nil
		}
		if err := one(ids.AccessMethodClassID, id); err != nil {
			return err
		}
		if method, ok := view.AccessMethod(id); ok {
			return procedure(method.Handler)
		}
		return nil
	}
	var family, left, right, method, implementation, sort ids.OID
	var owner ids.ObjectAddress
	switch d := d.(type) {
	case *catalog.AccessMethod:
		return procedure(d.Handler)
	case *catalog.OperatorFamily:
		return methodProvider(d.Method)
	case *catalog.OperatorClass:
		if err := methodProvider(d.Method); err != nil {
			return err
		}
		for _, a := range [...]ids.ObjectAddress{{ClassID: ids.OperatorFamilyClassID, ObjectID: d.Family}, {ClassID: ids.TypeClassID, ObjectID: d.InputType}, {ClassID: ids.TypeClassID, ObjectID: d.StorageType}} {
			if a.ObjectID != 0 {
				if err := visit(a); err != nil {
					return err
				}
			}
		}
		return nil
	case *catalog.OperatorFamilySupport:
		family, left, right, implementation, owner = d.Family, d.Left, d.Right, d.Function, d.InternalOwner
	case *catalog.OperatorFamilyOperator:
		family, left, right, method, sort, owner = d.Family, d.Left, d.Right, d.Method, d.SortFamily, d.InternalOwner
		if err := one(ids.OperatorClassID, d.Operator); err != nil {
			return err
		}
		if op, ok := view.Operator(ids.ObjectAddress{ClassID: ids.OperatorClassID, ObjectID: d.Operator}); ok {
			implementation = op.Function
		}
	case *catalog.Relation:
		if err := methodProvider(d.TableMethod); err != nil { return err }
		if d.SQLIndex == nil {
			return nil
		}
		if err := methodProvider(d.SQLIndex.MethodIdentity()); err != nil {
			return err
		}
		for _, key := range d.SQLIndex.Keys {
			if err := one(ids.OperatorClassCatalogID, ids.OID(key.Class.OID())); err != nil {
				return err
			}
			if err := one(ids.OperatorFamilyClassID, ids.OID(key.Class.FamilyOID())); err != nil {
				return err
			}
			for slot := 1; slot <= 2; slot++ {
				support := key.Class.Support(slot)
				if err := one(ids.AccessMethodProcedureClassID, ids.OID(support.Member)); err != nil {
					return err
				}
				if err := procedure(ids.OID(support.Routine)); err != nil {
					return err
				}
			}
		}
		return nil
	default:
		return nil
	}
	if method == 0 && family != 0 {
		if provider, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family}); ok {
			method = provider.Method
		}
	}
	if err := methodProvider(method); err != nil {
		return err
	}
	for _, a := range [...]ids.ObjectAddress{{ClassID: ids.OperatorFamilyClassID, ObjectID: family}, {ClassID: ids.TypeClassID, ObjectID: left}, {ClassID: ids.TypeClassID, ObjectID: right}, {ClassID: ids.OperatorFamilyClassID, ObjectID: sort}, owner} {
		if a.ObjectID != 0 {
			if err := visit(a); err != nil {
				return err
			}
		}
	}
	return procedure(implementation)
}
