package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/dump"
	"stuntgres/internal/engine/ids"
)

func policyDumpGroups(ctx context.Context, view catalog.Read, roles authorization.Read) ([]dump.Group, error) {
	var groups []dump.Group
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*catalog.Relation)
		if !ok || len(d.Security.Policies) == 0 {
			continue
		}
		ns := view.MustNamespace(d.Namespace())
		if ns.Temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		table := catalog.QuoteIdentifier(ns.Name()) + "." + catalog.QuoteIdentifier(d.Name())
		var sql strings.Builder
		for _, policy := range d.Security.Policies {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			option := "PERMISSIVE"
			if !policy.Permissive {
				option = "RESTRICTIVE"
			}
			sql.WriteString("CREATE POLICY " + catalog.QuoteIdentifier(policy.Name) + " ON " + table + " AS " + option + " FOR " + strings.ToUpper(string(policy.Command)) + " TO ")
			for i, role := range policy.Roles {
				if i != 0 {
					sql.WriteString(", ")
				}
				if role.Public {
					sql.WriteString("PUBLIC")
				} else {
					principal, err := roles.Descriptor(role.Ref)
					if err != nil {
						return nil, err
					}
					sql.WriteString(catalog.QuoteIdentifier(principal.Name))
				}
			}
			for _, clause := range []struct {
				name string
				expr *policyExpression
			}{{"USING", policyExpressionOf(policy.Using)}, {"WITH CHECK", policyExpressionOf(policy.Check)}} {
				if clause.expr == nil {
					continue
				}
				for _, leaf := range clause.expr.Presentation().Sources() {
					relation, ok := view.Relation(leaf.ref.Address)
					if !ok {
						return nil, fmt.Errorf("stuntgres: policy provider is missing")
					}
					if view.MustNamespace(relation.Namespace()).Temporary != (ids.SessionToken{}) {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize a policy referencing a temporary relation")
					}
				}
				for _, dependency := range clause.expr.Presentation().Dependencies() {
					address := dependency.Ref.Address
					address.SubID = 0
					provider := view.Get(address)
					if provider == nil {
						return nil, fmt.Errorf("stuntgres: policy provider is missing")
					}
					if namespace, ok := view.Namespace(provider.Namespace()); ok && namespace.Temporary != (ids.SessionToken{}) {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize a policy referencing a temporary relation")
					}
				}
				text, err := policyExpressionSQL(ctx, view, clause.expr)
				if err != nil {
					return nil, err
				}
				sql.WriteString(" " + clause.name + " (" + text + ")")
			}
			sql.WriteString(";\n")
		}
		groups = append(groups, dump.Group{Owner: owner.Name, Table: table, SQL: sql.String()})
	}
	slices.SortFunc(groups, func(a, b dump.Group) int { return strings.Compare(a.Table, b.Table) })
	return groups, nil
}
