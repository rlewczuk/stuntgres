package regobj

import (
	"fmt"
	"strconv"
	"strings"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

func OperatorFamilyDescription(view catalog.Read, d catalog.Descriptor, path []ids.ObjectAddress) (string, bool) {
	var familyID ids.OID
	var left, right ids.OID
	var number int16
	var implementation ids.OID
	var function bool
	switch d := d.(type) {
	case *catalog.OperatorClass:
		name := catalog.DependencyIdentifier(d.Name())
		if path != nil && !NamedObjectVisible(view, path, d) {
			name = catalog.DependencyIdentifier(view.Get(d.Namespace()).Name()) + "." + name
		}
		method, ok := catalog.AccessMethodByOID(d.Method)
		if !ok {
			return "", false
		}
		return "operator class " + name + " for access method " + method.Name, true
	case *catalog.OperatorFamily:
		familyID = d.Address().ObjectID
	case *catalog.OperatorFamilyOperator:
		familyID, left, right, number, implementation = d.Family, d.Left, d.Right, d.Number, d.Operator
	case *catalog.OperatorFamilySupport:
		familyID, left, right, number, implementation, function = d.Family, d.Left, d.Right, d.Number, d.Function, true
	default:
		return "", false
	}
	f, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: familyID})
	if !ok {
		return "", false
	}
	name := catalog.DependencyIdentifier(f.Name())
	visible := path == nil
	for _, ns := range path {
		if a := view.NameAddress(catalog.FamilyNameKey(ns, f.Method, f.Name())); a != (ids.ObjectAddress{}) {
			visible = a == f.Address()
			break
		}
	}
	if !visible {
		name = catalog.DependencyIdentifier(view.Get(f.Namespace()).Name()) + "." + name
	}
	fact, ok := catalog.AccessMethodByOID(f.Method)
	if !ok {
		return "", false
	}
	method := fact.Name
	label := "operator family " + name + " for access method " + method
	if number == 0 {
		return label, true
	}
	kind := "operator"
	var impl string
	if function {
		kind = "function"
		identity, ok := catalog.BuiltinRoutineIdentityByOID(implementation)
		if proc, present := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: implementation}); present {
			identity.Name = proc.Name()
			identity.Signature = proc.Signature
			ok = true
		}
		if ok {
			var text strings.Builder
			text.WriteString(identity.Name)
			text.WriteByte('(')
			WriteRoutineArguments(&text, view, path, identity.Signature, true)
			text.WriteByte(')')
			impl = text.String()
		}
	} else {
		impl = OperatorSpelling(view, path, CollectOperatorGroups(view), implementation, sqltype.Regoperator)
	}
	return fmt.Sprintf("%s %d (%s, %s) of %s: %s", kind, number, TypeSpelling(view, path, left), TypeSpelling(view, path, right), label, impl), true
}

func identifyOperatorFamilyMember(view catalog.Read, path []ids.ObjectAddress, d catalog.Descriptor, r ObjectIdentity) (ObjectIdentity, bool) {
	var family, left, right ids.OID
	var number int16
	kind := "operator"
	switch d := d.(type) {
	case *catalog.OperatorFamilyOperator:
		family, left, right, number = d.Family, d.Left, d.Right, d.Number
	case *catalog.OperatorFamilySupport:
		family, left, right, number = d.Family, d.Left, d.Right, d.Number
		kind = "function"
	default:
		return r, false
	}
	f, ok := view.OperatorFamily(ids.ObjectAddress{ClassID: ids.OperatorFamilyClassID, ObjectID: family})
	if !ok {
		return r, true
	}
	_, namespace := objectNamespace(view, f.Namespace())
	fact, ok := catalog.AccessMethodByOID(f.Method)
	if !ok {
		return r, true
	}
	method := fact.Name
	ltype, rtype := objectQualifiedType(view, left), objectQualifiedType(view, right)
	r.Found = true
	r.Names = []string{method, namespace, f.Name(), strconv.Itoa(int(number))}
	r.Args = []string{ltype, rtype}
	r.Identity = fmt.Sprintf("%s %d (%s, %s) of %s.%s USING %s", kind, number, ltype, rtype, catalog.Identifier(namespace), catalog.Identifier(f.Name()), method)
	r.Description, _ = OperatorFamilyDescription(view, d, path)
	return r, true
}
