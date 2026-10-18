package engine

import (
	"fmt"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

var builtinRoutineBodyEdges = [...]struct {
	outer, inner ids.OID
	demand       catalog.RoutineBodyDemand
}{
	{879, 873, catalog.BodyAlways}, {880, 874, catalog.BodyAlways},
	{1811, 1374, catalog.BodyAlways}, {1811, 141, catalog.BodyAlways},
	{1810, 720, catalog.BodyAlways}, {1810, 141, catalog.BodyAlways}, {1812, 1681, catalog.BodyAlways},
	{1708, 1707, catalog.BodyAlways}, {1710, 1709, catalog.BodyAlways},
	{1384, 2024, catalog.BodyAlways}, {1384, 2021, catalog.BodyAlways},
	{2059, 2024, catalog.BodyAlways}, {2059, 2058, catalog.BodyAlways},
	{1296, 1272, catalog.BodyAlways}, {1298, 1297, catalog.BodyAlways},
	{1848, 1747, catalog.BodyAlways}, {2546, 2071, catalog.BodyAlways}, {2547, 1749, catalog.BodyAlways},
	{2548, 2032, catalog.BodyAlways}, {2549, 1189, catalog.BodyAlways}, {2550, 1141, catalog.BodyAlways},
	{2631, 2630, catalog.BodyAlways},
	{2074, 1986, catalog.BodyAlways}, {2074, 2073, catalog.BodyAlways},
	{1285, 2971, catalog.BodyBooleanText}, {1285, 730, catalog.BodyNetworkText}, {1285, 1283, catalog.BodyAlways},
	{1290, 2971, catalog.BodyBooleanText}, {1290, 730, catalog.BodyNetworkText}, {1290, 1289, catalog.BodyAlways},
	{2003, 2971, catalog.BodyBooleanText}, {2003, 730, catalog.BodyNetworkText}, {2003, 1258, catalog.BodyAlways},
	{2004, 2971, catalog.BodyBooleanText}, {2004, 730, catalog.BodyNetworkText}, {2004, 1258, catalog.BodyAlways},
	{3935, 2649, catalog.BodyAlways}, {3935, 1189, catalog.BodyAlways}, {3935, 6203, catalog.BodyAlways}, {3935, 1725, catalog.BodyAlways}, {3935, 1746, catalog.BodyAlways}, {3935, 2626, catalog.BodyAlways},
	{3936, 2649, catalog.BodyAlways}, {3936, 6203, catalog.BodyAlways}, {3936, 1725, catalog.BodyAlways}, {3936, 1746, catalog.BodyAlways}, {3936, 2626, catalog.BodyAlways},
	{1305, 1189, catalog.BodyLeftDuration}, {1305, 1189, catalog.BodyRightDuration}, {1305, 1304, catalog.BodyAlways},
	{1306, 1189, catalog.BodyRightDuration}, {1306, 1304, catalog.BodyAlways},
	{1307, 1189, catalog.BodyLeftDuration}, {1307, 1304, catalog.BodyAlways},
	{2042, 2032, catalog.BodyLeftDuration}, {2042, 2032, catalog.BodyRightDuration}, {2042, 2041, catalog.BodyAlways},
	{2043, 2032, catalog.BodyRightDuration}, {2043, 2041, catalog.BodyAlways},
	{2044, 2032, catalog.BodyLeftDuration}, {2044, 2041, catalog.BodyAlways},
	{1309, 1747, catalog.BodyLeftDuration}, {1309, 1747, catalog.BodyRightDuration}, {1309, 1308, catalog.BodyAlways},
	{1310, 1747, catalog.BodyRightDuration}, {1310, 1308, catalog.BodyAlways},
	{1311, 1747, catalog.BodyLeftDuration}, {1311, 1308, catalog.BodyAlways},
	{1215, 184, catalog.BodyAlways}, {1215, 62, catalog.BodyAlways}, {1215, 65, catalog.BodyAlways},
	{1993, 184, catalog.BodyAlways}, {1993, 62, catalog.BodyAlways},
	{1348, 184, catalog.BodyAlways}, {1348, 65, catalog.BodyAlways},
	{1216, 184, catalog.BodyAlways}, {1216, 65, catalog.BodyAlways},
	{1741, 1736, catalog.BodyAlways}, {1481, 1736, catalog.BodyAlways},
}

func (e *Engine) initializeBuiltinRoutineBodies() {
	storage := make([]catalog.RoutineBodyEdge, len(builtinRoutineBodyEdges))
	for i, edge := range builtinRoutineBodyEdges {
		outer, ok := e.catalog.state.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: edge.outer})
		inner, found := e.catalog.state.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: edge.inner})
		if !ok || !found {
			panic("stuntgres: missing genuine SQL body declaration")
		}
		storage[i] = catalog.RoutineBodyEdge{inner, edge.demand}
		if len(outer.Body) == 0 {
			outer.Body = storage[i : i+1 : i+1]
		} else {
			outer.Body = storage[i-len(outer.Body) : i+1 : i+1]
		}
	}
}

