package catalog

import (
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// DescriptorAt reads a descriptor through reader when one is given, else
// from view.
func DescriptorAt(view Read, reader DefinitionReader, address ids.ObjectAddress) (Descriptor, bool) {
	if reader != nil {
		return reader.Descriptor(address)
	}
	return view.Descriptor(address)
}

// AliasAddress is the object a scalar reg* alias value names.
func AliasAddress(value sqltype.Value) (ids.ObjectAddress, error) {
	if value.Type().BaseType().ID() == sqltype.Regtype {
		id, err := value.TypeOID()
		return ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}, err
	}
	id, err := value.ObjectOID()
	class := ids.NamespaceClassID
	if value.Type().BaseType().ID() == sqltype.Regcollation {
		class = ids.CollationClassID
	}
	return ids.ObjectAddress{ClassID: class, ObjectID: ids.OID(id)}, err
}

// WalkAliasReferences records the existing objects a reg* alias value names.
// Visit existing represented identities independently of the datum's datatype.
func WalkAliasReferences(view Read, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	return WalkAliasReferencesAt(view, nil, value, record)
}

// WalkAliasReferencesAt is WalkAliasReferences through a definition reader.
func WalkAliasReferencesAt(view Read, reader DefinitionReader, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	if value.IsNull() || !value.Type().ContainsRegtype() && !value.Type().ContainsRegObject() {
		return nil
	}
	if value.Type().BaseType().IsComposite() {
		for i := range value.CompositeLen() {
			if err := WalkAliasReferencesAt(view, reader, value.CompositeField(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	if value.Type().BaseType().IsArray() {
		for i := range value.ArrayLen() {
			if err := WalkAliasReferencesAt(view, reader, value.ArrayElement(i), record); err != nil {
				return err
			}
		}
		return nil
	}
	address, err := AliasAddress(value)
	if err != nil {
		return err
	}
	if _, exists := DescriptorAt(view, reader, address); exists {
		return record(address)
	}
	return nil
}

// WalkAliasDependencies records the dependencies of a reg* alias constant.
// PostgreSQL dependency.c examines the exact Const datatype. The new aliases'
// opaque compound payloads are not scalar Const children. REGTYPE retains its
// existing recursive dependency behavior; replay/remap uses the full walker.
func WalkAliasDependencies(view Read, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	return WalkAliasDependenciesAt(view, nil, value, record)
}

// WalkAliasDependenciesAt is WalkAliasDependencies through a definition reader.
func WalkAliasDependenciesAt(view Read, reader DefinitionReader, value sqltype.Value, record func(ids.ObjectAddress) error) error {
	typ := value.Type()
	// Structural dependency nodes may have declared Usage but no materialized
	// datum. Their datatype edge is recorded by the expression visitor, not here.
	if value.IsNull() || typ == nil {
		return nil
	}
	if sqltype.IsRegObjectAlias(typ.ID()) {
		return WalkAliasReferencesAt(view, reader, value, record)
	}
	if !typ.ContainsRegtype() {
		return nil
	}
	return WalkAliasReferencesAt(view, reader, value, func(address ids.ObjectAddress) error {
		if address.ClassID == ids.TypeClassID {
			return record(address)
		}
		return nil
	})
}
