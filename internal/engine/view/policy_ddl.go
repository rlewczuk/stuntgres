package view

import (
	"fmt"
	"slices"
	"strings"

	"stuntgres/internal/engine/access"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/ddl"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/table"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PlanPolicy decides CREATE, ALTER or DROP POLICY after the root's
// preflight: it resolves the roles and the relation (locked is the admitted
// identity), checks ownership and the relation kind, binds the USING and WITH
// CHECK expressions, and returns the relation with its policy list changed,
// a new revision and the descriptors the expressions read. An IF EXISTS miss
// returns only the NOTICE text in Skip.
func PlanPolicy(h Host, auth ddl.Authority, stmt parser.Statement, locked ids.ObjectRef, input sqltype.InputEnvironment) (RelationChange, error) {
	var tbl parser.ObjectName
	var name string
	var rawRoles []parser.GrantGrantee
	var rawUsing, rawCheck parser.Expr
	create, alter, drop := false, false, false
	switch s := stmt.(type) {
	case *parser.CreatePolicy:
		tbl, name, rawRoles, rawUsing, rawCheck = s.Table, s.Name.Text, s.Roles, s.Using, s.Check
		create = true
	case *parser.AlterPolicy:
		tbl, name, rawRoles, rawUsing, rawCheck = s.Table, s.Name.Text, s.Roles, s.Using, s.Check
		alter = true
	case *parser.DropPolicy:
		tbl, name = s.Table, s.Name.Text
		drop = true
	}
	catalogAccess := h.AccessView(auth)
	roles, _, err := ResolvePolicyRoles(catalogAccess, rawRoles)
	if err != nil {
		return RelationChange{}, err
	}
	ref, err := h.ResolveRelation(auth, tbl)
	if s, ok := stmt.(*parser.DropPolicy); ok && s.IfExists && table.RenameMissing(err) {
		message := fmt.Sprintf(`relation "%s" does not exist, skipping`, tbl.Parts[len(tbl.Parts)-1].Text)
		if diagnostic, ok := err.(*sqlstate.Error); ok && diagnostic.Code == "3F000" {
			message = fmt.Sprintf(`schema "%s" does not exist, skipping`, tbl.Parts[0].Text)
		}
		return RelationChange{Skip: message}, nil
	}
	if err != nil {
		return RelationChange{}, err
	}
	if ref != locked {
		return RelationChange{}, ids.ErrCatalogReplaced
	}
	old := catalogAccess.Catalog.MustRelation(ref.Address)
	index := PolicyIndex(old, name)
	if drop && index < 0 {
		if stmt.(*parser.DropPolicy).IfExists {
			return RelationChange{Skip: fmt.Sprintf(`policy "%s" for relation "%s" does not exist, skipping`, name, old.Name())}, nil
		}
		return RelationChange{}, PolicyMissing(name, old)
	}
	if err = catalogAccess.Check(auth, access.Requirement{Object: ref, OwnerOnly: true, OwnerNoun: PolicyOwnerNoun(stmt)}); err != nil {
		return RelationChange{}, err
	}
	if old.RelationKind == catalog.CatalogRelation {
		return RelationChange{}, sqlError("42501", fmt.Sprintf(`permission denied: "%s" is a system catalog`, old.Name()))
	}
	if old.RelationKind != catalog.TableRelation && old.RelationKind != catalog.PartitionedRelation {
		return RelationChange{}, sqlError("42809", fmt.Sprintf(`"%s" is not a table`, old.Name()))
	}
	var using, check *stored.Policy
	if !drop {
		using, err = h.BindPolicyExpression(auth, old, rawUsing, input)
		if err == nil {
			check, err = h.BindPolicyExpression(auth, old, rawCheck, input)
		}
		if err != nil {
			return RelationChange{}, err
		}
	}
	next := *old
	next.Security.Policies = slices.Clone(old.Security.Policies)
	if create {
		if index >= 0 {
			return RelationChange{}, PolicyDuplicate(name, old)
		}
		oid, err := h.AllocateOID()
		if err != nil {
			return RelationChange{}, err
		}
		s := stmt.(*parser.CreatePolicy)
		next.Security.Policies = append(next.Security.Policies, catalog.PolicyDefinition{
			OID: oid, Name: name, Permissive: s.Permissive, Command: catalog.PolicyCommand(s.Command),
			Roles: roles, Using: PolicyPayloadOf(using), Check: PolicyPayloadOf(check),
		})
	} else if alter {
		s := stmt.(*parser.AlterPolicy)
		if s.Rename != nil && PolicyIndex(old, s.Rename.Text) >= 0 {
			return RelationChange{}, PolicyDuplicate(s.Rename.Text, old)
		}
		if index < 0 {
			return RelationChange{}, PolicyMissing(name, old)
		}
		if err = PolicyClauses(old.Security.Policies[index].Command, rawUsing, rawCheck, false); err != nil {
			return RelationChange{}, err
		}
		p := &next.Security.Policies[index]
		if s.Rename != nil {
			p.Name = s.Rename.Text
		}
		if rawRoles != nil {
			p.Roles = roles
		}
		if rawUsing != nil {
			p.Using = PolicyPayloadOf(using)
		}
		if rawCheck != nil {
			p.Check = PolicyPayloadOf(check)
		}
	} else {
		next.Security.Policies = append(next.Security.Policies[:index], next.Security.Policies[index+1:]...)
	}
	slices.SortFunc(next.Security.Policies, func(a, b catalog.PolicyDefinition) int { return strings.Compare(a.Name, b.Name) })
	next.ObjectRevision, err = h.Revision()
	if err != nil {
		return RelationChange{}, err
	}
	var sources []catalog.Descriptor
	for _, p := range next.Security.Policies {
		for _, expression := range []*stored.Policy{stored.PolicyOf(p.Using), stored.PolicyOf(p.Check)} {
			if expression == nil {
				continue
			}
			for _, leaf := range expression.Presentation().Sources() {
				if descriptor, ok := catalogAccess.Catalog.Descriptor(leaf.Ref.Address); ok {
					sources = append(sources, descriptor)
				}
			}
		}
	}
	return RelationChange{Ref: ref, Next: &next, Sources: sources}, nil
}
