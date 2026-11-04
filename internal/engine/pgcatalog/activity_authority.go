package pgcatalog

import "stuntgres/internal/engine/authorization"

// Activity ownership is the immutable authenticated principal; the observer's
// current inherited authority is resolved anew for every projection.
func activityAllStats(in Input) (bool, error) {
	observer, err := in.Roles.Descriptor(in.ActivityObserver)
	if err != nil {
		return false, err
	}
	if observer.Superuser {
		return true, nil
	}
	if stats, ok := in.Roles.Lookup("pg_read_all_stats"); ok {
		return in.Roles.InheritsRole(in.ActivityObserver, stats.Ref)
	}
	return false, nil
}

func activityAccess(in Input, principal authorization.PrincipalRef, allStats bool) (authorization.RoleDescriptor, bool, bool, error) {
	// A retained physical identity may outlive its role. Validate provenance,
	// but do not adopt a recreated name or erase valid all-statistics authority.
	if err := in.Roles.ValidatePrincipal(principal); err != nil {
		return authorization.RoleDescriptor{}, false, false, err
	}
	role, present := in.Roles.Find(principal)
	allowed := allStats
	if !allowed && present {
		var err error
		allowed, err = in.Roles.InheritsRole(in.ActivityObserver, principal)
		if err != nil {
			return authorization.RoleDescriptor{}, false, false, err
		}
	}
	return role, present, allowed, nil
}
