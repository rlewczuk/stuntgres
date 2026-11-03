package typedef

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// Retention supplies exact current-generation evidence, not permission to retag
// TEMP values. Check the builtin graph without allocating another visited map:
// every retained member/element/peer is itself an entry in the same finite map.
func validateRetainedBuiltinTypes(next *catalog.State, mapping map[ids.ObjectAddress]ids.ObjectAddress, retained map[sqltype.ID]*sqltype.Descriptor) error {
	matches := func(typ *sqltype.Descriptor) bool {
		if typ == nil || retained[typ.ID()] != typ {
			return false
		}
		current, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())})
		return ok && current.SQLType == typ
	}
	for id, typ := range retained {
		if typ == nil || !typ.IsBuiltin() {
			continue
		}
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(id)}
		if id != typ.ID() || !matches(typ) {
			return fmt.Errorf("stuntgres: incompatible retained builtin generation")
		}
		if to, ok := mapping[address]; ok && to != address {
			return fmt.Errorf("stuntgres: retained builtin type identity cannot be remapped")
		}
		owner := next.MustType(address)
		if to, ok := mapping[owner.Namespace()]; ok && to != owner.Namespace() {
			return fmt.Errorf("stuntgres: retained builtin type namespace cannot be remapped")
		}
		if typ.IsArray() && !matches(typ.Element()) {
			return fmt.Errorf("stuntgres: incompatible retained builtin array element")
		}
		if typ.IsComposite() {
			for i := range typ.CompositeLen() {
				if !matches(typ.CompositeMember(i).Usage.Type) {
					return fmt.Errorf("stuntgres: incomplete retained builtin composite members")
				}
			}
		}
		if owner.Array != 0 {
			array, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(owner.Array)})
			if !ok || !matches(array.SQLType) || array.SQLType.Element() != typ {
				return fmt.Errorf("stuntgres: incompatible retained builtin companion")
			}
		}
		for _, peerID := range sqltype.BuiltinGenerationPeers(id) {
			peer, ok := next.Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(peerID)})
			if !ok {
				return fmt.Errorf("stuntgres: missing retained builtin generation peer")
			}
			// Match the retention protocol's existing domain boundary.
			if peer.SQLType.IsDomain() || peer.SQLType.IsArray() && peer.SQLType.Element().IsDomain() {
				continue
			}
			if !matches(peer.SQLType) {
				return fmt.Errorf("stuntgres: incomplete retained builtin generation peers")
			}
		}
	}
	return nil
}
