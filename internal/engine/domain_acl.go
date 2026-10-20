package engine

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

func DefaultDomainACL(d *catalog.Type) bool {
	if !d.ACLSyntheticDefault || !d.ACL.Explicit {
		return false
	}
	var public, owner, options authorization.Privileges
	for _, entry := range d.ACL.Entries {
		if entry.Grantor != d.Owner {
			return false
		}
		if entry.Grantee.Public {
			if entry.Grantee.Role != (authorization.PrincipalRef{}) || entry.GrantOptions != 0 {
				return false
			}
			public |= entry.Privileges
		} else {
			if entry.Grantee.Role != d.Owner {
				return false
			}
			owner |= entry.Privileges
			options |= entry.GrantOptions
		}
	}
	return public == authorization.UsagePrivilege && owner == authorization.UsagePrivilege && options == 0
}
