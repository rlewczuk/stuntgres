package partition_test

import (
	"context"
	"stuntgres/internal/engine"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/partition"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/routine"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestPartitionDetachConcurrentProof(t *testing.T) {
	e := engine.New()
	view := e.CatalogRead()
	ctx := context.Background()
	varchar, _ := view.LookupType(sqltype.VarChar)
	text, _ := view.LookupType(sqltype.Text)
	boolean, _ := view.LookupType(sqltype.Bool)
	method, _ := view.AccessMethod(403)
	fact, ok := view.LookupOperatorClass(ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}, 403, "text_pattern_ops")
	if !ok {
		t.Fatal("missing real pattern opclass")
	}
	class, ok := view.IndexClassForMethod(fact.Address().ObjectID, method)
	if !ok {
		t.Fatal("missing captured class")
	}
	usage := varchar.Usage()
	ordering, err := routine.BindIndexOrdering(view, usage, class)
	if err != nil {
		t.Fatal(err)
	}
	key := catalog.PartitionKeyEntry{Attribute: 1, Usage: usage, Class: class, Comparison: ordering, Label: "k"}
	parent := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 190001}, Columns: []catalog.Column{{Name: "k", Usage: usage}}, Attributes: []int32{1}, Index: map[string]int{"k": 0}, Partition: &catalog.PartitionDefinition{Strategy: parser.PartitionRange, Keys: []catalog.PartitionKeyEntry{key}}}
	child := &catalog.Relation{ObjectAddress: ids.ObjectAddress{ClassID: ids.RelationClassID, ObjectID: 190002}, Columns: parent.Columns, Attributes: parent.Attributes, Index: parent.Index}
	child.Constraints.NotNull = []catalog.NotNullConstraint{{Attribute: 1}}
	column := plan.ColumnExpr{Info: expr.Info{Usage: usage, Property: expr.Properties{RowDependent: true}}, Origin: e.Catalog.Domain.Ref(view.Epoch(), child.Address()), Scope: 1, Frame: 1, Slot: 0}
	column.Origin.Address.SubID = 1
	target := usage
	target.Type = text
	target.Typmod = -1
	adapter, err := plan.NewDatumCast(expr.Info{Usage: target, Property: column.Properties()}, column, "b", sqltype.Implicit)
	if err != nil {
		t.Fatal(err)
	}
	adapter.Context = "i"
	value := func(typ *sqltype.Descriptor, s string) sqltype.Value {
		v, err := typ.Input(ctx, s, -1)
		if err != nil {
			t.Fatal(err)
		}
		return v
	}
	bound := &catalog.PartitionMembership{Parent: parent.Address(), Strategy: parser.PartitionRange, Lower: []catalog.PartitionDatum{{Value: value(varchar, "b")}}, Upper: []catalog.PartitionDatum{{Value: value(varchar, "d")}}}
	comparison := func(operand expr.Expr, strategy int16, literal string) expr.Expr {
		op, err := partition.BoundOperator(view, key, strategy)
		if err != nil {
			t.Fatal(err)
		}
		procedure, ok := view.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: op.Function})
		if !ok {
			t.Fatal("missing actual pattern procedure")
		}
		name, operation := "text_pattern_ge", sqltype.CompareGreaterEqual
		if strategy == 1 {
			name, operation = "text_pattern_lt", sqltype.CompareLess
		}
		predicate, err := sqltype.BindComparisonPredicate(operation, text, text, boolean, sqltype.CollationRef{ID: 950, Derivation: sqltype.Default}, false)
		if err != nil {
			t.Fatal(err)
		}
		info := expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}, RoutineReference: procedure}
		return functions.TextPatternComparison{Info: info, Args: [2]expr.Expr{operand, plan.LiteralExpr{Info: expr.Info{Usage: target}, Value: value(text, literal)}}, Name: name, Predicate: predicate}
	}
	for _, change := range []string{"real_binary", "io", "sizing", "wrong_binary", "unknown_input", "numeric", "domain_entry"} {
		t.Run(change, func(t *testing.T) {
			var operand expr.Expr = adapter
			switch change {
			case "io":
				next := adapter
				next.Method = "i"
				operand = next
			case "sizing":
				next := adapter
				next.Size = true
				operand = next
			case "wrong_binary":
				next := adapter
				next.Binary, err = sqltype.PlanBinaryCoercion(varchar, varchar)
				if err != nil {
					t.Fatal(err)
				}
				operand = next
			case "unknown_input":
				p, err := sqltype.PlanDeclaredBinaryCoercion(usage, target)
				if err != nil {
					t.Fatal(err)
				}
				operand = plan.CoercionExpr{Info: expr.Info{Usage: target}, Child: column, Plan: p, UnknownInputCast: &plan.UnknownInputCast{}}
			case "numeric":
				integer, _ := view.LookupType(sqltype.Int4)
				wide, _ := view.LookupType(sqltype.Int8)
				other := column
				other.Usage = integer.Usage()
				p, err := sqltype.PlanCoercion(other.Usage, wide.Usage(), sqltype.Implicit)
				if err != nil {
					t.Fatal(err)
				}
				operand = plan.CoercionExpr{Info: expr.Info{Usage: wide.Usage()}, Child: other, Plan: p, Mode: sqltype.Implicit}
			case "domain_entry":
				domain, err := sqltype.Domain(190003, "proof_domain", "proof_domain", usage, true)
				if err != nil {
					t.Fatal(err)
				}
				p, err := sqltype.PlanCoercion(usage, domain.Usage(), sqltype.Explicit)
				if err != nil {
					t.Fatal(err)
				}
				operand = plan.CoercionExpr{Info: expr.Info{Usage: domain.Usage()}, Child: column, Plan: p, Mode: sqltype.Explicit}
			}
			check := plan.LogicalExpr{Info: expr.Info{Usage: boolean.Usage(), Property: expr.Properties{RowDependent: true}}, Op: parser.LogicalAnd, Left: comparison(operand, 4, "c"), Right: comparison(operand, 1, "d")}
			next := *child
			next.Constraints.Check = []catalog.CheckConstraint{{Name: "stronger", Expression: check}}
			proven, err := partition.ConstraintProven(ctx, sqltype.InputEnvironment{}, view, parent, &next, bound, false)
			if err != nil || proven != (change == "real_binary") {
				t.Fatal("binary/pattern implication authority", change, proven, err)
			}
		})
	}
}
