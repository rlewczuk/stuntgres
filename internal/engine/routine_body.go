package engine

import (
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/sqltype"
)

// These are declaration identities, never permission results or a SQL evaluator.
// All edges come from REL_18_0 system_functions.sql/pg_proc.dat/pg_cast.dat.
// A descriptor borrows one engine-owned backing array; original binding/recipe
// manifests separately retain every actually selected edge's scoped lifetime.
type routineBodyDemand uint8

const (
	bodyAlways routineBodyDemand = iota
	bodyBooleanText
	bodyNetworkText
	bodyLeftDuration
	bodyRightDuration
)

type routineBodyEdge struct {
	routine *procedureDescriptor
	demand  routineBodyDemand
}

var builtinRoutineBodyEdges = [...]struct {
	outer, inner OID
	demand       routineBodyDemand
}{
	{879, 873, bodyAlways}, {880, 874, bodyAlways},
	{1811, 1374, bodyAlways}, {1811, 141, bodyAlways},
	{1810, 720, bodyAlways}, {1810, 141, bodyAlways}, {1812, 1681, bodyAlways},
	{1708, 1707, bodyAlways}, {1710, 1709, bodyAlways},
	{1384, 2024, bodyAlways}, {1384, 2021, bodyAlways},
	{2059, 2024, bodyAlways}, {2059, 2058, bodyAlways},
	{1296, 1272, bodyAlways}, {1298, 1297, bodyAlways},
	{1848, 1747, bodyAlways}, {2546, 2071, bodyAlways}, {2547, 1749, bodyAlways},
	{2548, 2032, bodyAlways}, {2549, 1189, bodyAlways}, {2550, 1141, bodyAlways},
	{2631, 2630, bodyAlways},
	{2074, 1986, bodyAlways}, {2074, 2073, bodyAlways},
	{1285, 2971, bodyBooleanText}, {1285, 730, bodyNetworkText}, {1285, 1283, bodyAlways},
	{1290, 2971, bodyBooleanText}, {1290, 730, bodyNetworkText}, {1290, 1289, bodyAlways},
	{2003, 2971, bodyBooleanText}, {2003, 730, bodyNetworkText}, {2003, 1258, bodyAlways},
	{2004, 2971, bodyBooleanText}, {2004, 730, bodyNetworkText}, {2004, 1258, bodyAlways},
	{3935, 2649, bodyAlways}, {3935, 1189, bodyAlways}, {3935, 6203, bodyAlways}, {3935, 1725, bodyAlways}, {3935, 1746, bodyAlways}, {3935, 2626, bodyAlways},
	{3936, 2649, bodyAlways}, {3936, 6203, bodyAlways}, {3936, 1725, bodyAlways}, {3936, 1746, bodyAlways}, {3936, 2626, bodyAlways},
	{1305, 1189, bodyLeftDuration}, {1305, 1189, bodyRightDuration}, {1305, 1304, bodyAlways},
	{1306, 1189, bodyRightDuration}, {1306, 1304, bodyAlways},
	{1307, 1189, bodyLeftDuration}, {1307, 1304, bodyAlways},
	{2042, 2032, bodyLeftDuration}, {2042, 2032, bodyRightDuration}, {2042, 2041, bodyAlways},
	{2043, 2032, bodyRightDuration}, {2043, 2041, bodyAlways},
	{2044, 2032, bodyLeftDuration}, {2044, 2041, bodyAlways},
	{1309, 1747, bodyLeftDuration}, {1309, 1747, bodyRightDuration}, {1309, 1308, bodyAlways},
	{1310, 1747, bodyRightDuration}, {1310, 1308, bodyAlways},
	{1311, 1747, bodyLeftDuration}, {1311, 1308, bodyAlways},
	{1215, 184, bodyAlways}, {1215, 62, bodyAlways}, {1215, 65, bodyAlways},
	{1993, 184, bodyAlways}, {1993, 62, bodyAlways},
	{1348, 184, bodyAlways}, {1348, 65, bodyAlways},
	{1216, 184, bodyAlways}, {1216, 65, bodyAlways},
	{1741, 1736, bodyAlways}, {1481, 1736, bodyAlways},
}

