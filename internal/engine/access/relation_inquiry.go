package access

import (
	"context"

	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// RelationPrivilege reads only this captured publication. Attribute validity is
// established by the caller before owner or superuser shortcuts are possible.
// anyColumn scans live positive attributes only; sub identifies a valid column.
func (v View) RelationPrivilege(ctx context.Context, actor uint64, relation *catalog.Relation, sub int32, anyColumn bool, ordinary, options authorization.Privileges) (bool, error) {
	if err := ctx.Err(); err != nil {
		return false, err
	}
	d, err := v.Object(v.Ref(relation.Address()))
	if err != nil {
		return false, err
	}
	relation = d.(*catalog.Relation)
	principal := v.Owner.Principals.Principal(actor)
	role, live := v.Roles.Find(principal)
	if live && role.Superuser {
		return true, nil
	}
	var principals []authorization.PrincipalRef
	if live {
		principals, err = v.Roles.RoleClosure(ctx, principal, true)
		if err != nil {
			return false, err
		}
	}
	// Non-superusers cannot acquire system-catalog write privileges, even from
	// an explicit ACL. Apply this before predefined role privileges as well.
	if relation.RelationKind == catalog.CatalogRelation && !relation.CatalogView {
		ordinary &^= authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.UsagePrivilege
		options &^= authorization.InsertPrivilege | authorization.UpdatePrivilege | authorization.DeletePrivilege | authorization.TruncatePrivilege | authorization.UsagePrivilege
	}
	for _, p := range principals {
		if p == relation.Owner && options != 0 {
			return true, nil
		}
	}
	defaults := authorization.TableGrantPrivileges
	if relation.RelationKind == catalog.SequenceRelation {
		defaults = authorization.SelectPrivilege | authorization.UsagePrivilege | authorization.UpdatePrivilege
	}
	acl := relation.Security.ACL
	rights := authorization.ACLRights(acl, relation.Owner, principals, defaults, false)
	if live {
		rights |= authorization.PredefinedPrivileges(principals)
	}
	if rights&ordinary != 0 || authorization.ACLRights(acl, relation.Owner, principals, defaults, true)&options != 0 {
		return true, nil
	}
	columnRights := func(number int32) bool {
		acl := relation.Security.ColumnACL[number]
		return authorization.ACLRights(acl, authorization.PrincipalRef{}, principals, authorization.ColumnGrantPrivileges, false)&ordinary != 0 || authorization.ACLRights(acl, authorization.PrincipalRef{}, principals, authorization.ColumnGrantPrivileges, true)&options != 0
	}
	if sub != 0 {
		return columnRights(sub), nil
	}
	if anyColumn {
		for _, number := range relation.Attributes {
			if err := ctx.Err(); err != nil {
				return false, err
			}
			if number > 0 && columnRights(number) {
				return true, nil
			}
		}
	}
	return false, nil
}
