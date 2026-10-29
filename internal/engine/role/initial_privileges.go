package role

import (
	"context"

	"stuntgres/internal/engine/authorization"
)

// RemoveRoleFromInitPriv revokes selected grantees as the current object owner,
// with RESTRICT. Unlike live DROP OWNED cleanup, it neither adopts historical
// grantors nor cascades their grants. Bare ALTER OWNER can therefore leave an
// initial-ACL dependency that requires REASSIGN OWNED rather than DROP OWNED.
func cleanupInitialACL(ctx context.Context, roles authorization.Read, acl authorization.ACLValue, owner authorization.PrincipalRef, sources map[authorization.PrincipalRef]bool) (authorization.ACLValue, bool, error) {
	var grantees []authorization.ACLGrantee
	var mask authorization.Privileges
	for _, entry := range acl.Entries {
		if err := ctx.Err(); err != nil {
			return acl, false, err
		}
		if entry.Grantor == owner && !entry.Grantee.Public && sources[entry.Grantee.Role] {
			grantees = append(grantees, entry.Grantee)
			mask |= entry.Privileges | entry.GrantOptions
		}
	}
	return authorization.ChangeObjectACL(ctx, roles, acl, owner, owner, grantees, mask, false, true, false, false)
}