func polymorphicBodyArgument(node expr.Expr, id ids.OID) expr.Expr {
	switch n := node.(type) {
	case functions.StringVariadic:
		if (id == 1285 || id == 1290) && len(n.Args) == 1 {
			return n.Args[0]
		}
	case functions.String:
		if id == 2003 && len(n.Args) == 2 {
			return n.Args[1]
		}
		if id == 2004 && len(n.Args) == 2 {
			return n.Args[0]
		}
	}
	return nil
}

func bodyCastMatches(edge catalog.RoutineBodyEdge, argument expr.Expr) bool {
	if argument == nil {
		return false
	}
	id := argument.ResultUsage().Type.BaseType().ID()
	return edge.Demand == catalog.BodyBooleanText && id == sqltype.Bool || edge.Demand == catalog.BodyNetworkText && (id == sqltype.Inet || id == sqltype.Cidr)
}

// Capture at binding, before descriptors can be remapped or published with a new
// ACL. Optional polymorphic casts are selected from the real argument, not all
// possible casts and not ordinary I/O helper identities.
func (b *expressionBinder) capturePolymorphicBody(node expr.Expr) error {
	outer, err := expressionRoutineDescriptor(node)
	if err != nil || outer == nil {
		return err
	}
	argument := polymorphicBodyArgument(node, outer.Address().ObjectID)
	for _, edge := range outer.Body {
		if bodyCastMatches(edge, argument) {
			if _, _, err := b.captureBuiltinRoutineDeclaration(edge.Routine.Builtin); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *expressionBinder) captureRoutineBody(outer *catalog.Procedure) error {
	for _, edge := range outer.Body {
		if edge.Demand == catalog.BodyBooleanText || edge.Demand == catalog.BodyNetworkText {
			continue
		}
		if edge.Routine == nil || edge.Routine.Builtin == nil {
			return fmt.Errorf("stuntgres: invalid SQL body declaration")
		}
		if _, _, err := b.captureBuiltinRoutineDeclaration(edge.Routine.Builtin); err != nil {
			return err
		}
	}
	return nil
}

// Lifetime capture is conservative even where strict NULL later removes actual
// invocation. Only instance-selected polymorphic cast identities participate.
func walkRoutineBody(outer *catalog.Procedure, node expr.Expr, visit func(*catalog.Procedure) error) error {
	argument := polymorphicBodyArgument(node, outer.Address().ObjectID)
	for _, edge := range outer.Body {
		if (edge.Demand == catalog.BodyBooleanText || edge.Demand == catalog.BodyNetworkText) && !bodyCastMatches(edge, argument) {
			continue
		}
		if err := visit(edge.Routine); err != nil {
			return err
		}
		if err := walkRoutineBody(edge.Routine, nil, visit); err != nil {
			return err
		}
	}
	return nil
}

// Only these already-implemented function-method casts belong to the reviewed
// SQL-body closure. Other casts/I/O helpers do not gain a fabricated invocation.
func embeddedCastOID(source, target *sqltype.Descriptor) ids.OID {
	if source == nil || target == nil {
		return 0
	}
	left, right := source.BaseType().ID(), target.BaseType().ID()
	switch {
	case left == sqltype.Date && right == sqltype.Timestamp:
		return 2024
	case left == sqltype.Numeric && right == sqltype.Float8:
		return 1746
	case left == sqltype.Bool && right == sqltype.Text:
		return 2971
	case (left == sqltype.Inet || left == sqltype.Cidr) && right == sqltype.Text:
		return 730
	}
	return 0
}
