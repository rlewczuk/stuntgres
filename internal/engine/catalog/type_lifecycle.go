package catalog

import (
	"fmt"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqlstate"
)

// ArrayTypeOwner resolves genuine internal ownership, including displaced
// companions. Equal numeric IDs or underscore spellings are not authority.
func ArrayTypeOwner(v Read, array *Type) (*Type, error) {
	if array == nil || !array.SQLType.IsArray() || array.SQLType.IsCatalogVector() {
		return nil, fmt.Errorf("stuntgres: invalid array ownership")
	}
	registered, ok := v.Type(array.Address())
	if !ok || registered != array {
		return nil, fmt.Errorf("stuntgres: foreign array ownership")
	}
	element, ok := v.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(array.SQLType.Element().ID())})
	if !ok || element.SQLType != array.SQLType.Element() || element.Array != array.SQLType.ID() || element.Namespace() != array.Namespace() || element.Owner != array.Owner {
		return nil, fmt.Errorf("stuntgres: invalid reciprocal array ownership")
	}
	return element, nil
}

// TypeAlterProtection rejects internally owned identities without confusing a
// standalone composite's backing relation with a relation-owned row type.
func TypeAlterProtection(v Read, typ *Type, path []ids.ObjectAddress) error {
	if err := TypeAlterArrayProtection(v, typ, path); err != nil {
		return err
	}
	return TypeAlterRowProtection(v, typ, path)
}

// TypeAlterArrayProtection precedes schema-switch and destination-name checks.
func TypeAlterArrayProtection(v Read, typ *Type, path []ids.ObjectAddress) error {
	if typ.SQLType.IsArray() && !typ.SQLType.IsCatalogVector() {
		element, err := ArrayTypeOwner(v, typ)
		if err != nil {
			return err
		}
		name := TypeDiagnosticNameAt(v, nil, typ, path)
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot alter array type " + name, Hint: "You can alter type " + TypeDiagnosticNameAt(v, nil, element, path) + ", which will alter the array type as well."}
	}
	return nil
}

// TypeAlterRowProtection follows a schema move's base-name collision check,
// but precedes its companion-name check and any descriptor mutation.
func TypeAlterRowProtection(v Read, typ *Type, path []ids.ObjectAddress) error {
	if typ.OwningRelation != (ids.ObjectAddress{}) && v.MustRelation(typ.OwningRelation).RelationKind != CompositeRelation {
		name := TypeDiagnosticNameAt(v, nil, typ, path)
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: name + " is a table's row type", Hint: "Use ALTER TABLE instead."}
	}
	return nil
}

// MultirangeOwnerProtection follows the current reciprocal range identity.
// Generated companions cannot independently change ownership.
func MultirangeOwnerProtection(v Read, typ *Type, path []ids.ObjectAddress) error {
	if !typ.SQLType.IsMultirange() {
		return nil
	}
	owner, err := TypeUsageAuthority(v, typ)
	if err != nil {
		return err
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42809", Message: "cannot alter multirange type " + TypeDiagnosticNameAt(v, nil, typ, path), Hint: "You can alter type " + TypeDiagnosticNameAt(v, nil, owner, path) + ", which will alter the multirange type as well."}
}

// TypeDropProtection is for fully resolved, authorized explicit SQL roots only.
// A genuine companion may defer to its pending explicit internal owner, which
// must still independently pass this protection. Keep every original root for
// dependency diagnostics and the existing idempotent retirement closure.
func TypeDropProtection(v Read, typ *Type, path []ids.ObjectAddress, pending map[ids.ObjectAddress]*Type) error {
	required := typ
	if typ.SQLType.IsArray() && !typ.SQLType.IsCatalogVector() {
		element, err := ArrayTypeOwner(v, typ)
		if err != nil {
			return err
		}
		required = element
		if pending[element.Address()] == element {
			return nil
		}
	}
	name := "type " + TypeDiagnosticNameAt(v, nil, typ, path)
	if typ.OwningRelation == (ids.ObjectAddress{}) && !typ.SQLType.IsUserDefined() && !(typ.SQLType.IsArray() && typ.SQLType.Element().IsUserDefined()) {
		return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + name + " because it is required by the database system"}
	}
	owner := ""
	// An explicit companion root follows its internal owner before checking
	// extension/relation ownership. The diagnostic still describes that root.
	if typ.Extension != (ids.ObjectAddress{}) {
		owner = "extension " + DependencyIdentifier(v.Get(typ.Extension).Name())
	} else if required.Extension != (ids.ObjectAddress{}) {
		owner = "extension " + DependencyIdentifier(v.Get(required.Extension).Name())
	} else {
		if required.OwningRelation != (ids.ObjectAddress{}) {
			r := v.MustRelation(required.OwningRelation)
			if r.RelationKind != CompositeRelation {
				owner = RelationDiagnosticKind(r) + " " + TypeDiagnosticNameAt(v, nil, required, path)
			}
		}
		if owner == "" && required != typ {
			owner = "type " + TypeDiagnosticNameAt(v, nil, required, path)
		}
	}
	if owner == "" {
		return nil
	}
	return &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "2BP01", Message: "cannot drop " + name + " because " + owner + " requires it", Hint: "You can drop " + owner + " instead."}
}
