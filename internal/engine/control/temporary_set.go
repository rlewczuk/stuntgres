package control

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// TemporarySet returns the live temporary objects a persistent replacement
// keeps: every session-owned temporary namespace of current and the members
// of those namespaces. current is the published catalog the caller holds
// under Engine.mu; next is the replacement candidate. A live temporary
// extension whose name the candidate binds to another object fails.
func TemporarySet(current catalog.Read, next *catalog.State) (map[ids.ObjectAddress]catalog.Descriptor, error) {
	temporary := make(map[ids.ObjectAddress]catalog.Descriptor)
	for a, d := range current.State().Descriptors() {
		if ns, ok := d.(*catalog.Namespace); ok && ns.Temporary != (ids.SessionToken{}) {
			temporary[a] = d
		}
	}
	namespaces := make(map[ids.ObjectAddress]bool, len(temporary))
	for address := range temporary {
		namespaces[address] = true
	}
	children, err := catalog.NamespaceChildren(current, namespaces)
	if err != nil {
		return nil, err
	}
	for _, address := range children {
		descriptor := current.State().Get(address)
		if extension, ok := descriptor.(*catalog.Extension); ok {
			if imported, exists := next.LookupName(catalog.DescriptorNameKey(extension)); exists && next.Get(imported) != extension {
				return nil, fmt.Errorf("stuntgres: catalog replacement conflicts with live temporary extension %q", extension.Name())
			}
		}
		temporary[address] = descriptor
	}
	return temporary, nil
}

// CheckTemporaryDependencies rejects a candidate that would substitute an
// imported object for an exact dependency of a retained temporary relation:
// a policy source or dependency, or a user domain under an array column.
// Persistent replacement cannot substitute imported domains for a live TEMP
// array's exact dependency identities, so this runs before any allocator or
// candidate state work.
func CheckTemporaryDependencies(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) error {
	for _, descriptor := range temporary {
		relation, ok := descriptor.(*catalog.Relation)
		if !ok {
			continue
		}
		for _, policy := range relation.Security.Policies {
			for _, condition := range []*stored.Policy{stored.PolicyOf(policy.Using), stored.PolicyOf(policy.Check)} {
				if condition == nil {
					continue
				}
				for _, leaf := range condition.Presentation().Sources() {
					if _, retained := temporary[leaf.Ref.Address]; retained {
						continue
					}
					if next.Get(leaf.Ref.Address) != current.State().Get(leaf.Ref.Address) {
						return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported")
					}
				}
				for _, dependency := range condition.Presentation().Dependencies() {
					address := dependency.Ref.Address
					address.SubID = 0
					if _, retained := temporary[address]; retained {
						continue
					}
					if next.Get(address) != current.State().Get(address) {
						return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary policy dependencies is unsupported")
					}
				}
			}
		}
		for _, column := range relation.Columns {
			typ := column.Usage.Type
			if !typ.BaseType().IsArray() || !arrayDomainDependency(typ) {
				continue
			}
			if !retainedArrayDependencies(current.State(), next, typ) {
				return fmt.Errorf("stuntgres: catalog replacement with incompatible live temporary user-domain array dependencies is unsupported")
			}
		}
	}
	return nil
}

func arrayDomainDependency(typ *sqltype.Descriptor) bool {
	if typ.IsDomain() {
		return true
	}
	return typ.IsArray() && arrayDomainDependency(typ.Element())
}
func retainedArrayDependencies(source, target *catalog.State, typ *sqltype.Descriptor) bool {
	address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
	old, ok := source.Type(address)
	if !ok {
		return false
	}
	next, ok := target.Type(address)
	if !ok || old.SQLType != typ || next.SQLType != typ || old.Array != next.Array || old.Namespace() != next.Namespace() || old.Owner != next.Owner || old.Revision() != next.Revision() {
		return false
	}
	if typ.IsDomain() {
		return retainedArrayDependencies(source, target, typ.DomainBase().Type)
	}
	if typ.IsArray() {
		return retainedArrayDependencies(source, target, typ.Element())
	}
	return true
}
