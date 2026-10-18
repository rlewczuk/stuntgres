package engine

import (
	"context"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPGDependSetArmCoercionReferences(t *testing.T) {
	_, catalog, integer := pgDependUnitCatalog(t)
	domains := make([]*sqltype.Descriptor, 0, 2)
	for i, name := range []string{"left_target", "right_target"} {
		domain, err := sqltype.Domain(sqltype.ID(49501+i), name, "public."+name, integer.Usage())
		if err != nil {
			t.Fatal(err)
		}
		catalog.state.setDescriptor(pgDependUnitAddress(1247, ids.OID(49501+i), 0), &typeDescriptor{typ: domain})
		domains = append(domains, domain)
	}
	literal := literalExpression{Info: expressionInfo{Usage: integer.Usage()}}
	// The ordinary project arms contain no unpinned references. Both targets
	// exist solely in the set's analyzed coercion expression arrays.
	left := &projectNode{expressions: []BoundExpr{literal}}
	right := &projectNode{expressions: []BoundExpr{literal}}
	node := &setNode{operation: "union", all: true, left: left, right: right,
		leftExpressions:  []BoundExpr{coercionExpression{Info: expressionInfo{Usage: domains[0].Usage()}, child: literal}},
		rightExpressions: []BoundExpr{coercionExpression{Info: expressionInfo{Usage: domains[1].Usage()}, child: literal}}}
	got, err := pgDependQueryRefs(context.Background(), catalog, &BoundQuery{Root: node})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{pgDependUnitAddress(1247, 49501, 0), pgDependUnitAddress(1247, 49502, 0)})
	node.rightExpressions = append(node.rightExpressions, node.leftExpressions[0])
	got, err = pgDependQueryRefs(context.Background(), catalog, &BoundQuery{Root: node})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, []ids.ObjectAddress{pgDependUnitAddress(1247, 49501, 0), pgDependUnitAddress(1247, 49502, 0)})
	node.leftExpressions = nil
	node.rightExpressions = nil
	// Result metadata is not itself a Const or coercion target.
	node.querySchema = querySchema{{Usage: domains[0].Usage()}}
	got, err = pgDependQueryRefs(context.Background(), catalog, &BoundQuery{Root: node})
	if err != nil {
		t.Fatal(err)
	}
	pgDependUnitRefs(t, got, nil)
}
