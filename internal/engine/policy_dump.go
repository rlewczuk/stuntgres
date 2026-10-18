package engine

import (
	"context"
	"fmt"
	"slices"
	"strings"
	"stuntgres/internal/engine/authorization"
	"stuntgres/internal/engine/ids"
)

type dumpPolicyGroup struct{ owner, table, sql string }

func policyExpressionSQL(ctx context.Context, view CatalogRead, expression *policyExpression) (string, error) {
	r := newViewRenderer(expression.presentation)
	r.frame = 1
	source := viewFromRoot(r.frames[1])
	leaf := r.sources[0]
	d, ok := view.Relation(leaf.ref.Address)
	if !ok {
		return "", fmt.Errorf("stuntgres: policy owner is missing")
	}
	r.reserved[d.name] = true
	slots := make(map[int]string, len(leaf.attributes))
	for i, attribute := range leaf.attributes {
		for j, current := range d.Attributes {
			if current == attribute {
				slots[i] = quoteIdentifier(d.name) + "." + quoteIdentifier(d.Columns[j].Name)
				break
			}
		}
	}
	r.slots[source] = slots
	return r.expressionSQL(ctx, view, expression.syntax, viewLiteralPath{clause: viewTargetLiteral})
}

func policyDumpGroups(ctx context.Context, view CatalogRead, roles authorization.Read) ([]dumpPolicyGroup, error) {
	var groups []dumpPolicyGroup
	for _, descriptor := range view.Descriptors() {
		d, ok := descriptor.(*relationDescriptor)
		if !ok || len(d.Policies) == 0 {
			continue
		}
		ns := view.MustNamespace(d.namespace)
		if ns.temporary != (ids.SessionToken{}) {
			continue
		}
		owner, err := roles.Descriptor(d.Owner)
		if err != nil {
			return nil, err
		}
		table := quoteIdentifier(ns.name) + "." + quoteIdentifier(d.name)
		var sql strings.Builder
		for _, policy := range d.Policies {
			if err := ctx.Err(); err != nil {
				return nil, err
			}
			option := "PERMISSIVE"
			if !policy.permissive {
				option = "RESTRICTIVE"
			}
			sql.WriteString("CREATE POLICY " + quoteIdentifier(policy.Name) + " ON " + table + " AS " + option + " FOR " + strings.ToUpper(string(policy.command)) + " TO ")
			for i, role := range policy.roles {
				if i != 0 {
					sql.WriteString(", ")
				}
				if role.public {
					sql.WriteString("PUBLIC")
				} else {
					principal, err := roles.Descriptor(role.ref)
					if err != nil {
						return nil, err
					}
					sql.WriteString(quoteIdentifier(principal.Name))
				}
			}
			for _, clause := range []struct {
				name string
				expr *policyExpression
			}{{"USING", policyExpressionOf(policy.using)}, {"WITH CHECK", policyExpressionOf(policy.check)}} {
				if clause.expr == nil {
					continue
				}
				for _, leaf := range clause.expr.presentation.sources {
					relation, ok := view.Relation(leaf.ref.Address)
					if !ok {
						return nil, fmt.Errorf("stuntgres: policy provider is missing")
					}
					if view.MustNamespace(relation.namespace).temporary != (ids.SessionToken{}) {
						return nil, fmt.Errorf("stuntgres: dump cannot serialize a policy referencing a temporary relation")
					}
				}
				for _, dependency := range clause.expr.presentation.dependencies {
					address := dependency.ref.Address
					address.SubID = 0
					provider := view.Get(address)
					if provider == nil {
						return nil, fmt.Errorf("stuntgres: policy provider is missing")
					}
					if namespace, ok := view.Namespace(provider.Namespace()); ok && namespace.temporary != (ids.SessionToken{}) {
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
		groups = append(groups, dumpPolicyGroup{owner: owner.Name, table: table, sql: sql.String()})
	}
	slices.SortFunc(groups, func(a, b dumpPolicyGroup) int { return strings.Compare(a.table, b.table) })
	return groups, nil
}
