package access

import (
	"context"
	"fmt"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ids"
)

// DatabaseRights reads one captured catalog/role pair; callers own its coherence
// and the engine mutex when observing live roots. It invokes no host callback.
func DatabaseRights(ctx context.Context, view catalog.Read, roles authorization.Read, principal authorization.PrincipalRef) (authorization.Privileges, error) {
	if err := ctx.Err(); err != nil {
		return 0, err
	}
	actor, err := roles.Descriptor(principal)
	if err != nil {
		return 0, err
	}
	if actor.Superuser {
		return authorization.DatabasePrivileges, nil
	}
	principals, err := roles.RoleClosure(ctx, principal, true)
	if err != nil {
		return 0, err
	}
	db := view.MustDatabase(view.Owner().DatabaseAddress())
	// Avoid materializing a default ACL merely to inspect its ordinary rights.
	rights := authorization.ACLRights(db.ACL, db.Owner, principals, authorization.DatabasePrivileges, false)
	if !db.ACL.Explicit {
		rights |= authorization.DatabasePublicPrivileges
	}
	return rights & authorization.DatabasePrivileges, nil
}

// PredefinedPrivileges returns the rights that predefined role memberships
// among principals grant on d.
func PredefinedPrivileges(d catalog.Descriptor, principals []authorization.PrincipalRef) authorization.Privileges {
	rights := authorization.PredefinedPrivileges(principals)
	if d.Kind() == ids.NamespaceObject {
		if rights&(authorization.SelectPrivilege|authorization.InsertPrivilege|authorization.UpdatePrivilege|authorization.DeletePrivilege) != 0 {
			return authorization.UsagePrivilege
		}
		return 0
	}
	r, ok := d.(*catalog.Relation)
	if !ok {
		return 0
	}
	if r.VirtualView || r.RelationKind == catalog.CatalogRelation {
		return rights & authorization.SelectPrivilege
	}
	switch r.RelationKind {
	case catalog.SequenceRelation:
		return rights & (authorization.SelectPrivilege | authorization.UpdatePrivilege)
	case catalog.TableRelation, catalog.PartitionedRelation, catalog.ViewRelation, catalog.MaterializedViewRelation:
		return rights
	}
	return 0
}

// REL_18_0 ObjectProperty, not the native provider inventory. A catalog without
// native instances can still be a recognized address class. pg_largeobject is
// the released alias for pg_largeobject_metadata. pg_parameter_acl is not listed.
func ObjectACLClass(class ids.OID) (hasACL, known bool) {
	switch class {
	case 1259, 2615, 1247, 1255, 1262, 2612, 1213, 2328, 1417, 2995, 2613:
		return true, true
	case 2601, 2602, 2603, 2605, 3456, 2606, 2607, 826, 3079,
		2616, 2617, 2753, 1260, 1261, 2618, 3576, 2620, 3256, 3466,
		3602, 3600, 3601, 3764, 6104, 6100, 3381, 1418:
		return false, true
	}
	return false, false
}

// RawObjectACL returns the stored ACL at address, the zero value for classes
// without ACLs, and an error for unrecognized classes.
func RawObjectACL(view catalog.Read, address ids.ObjectAddress) (authorization.ACLValue, error) {
	if address.ClassID == 0 && address.ObjectID == 0 {
		return authorization.ACLValue{}, nil
	}
	if address.ClassID == 2613 {
		address.ClassID = 2995
	}
	hasACL, known := ObjectACLClass(address.ClassID)
	if !known {
		return authorization.ACLValue{}, sqlError("XX000", fmt.Sprintf("unrecognized class ID: %d", address.ClassID))
	}
	if !hasACL {
		return authorization.ACLValue{}, nil
	}
	key := address
	key.SubID = 0
	d := view.Get(key)
	if address.ClassID == ids.RelationClassID && address.SubID != 0 {
		// AttrNumber is signed int16. Narrow only after choosing the column
		// path, so wrapped zero never becomes a whole-object ACL request.
		subid := int32(int16(address.SubID))
		if relation, ok := d.(*catalog.Relation); ok && subid > 0 && relation.HasAttribute(subid) {
			return relation.Security.ColumnACL[subid], nil
		}
		return authorization.ACLValue{}, nil
	}
	if typ, ok := d.(*catalog.Type); ok && typ.ACLSyntheticDefault {
		return authorization.ACLValue{}, nil
	}
	_, acl, _ := DescriptorACL(d)
	return acl, nil
}
