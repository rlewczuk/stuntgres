package analyze

import (
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/typedef"
	"stuntgres/internal/sqltype"
)

// DefaultDestination coerces a default recipe to its column's usage by
// assignment, or returns node when it already has that usage.
func DefaultDestination(node expr.Expr, target sqltype.Usage) expr.Expr {
	if node.ResultUsage() == target {
		return node
	}
	coercionPlan, err := sqltype.PlanCoercion(node.ResultUsage(), target, sqltype.Assignment)
	if err != nil {
		panic(err)
	} // Catalog validation guarantees assignment-compatible recipes.
	return plan.CoercionExpr{Info: expr.Info{Usage: target, Property: CoercionProperties(node, coercionPlan)}, Child: node, Plan: coercionPlan, Mode: sqltype.Assignment}
}

// ColumnDefault returns the default expression of the column at slot, from the
// column, then its type's default, then a domain NULL input, with its origin.
func ColumnDefault(view catalog.Read, d *catalog.Relation, slot int) (expr.Expr, DefaultOrigin) {
	attribute := d.Attributes[slot]
	usage := d.Columns[slot].Usage
	if definition, ok := d.ColumnMeta.Defaults[attribute]; ok {
		return definition.Expression, DefaultOrigin{Definition: definition, Consumer: d, Quote: exec.QuoteOrigin{Owner: view.Owner().Domain.Ref(view.Epoch(), d.Address()), Attribute: attribute, Definition: definition.OID}}
	}
	if definition := typedef.TypeDefault(view, usage.Type); definition != nil {
		owner := view.Owner().Domain.Ref(view.Epoch(), ids.ObjectAddress{ClassID: ids.TypeClassID, ObjectID: ids.OID(usage.Type.ID())})
		return DefaultDestination(definition.Expression, usage), DefaultOrigin{Definition: *definition, Quote: exec.QuoteOrigin{Owner: owner}}
	}
	if usage.Type.HasDomainConstraints() {
		return DomainNullInputExpression(usage), DefaultOrigin{}
	}
	return nil, DefaultOrigin{}
}

// DomainNullInputExpression is a NULL of target's base type coerced to target,
// so domain constraints run on an absent default.
func DomainNullInputExpression(target sqltype.Usage) expr.Expr {
	base := target.BaseUsage()
	coercionPlan, err := sqltype.PlanCoercion(base, target, sqltype.Assignment)
	if err != nil {
		panic(err)
	}
	child := plan.LiteralExpr{Info: expr.Info{Usage: base}, Value: sqltype.Null(base.Type)}
	return plan.CoercionExpr{Info: expr.Info{Usage: target, Property: CoercionProperties(child, coercionPlan)}, Child: child, Plan: coercionPlan, Mode: sqltype.Assignment}
}
