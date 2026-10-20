package engine

import (
	"context"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGDependSetArmCoercionReferences(t *testing.T) {
	_, view, integer := pgDependUnitCatalog(t)
	domains := make([]*sqltype.Descriptor, 0, 2)
	for i, name := range []string{"left_target", "right_target"} {
		domain, err := sqltype.Domain(sqltype.ID(49501+i), name, "public."+name, integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		view.State().SetDescriptor(pgDependUnitAddress(1247, ids.OID(49501+i), 0), &catalog.Type{SQLType: domain})
		domains = append(domains, domain)
	}
	literal := plan.LiteralExpr{Info: expr.Info{Usage: integer.Usage()}}
	// The ordinary project arms contain no unpinned references. Both targets
	// exist solely in the set's analyzed coercion expression arrays.
	left := &plan.ProjectNode{Expressions: []expr.Expr{literal}}
	right := &plan.ProjectNode{Expressions: []expr.Expr{literal}}
	node := &plan.SetNode{Operation: "union", All: true, Left: left, Right: right,
		LeftExpressions:  []expr.Expr{plan.CoercionExpr{Info: expr.Info{Usage: domains[0].Usage()}, Child: literal}},
		RightExpressions: []expr.Expr{plan.CoercionExpr{Info: expr.Info{Usage: domains[1].Usage()}, Child: literal}}}
	got, err := pgDependQueryRefs(context.Background(), view, nil, &plan.Query{Root: node})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{pgDependUnitAddress(1247, 49501, 0), pgDependUnitAddress(1247, 49502, 0)})
	node.RightExpressions = append(node.RightExpressions, node.LeftExpressions[0])
	got, err = pgDependQueryRefs(context.Background(), view, nil, &plan.Query{Root: node})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{pgDependUnitAddress(1247, 49501, 0), pgDependUnitAddress(1247, 49502, 0)})
	node.LeftExpressions = nil
	node.RightExpressions = nil
	// Result metadata is not itself a Const or coercion target.
	node.QuerySchema = plan.QuerySchema{{Usage: domains[0].Usage()}}
	got, err = pgDependQueryRefs(context.Background(), view, nil, &plan.Query{Root: node})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, nil)
}