func (e *Engine) initializeBuiltinRoutineBodies() {
	storage := make([]routineBodyEdge, len(builtinRoutineBodyEdges))
	for i, edge := range builtinRoutineBodyEdges {
		outer, ok := e.state.descriptors[ObjectAddress{ClassID: ProcedureClassID, ObjectID: edge.outer}].(*procedureDescriptor)
		inner, found := e.state.descriptors[ObjectAddress{ClassID: ProcedureClassID, ObjectID: edge.inner}].(*procedureDescriptor)
		if !ok || !found {
			panic("stuntgres: missing genuine SQL body declaration")
		}
		storage[i] = routineBodyEdge{inner, edge.demand}
		if len(outer.body) == 0 {
			outer.body = storage[i : i+1 : i+1]
		} else {
			outer.body = storage[i-len(outer.body) : i+1 : i+1]
		}
	}
}

func polymorphicBodyArgument(node BoundExpr, id OID) BoundExpr {
	switch n := node.(type) {
	case functions.StringVariadic[BoundExpr, *EvalContext, ExprProperties, quoteOrigin]:
		if (id == 1285 || id == 1290) && len(n.Args) == 1 {
			return n.Args[0]
		}
	case functions.String[BoundExpr, *EvalContext, ExprProperties]:
		if id == 2003 && len(n.Args) == 2 {
			return n.Args[1]
		}
		if id == 2004 && len(n.Args) == 2 {
			return n.Args[0]
		}
	}
	return nil
}

func bodyCastMatches(edge routineBodyEdge, argument BoundExpr) bool {
	if argument == nil {
		return false
	}
	id := argument.ResultUsage().Type.BaseType().ID()
	return edge.demand == bodyBooleanText && id == sqltype.Bool || edge.demand == bodyNetworkText && (id == sqltype.Inet || id == sqltype.Cidr)
}

// Capture at binding, before descriptors can be remapped or published with a new
// ACL. Optional polymorphic casts are selected from the real argument, not all
// possible casts and not ordinary I/O helper identities.
func (b *expressionBinder) capturePolymorphicBody(node BoundExpr) error {
	outer, err := expressionRoutineDescriptor(node)
	if err != nil || outer == nil {
		return err
	}
	argument := polymorphicBodyArgument(node, outer.address.ObjectID)
	for _, edge := range outer.body {
		if bodyCastMatches(edge, argument) {
			if _, _, err := b.captureBuiltinRoutineDeclaration(edge.routine.builtin); err != nil {
				return err
			}
		}
	}
	return nil
}

func (b *expressionBinder) captureRoutineBody(outer *procedureDescriptor) error {
	for _, edge := range outer.body {
		if edge.demand == bodyBooleanText || edge.demand == bodyNetworkText {
			continue
		}
		if edge.routine == nil || edge.routine.builtin == nil {
			return fmt.Errorf("stuntgres: invalid SQL body declaration")
		}
		if _, _, err := b.captureBuiltinRoutineDeclaration(edge.routine.builtin); err != nil {
			return err
		}
	}
	return nil
}

// Lifetime capture is conservative even where strict NULL later removes actual
// invocation. Only instance-selected polymorphic cast identities participate.
func walkRoutineBody(outer *procedureDescriptor, node BoundExpr, visit func(*procedureDescriptor) error) error {
	argument := polymorphicBodyArgument(node, outer.address.ObjectID)
	for _, edge := range outer.body {
		if (edge.demand == bodyBooleanText || edge.demand == bodyNetworkText) && !bodyCastMatches(edge, argument) {
			continue
		}
		if err := visit(edge.routine); err != nil {
			return err
		}
		if err := walkRoutineBody(edge.routine, nil, visit); err != nil {
			return err
		}
	}
	return nil
}

// Only these already-implemented function-method casts belong to the reviewed
// SQL-body closure. Other casts/I/O helpers do not gain a fabricated invocation.
func embeddedCastOID(source, target *sqltype.Descriptor) OID {
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
