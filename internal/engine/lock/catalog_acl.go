package lock

import "stuntgres/internal/engine/ids"

// DefaultACLWriter constructs the exact default-key claim without extending
// ordinary Resource keys. This kind alone interprets High/Low as a creator ID.
func DefaultACLWriter(ref ids.ObjectRef, creator uint64, class uint8) Resource {
	return Resource{ObjectRef: ref, Kind: DefaultACLResource, High: uint32(creator >> 32), Low: uint32(creator), DefaultClass: class}
}

// DefaultCreator decodes a DefaultACLResource's actual creator identity.
func (r Resource) DefaultCreator() uint64 { return uint64(r.High)<<32 | uint64(r.Low) }

// ACL claims are internal catalog-writer resources, not relation AX locks.
// The engine validates principal provenance and default-class meaning before
// constructing a claim; the manager validates its complete structural identity.
func (m *Manager) validACLResource(t *Ticket, ref Resource, mode Mode) bool {
	validDomain := ref.Domain() == t.ref.Domain() && ref.Epoch == t.ref.Epoch()
	if ref.Kind == CatalogACLResource && ref.Address.ClassID == ids.ParameterClassID {
		validDomain = ref.Domain() == m.roleDomain && ref.Epoch == m.roleEpoch
	}
	if !validDomain || ref.Address.SubID != 0 || ref.Database != nil || ref.RoleTuple ||
		ref.Name != (ids.NameKey{}) || ref.Row != 0 || ref.key != nil || ref.completion != nil ||
		mode != AccessExclusive {
		return false
	}
	if ref.Kind == DefaultACLResource {
		return ref.DefaultCreator() != 0 && ref.DefaultClass >= 1 && ref.DefaultClass <= 6 &&
			(ref.Address == (ids.ObjectAddress{}) || ref.Address.ClassID == ids.NamespaceClassID && ref.Address.ObjectID != 0)
	}
	if ref.High != 0 || ref.Low != 0 || ref.DefaultClass != 0 || ref.Address.ObjectID == 0 {
		return false
	}
	switch ref.Address.ClassID {
	case ids.RelationClassID, ids.NamespaceClassID, ids.TypeClassID, ids.ProcedureClassID,
		ids.LanguageClassID, ids.ParameterClassID, ids.TablespaceClassID, ids.LargeObjectClassID:
		return true
	}
	return false
}
