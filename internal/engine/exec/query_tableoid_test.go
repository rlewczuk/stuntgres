package exec

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryTableOIDIdentity(t *testing.T) {
	oid := asyncUnitType(t, sqltype.Oid)
	domain := new(ids.Domain)
	parent := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20000})
	child := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 20001})
	source := &Source{Ref: parent, Occurrence: 7, Inheritance: []InheritanceQuerySegment{{Leaf: Leaf{Ref: child}, Prefix: &Source{Anonymous: [][]sqltype.Value{{sqltype.Null(oid)}}}}}, InheritanceEnds: []int{1}}
	n := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: parent, Scope: 1, Frame: 4, QueryOccurrence: 1}
	outer := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 4, Query: &State{Source: source, IdentityReads: true}, QueryOccurrences: []int{7}, QueryProvenance: []int{0}}}
	inner := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 8, Outer: &outer}}
	value, err := n.Eval(&inner)
	if err != nil {
		t.Fatal(err)
	}
	if got, err := value.OID(); err != nil || got != uint32(child.Address.ObjectID) {
		t.Fatal("physical leaf instead of parent", got, err)
	}
	outer.QueryProvenance[0] = AbsentQueryIndex
	value, err = n.Eval(&inner)
	if err != nil || !value.IsNull() || value.Type() != oid {
		t.Fatal("outer absence", value, err)
	}
	outer.QueryProvenance[0] = 0
	for _, bad := range []func(*Context, *plan.TableOIDExpr){func(c *Context, n *plan.TableOIDExpr) { c.Query = nil }, func(c *Context, n *plan.TableOIDExpr) { c.QueryOccurrences = nil }, func(c *Context, n *plan.TableOIDExpr) { n.QueryOccurrence = 2 }, func(c *Context, n *plan.TableOIDExpr) { n.Frame = 99 }, func(c *Context, n *plan.TableOIDExpr) { c.QueryProvenance = []int{2} }} {
		copy, node := outer, n
		bad(&copy, &node)
		if _, err := node.Eval(&copy); err == nil {
			t.Fatal("invalid coordinates fell back to parent")
		}
	}
	generated := n
	generated.Scope, generated.Frame, generated.QueryOccurrence = 0, 0, 0
	frame := Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 1, PhysicalTable: child}}
	value, err = generated.Eval(&frame)
	if got, oidErr := value.OID(); err != nil || oidErr != nil || got != uint32(child.Address.ObjectID) {
		t.Fatal("generated physical override", got, err, oidErr)
	}
}

func TestQueryTableOIDIdentityDemand(t *testing.T) {
	typ := asyncUnitType(t, sqltype.Oid)
	column := plan.TableOIDExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 1, QueryOccurrence: 1}
	query := &plan.Query{Root: &plan.ProjectNode{Expressions: []expr.Expr{column}}}
	edge := plan.SubqueryExpr{Info: expr.Info{Usage: typ.Usage()}, QueryPlan: query}
	if !BoundHasIdentityReads(plan.CaseExpr{Info: expr.Info{Usage: typ.Usage()}, Otherwise: edge}) || !QueryHasIdentityReads(&plan.ProjectNode{Expressions: []expr.Expr{edge}}) {
		t.Fatal("nested correlated identity demand lost")
	}
	edge.CTEEdge = true
	if BoundHasIdentityReads(edge) || QueryHasIdentityReads(&plan.ProjectNode{AnalysisExpressions: []expr.Expr{edge}}) {
		t.Fatal("analysis-only CTE demanded runtime identity")
	}
	column.Scope, column.Frame, column.QueryOccurrence = 0, 0, 0
	if BoundHasIdentityReads(column) {
		t.Fatal("generated owner demanded query lineage")
	}
}

func TestQueryTableOIDFunctionCorrelation(t *testing.T) {
	var array *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.IsArray() && typ.Element().ID() == sqltype.Oid {
			array = typ
			break
		}
	}
	if array == nil {
		t.Fatal("missing OID array")
	}
	oid := array.Element()
	domain := new(ids.Domain)
	ancestorRef := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 23000})
	localRef := domain.Ref(1, ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 23001})
	ancestor := &Source{Ref: ancestorRef, Occurrence: 7, Anonymous: [][]sqltype.Value{{}}}
	local := &Source{Ref: localRef, Occurrence: 8, Anonymous: [][]sqltype.Value{{}}}
	bundle := &Source{Sources: []*Source{ancestor, local}}
	state := State{Source: bundle, Frame: 1, IdentityReads: true}
	state.Invocation = &Context{EvalStatement: EvalStatement{Context: context.Background()}, EvalFrame: EvalFrame{Scope: 1, Frame: 1, Query: &state, QueryOccurrences: []int{7}, QueryProvenance: []int{0}}}
	first := plan.TableOIDExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Owner: ancestorRef, Scope: 1, Frame: 1, QueryOccurrence: 1}
	second := first
	second.Owner, second.QueryOccurrence = localRef, 2
	call := plan.UnnestExpr{Info: expr.Info{Usage: oid.Usage(), Property: expr.Properties{RowDependent: true}}, Args: []expr.Expr{plan.ArrayExpr{Info: expr.Info{Usage: array.Usage(), Property: expr.Properties{RowDependent: true}}, Elements: []expr.Expr{first, second}}}}
	right := &plan.FunctionFromNode{QuerySchema: plan.QuerySchema{{Name: "physical", Usage: oid.Usage()}}, Frame: 1, Correlated: true, Calls: []expr.Expr{call}, Captures: []plan.FunctionCorrelation{{Frame: 1, QueryOccurrence: 1}, {Frame: 1, QueryOccurrence: 2}}}
	join := &plan.JoinNode{QuerySchema: right.QuerySchema, Left: &plan.SourceNode{Ref: localRef, Occurrence: 1}, Right: right, RightWidth: 1, Frame: 1, CorrelatedRight: true, CorrelationIdentityPrefix: true}
	stage, err := OpenJoinNode(context.Background(), &state, join)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	row := make([]sqltype.Value, 1)
	for _, want := range []uint32{uint32(ancestorRef.Address.ObjectID), uint32(localRef.Address.ObjectID)} {
		present, err := stage.Next(context.Background(), row)
		if err != nil || !present {
			t.Fatal("zero-width inherited identity was lost", present, err)
		}
		if got, err := row[0].OID(); err != nil || got != want {
			t.Fatal("wrong function capture", got, want, err)
		}
	}
	if present, err := stage.Next(context.Background(), row); present || err != nil {
		t.Fatal("function multiplicity", present, err)
	}
}
