package access

import (
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
)

// RLSClause retains the original immutable payload and diagnostic identity.
// Applicability is selected once per command, never during row evaluation.
type RLSClause struct {
	Name    string
	Payload catalog.PolicyPayload
}
type RLSGroup struct {
	Permissive  []RLSClause
	Restrictive []RLSClause
}
type RLSSelection struct {
	Groups [2]RLSGroup
	Count  int
}

func (s RLSSelection) Denied() bool {
	for i := range s.Count {
		if len(s.Groups[i].Permissive) == 0 {
			return true
		}
	}
	return false
}

// SelectRowSecurity keeps command and SELECT groups separate. SELECT NEW uses
// USING even when the same ALL policy supplies a distinct write CHECK clause.
func SelectRowSecurity(roles authorization.Read, d *catalog.Relation, principal authorization.PrincipalRef, command catalog.PolicyCommand, proposed, selectRead bool) (RLSSelection, error) {
	var result RLSSelection
	applies, err := RowSecurityApplies(roles, d, principal)
	if err != nil || !applies {
		return result, err
	}
	result.Count = 1
	if selectRead && command != catalog.PolicySelect {
		result.Count = 2
	}
	for i := range result.Count {
		current := command
		if i == 1 {
			current = catalog.PolicySelect
		}
		group := &result.Groups[i]
		for _, policy := range d.Security.Policies {
			if policy.Command != catalog.PolicyAll && policy.Command != current {
				continue
			}
			matches := false
			for _, role := range policy.Roles {
				if role.Public {
					matches = true
					break
				}
				inherited, err := roles.InheritsRole(principal, role.Ref)
				if err != nil {
					return RLSSelection{}, err
				}
				if inherited {
					matches = true
					break
				}
			}
			if !matches {
				continue
			}
			payload := policy.Using
			if proposed && current != catalog.PolicySelect {
				payload = policy.Check
				if payload == nil && (policy.Command == catalog.PolicyAll || policy.Command == catalog.PolicyUpdate) {
					payload = policy.Using
				}
			}
			if payload == nil {
				continue
			}
			clause := RLSClause{Name: policy.Name, Payload: payload}
			if policy.Permissive {
				group.Permissive = append(group.Permissive, clause)
			} else {
				group.Restrictive = append(group.Restrictive, clause)
			}
		}
	}
	return result, nil
}
