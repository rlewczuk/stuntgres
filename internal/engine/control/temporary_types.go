package control

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/sqltype"
)

// RetainTemporaryBuiltinTypes keeps, in the candidate next, the exact builtin
// type generations and array associations that retained temporary objects
// use: column types, the result types of their defaults, checks, policies,
// generations, index keys and predicates, view queries, literals and casts,
// and every column of a pinned catalog relation a temporary view reads.
// Imported columns and values are reowned later, never temporary rows. It
// reports whether it changed next.
// This candidate-only phase precedes CheckTemporaryDependencies so full policy
// runtime manifests can certify exact retained codec generations.
func RetainTemporaryBuiltinTypes(current catalog.Read, next *catalog.State, temporary map[ids.ObjectAddress]catalog.Descriptor) bool {
	typesChanged := false
	retained := make(map[sqltype.ID]bool)
	var retainBuiltin func(*sqltype.Descriptor)
	retainBuiltin = func(typ *sqltype.Descriptor) {
		if typ == nil || typ.IsDomain() || typ.IsArray() && typ.Element().IsDomain() || retained[typ.ID()] {
			return
		}
		retained[typ.ID()] = true
		address := ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(typ.ID())}
		source, ok := current.State().Type(address)
		if !ok || source.SQLType != typ {
			return
		}
		if imported, ok := next.Type(address); ok && imported.SQLType != typ {
			copy := *imported
			copy.SQLType = typ
			next.SetDescriptor(address, &copy)
			typesChanged = true
		}
		if typ.IsArray() {
			retainBuiltin(typ.Element())
		}
		if source.Array != 0 {
			if array, ok := current.State().Type(ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(source.Array)}); ok {
				retainBuiltin(array.SQLType)
			}
		}
	}
	for _, descriptor := range temporary {
		if relation, ok := descriptor.(*catalog.Relation); ok {
			for _, column := range relation.Columns {
				retainBuiltin(column.Usage.Type)
			}
			retainExpression := func(node expr.Expr) {
				plan.WalkExpression(node, func(node expr.Expr) { retainBuiltin(node.ResultUsage().Type) })
			}
			for _, definition := range relation.ColumnMeta.Defaults {
				retainExpression(definition.Expression)
			}
			for _, definition := range relation.Constraints.Check {
				retainExpression(definition.Expression)
			}
			for _, policy := range relation.Security.Policies {
				for _, condition := range []*stored.Policy{stored.PolicyOf(policy.Using), stored.PolicyOf(policy.Check)} {
					if condition != nil {
						analyze.WalkQueryArgumentExpression(condition.Expression(), func(node expr.Expr) { retainBuiltin(node.ResultUsage().Type) })
					}
				}
			}
			for _, definition := range relation.ColumnMeta.Generations {
				retainExpression(definition.Expression)
				retainExpression(definition.Planned)
			}
			if index := relation.SQLIndex; index != nil {
				for _, key := range index.Keys {
					if key.Derived != nil {
						retainExpression(key.Derived.Definition)
						retainExpression(key.Derived.Execution)
					}
				}
				if predicate := index.Predicate; predicate != nil {
					retainExpression(predicate.Definition)
					retainExpression(predicate.Execution)
				}
			}
			if definition := stored.ViewOf(relation); definition != nil {
				var retainQuery func(plan.Node)
				retainQuery = func(node plan.Node) {
					if node == nil {
						return
					}
					for _, column := range node.Schema() {
						retainBuiltin(column.Usage.Type)
					}
					plan.QueryExpressions(node, func(expression expr.Expr) {
						plan.WalkExpression(expression, func(expression expr.Expr) {
							retainBuiltin(expression.ResultUsage().Type)
							if child, ok := expression.(plan.SubqueryExpr); ok {
								retainQuery(child.QueryPlan.Root)
							}
						})
					})
					left, right := plan.QueryInputs(node)
					retainQuery(left)
					retainQuery(right)
				}
				retainQuery(definition.Query().Root)
				for _, literal := range definition.Literals() {
					retainBuiltin(literal.ResultUsage().Type)
				}
				for _, usage := range definition.CastUsages() {
					retainBuiltin(usage.Type)
				}
				for ref, captured := range definition.References() {
					address := ref.Address
					address.SubID = 0
					switch provider := current.State().Get(address).(type) {
					case *catalog.Type:
						retainBuiltin(provider.SQLType)
					case *catalog.Relation:
						if ref.Address.SubID != 0 || provider != captured || provider.RelationKind != catalog.CatalogRelation || !stored.DependPinned(current, nil, address) {
							continue
						}
						// The exact immutable catalog provider includes unused columns.
						// Retain their generations too, so imported scalar retagging
						// cannot clone the certified provider on a later replacement.
						for _, column := range provider.Columns {
							retainBuiltin(column.Usage.Type)
						}
					}
				}
			}
		}
	}
	return typesChanged
}
