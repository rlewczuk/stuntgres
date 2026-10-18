package authorization

import (
	"context"
	"fmt"
	"strings"
)

// RolePrivileges are the pg_has_role privilege kinds.
type RolePrivileges uint8

const (
	RoleMember RolePrivileges = 1 << iota
	RoleUsage
	RoleSet
	RoleAdmin
)

func rolePrivilegeSpace(c byte) bool {
	return c == ' ' || c == '\t' || c == '\n' || c == '\r' || c == '\v' || c == '\f'
}
func rolePrivilegeEqual(input, upper string) bool {
	if len(input) != len(upper) {
		return false
	}
	for i := range len(input) {
		c := input[i]
		if c >= 'a' && c <= 'z' {
			c -= 'a' - 'A'
		}
		if c != upper[i] {
			return false
		}
	}
	return true
}

// ParseRolePrivileges parses pg_has_role's comma-separated privilege text.
func ParseRolePrivileges(text string) (RolePrivileges, error) {
	var mask RolePrivileges
	for {
		token, rest, more := strings.Cut(text, ",")
		for len(token) > 0 && rolePrivilegeSpace(token[0]) {
			token = token[1:]
		}
		for len(token) > 0 && rolePrivilegeSpace(token[len(token)-1]) {
			token = token[:len(token)-1]
		}
		switch {
		case rolePrivilegeEqual(token, "MEMBER"):
			mask |= RoleMember
		case rolePrivilegeEqual(token, "USAGE"):
			mask |= RoleUsage
		case rolePrivilegeEqual(token, "SET"):
			mask |= RoleSet
		case rolePrivilegeEqual(token, "MEMBER WITH ADMIN OPTION"), rolePrivilegeEqual(token, "USAGE WITH ADMIN OPTION"), rolePrivilegeEqual(token, "SET WITH ADMIN OPTION"), rolePrivilegeEqual(token, "MEMBER WITH GRANT OPTION"), rolePrivilegeEqual(token, "USAGE WITH GRANT OPTION"), rolePrivilegeEqual(token, "SET WITH GRANT OPTION"):
			mask |= RoleAdmin
		default:
			return 0, sqlError("22023", fmt.Sprintf(`unrecognized privilege type: "%s"`, token))
		}
		if !more {
			return mask, nil
		}
		text = rest
	}
}

// HasRole answers pg_has_role. Raw SQL OIDs are not trusted PrincipalRefs.
// Only this SQL inquiry boundary admits PostgreSQL's self/superuser shortcuts
// for absent endpoints.
func HasRole(ctx context.Context, roles Read, domain *Domain, actor, target uint64, mask RolePrivileges) (bool, error) {
	a, aLive := roles.Find(domain.Principal(actor))
	_, tLive := roles.Find(domain.Principal(target))
	if aLive && a.Superuser {
		return true, nil
	}
	if actor == target {
		return mask&(RoleMember|RoleUsage|RoleSet) != 0, nil
	}
	if !aLive || !tLive {
		return false, nil
	}
	destination := domain.Principal(target)
	if mask&RoleUsage != 0 {
		yes, err := roles.InheritsRole(a.Ref, destination)
		if err != nil || yes {
			return yes, err
		}
	}
	if mask&RoleSet != 0 {
		yes, err := roles.CanSetRole(a.Ref, destination)
		if err != nil || yes {
			return yes, err
		}
	}
	if mask&RoleAdmin != 0 {
		yes, err := roles.AdminOfRole(a.Ref, destination)
		if err != nil || yes {
			return yes, err
		}
	}
	if mask&RoleMember != 0 {
		closure, err := roles.RoleClosure(ctx, a.Ref, false)
		if err != nil {
			return false, err
		}
		for _, ref := range closure {
			if ref == destination {
				return true, nil
			}
		}
	}
	return false, nil
}
