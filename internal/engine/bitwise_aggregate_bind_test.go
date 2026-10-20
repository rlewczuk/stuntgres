package engine

import (
	"context"
	"fmt"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func bitwiseAggregateUnitProvider(t *testing.T, d *sqltype.Descriptor, operation functions.AggregateOperation) functions.AggregateProvider {
	t.Helper()
	if d.ID() == sqltype.Bit {
		operator, err := bitwiseAggregateOperator(operation)
		if err != nil {
			t.Fatal(err)
		}
		return functions.NewBitStringAggregateProvider(d.Usage(), operator)
	}
	operator, err := bitwiseAggregateOperator(operation)
	if err != nil {
		t.Fatal(err)
	}
	bound, err := sqltype.BindBitwise(operator, d, d)
	if err != nil {
		t.Fatal(err)
	}
	return functions.NewIntegralBitwiseAggregateProvider(d.Usage(), operator, bound)
}

func TestBitwiseAggregateSlotsAndMetadata(t *testing.T) {
	ctx, e := context.Background(), New()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Bit, sqltype.VarBit} {
		d, _ := e.CatalogRead().LookupType(id)
		usage := d.Usage()
		if id == sqltype.Bit || id == sqltype.VarBit {
			usage.Typmod = 5
		}
		b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		b.clause.aggregateClause = "SELECT"
		b.params.parameters[1] = usage
		for _, name := range []string{"bit_and", "bit_or", "bit_xor", "bit_and"} {
			raw := parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}}, Pos: parser.Position{Character: 8}}
			expr, err := b.bind(raw, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			want := id
			if id == sqltype.VarBit {
				want = sqltype.Bit
			}
			if expr.ResultUsage().Type.ID() != want || expr.ResultUsage().Typmod != -1 || expr.ResultUsage().Collation != (sqltype.CollationRef{}) || !expr.Properties().StageDependent {
				t.Fatal("aggregate result usage", expr.ResultUsage())
			}
		}
		if len(b.grouping.aggregates) != 3 {
			t.Fatal("operation dedup identity", len(b.grouping.aggregates))
		}
		for i, op := range []functions.AggregateOperation{functions.AggregateBitAnd, functions.AggregateBitOr, functions.AggregateBitXor} {
			if b.grouping.aggregates[i].Operation != op || b.grouping.aggregates[i].IncludeNulls {
				t.Fatal("operation/strict identity", b.grouping.aggregates[i])
			}
		}
		binder := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
		binder.params.parameters[1] = usage
		targets := []parser.Expr{}
		for _, name := range []string{"bit_and", "bit_or", "bit_xor"} {
			targets = append(targets, parser.AggregateExpr{Name: name, Args: []parser.Expr{parser.ParameterExpr{Number: 1}}})
		}
		q, err := binder.bindSelect(&parser.Select{Targets: targets}, false)
		if err != nil {
			t.Fatal(err)
		}
		for _, col := range q.Root.Schema() {
			if !col.Nullable || !col.NullabilityKnown || col.OriginTable != 0 || col.OriginAttribute != 0 || col.Usage.Typmod != -1 || col.Usage.Collation != (sqltype.CollationRef{}) {
				t.Fatal("aggregate physical metadata", col)
			}
		}
	}
}

func TestBitwiseAggregateOrderingBoundary(t *testing.T) {
	e := New()
	for _, id := range []sqltype.ID{sqltype.Bit, sqltype.VarBit} {
		d, _ := e.CatalogRead().LookupType(id)
		_, err := functions.AggregateOrdering(d.Usage(), parser.Position{Character: 27}, false)
		diagnostic, ok := err.(*sqlstate.Error)
		if !ok || diagnostic.Code != "0A000" || diagnostic.Message != "aggregate ordering is unsupported for type "+d.DiagnosticName() || diagnostic.Position != "27" {
			t.Fatal("structured bit ordering boundary", err)
		}
	}
	d, _ := e.CatalogRead().LookupType(sqltype.Int4)
	if _, err := functions.AggregateOrdering(d.Usage(), parser.Position{Character: 27}, false); err != nil {
		t.Fatal("independent integer-key ordering changed", err)
	}
}

func TestBitwiseAggregateRemap(t *testing.T) {
	ctx := context.Background()
	source, destination := New(), New()
	for _, id := range []sqltype.ID{sqltype.Int2, sqltype.Int4, sqltype.Int8, sqltype.Bit} {
		oldType, _ := source.CatalogRead().LookupType(id)
		newType, _ := destination.CatalogRead().LookupType(id)
		for _, op := range []functions.AggregateOperation{functions.AggregateBitAnd, functions.AggregateBitOr, functions.AggregateBitXor} {
			provider := bitwiseAggregateUnitProvider(t, oldType, op)
			root := &plan.AggregateNode{QuerySchema: plan.QuerySchema{{Name: "fold", Usage: provider.ResultUsage(), Nullable: true, NullabilityKnown: true}}, Child: &plan.ValuesNode{}, Calls: []plan.AggregateCall{{Provider: provider, Operation: op}}}
			mapped, err := remapViewQuery(source.CatalogRead(), root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
				d, _ := destination.CatalogRead().LookupType(old.ID())
				return d
			})
			if err != nil {
				t.Fatal(err)
			}
			call := mapped.(*plan.AggregateNode).Calls[0]
			if call.Operation != op || call.Provider.ResultUsage().Type != newType || mapped.Schema()[0].Usage.Type != newType || provider.ResultUsage().Type != oldType {
				t.Fatal("incomplete provider remap or original mutated")
			}
			s, err := call.Provider.Init(ctx)
			if err != nil {
				t.Fatal(err)
			}
			var oldValue, newValue sqltype.Value
			if id == sqltype.Bit {
				oldValue, _ = sqltype.BitString(oldType, "10101")
				newValue, _ = sqltype.BitString(newType, "10101")
			} else {
				oldValue, _ = sqltype.Integer(oldType, 12)
				newValue, _ = sqltype.Integer(newType, 12)
			}
			if err = call.Provider.Advance(ctx, s, oldValue, sqltype.Value{}); err == nil {
				t.Fatal("remapped provider accepted old-generation datum")
			}
			if err = call.Provider.Advance(ctx, s, newValue, sqltype.Value{}); err != nil {
				t.Fatal(err)
			}
			if id == sqltype.Bit {
				newValue, _ = sqltype.BitString(newType, "11000")
			} else {
				newValue, _ = sqltype.Integer(newType, 10)
			}
			if err = call.Provider.Advance(ctx, s, newValue, sqltype.Value{}); err != nil {
				t.Fatal("integral bound signature not rebuilt", err)
			}
			v, err := call.Provider.Finalize(ctx, s)
			if err != nil || v.Type() != newType || v.IsNull() {
				t.Fatal(v, err)
			}
			if id == sqltype.Bit {
				want := "10000"
				if op == functions.AggregateBitOr {
					want = "11101"
				}
				if op == functions.AggregateBitXor {
					want = "01101"
				}
				if got, _ := v.Bits(); got != want {
					t.Fatal(got, want)
				}
			} else {
				want := int64(8)
				if op == functions.AggregateBitOr {
					want = 14
				}
				if op == functions.AggregateBitXor {
					want = 6
				}
				if got, _ := v.Integer(); got != want {
					t.Fatal(got, want)
				}
			}
		}
	}
}

func TestBitwiseAggregateValuesAnalysis(t *testing.T) {
	ctx, e := context.Background(), New()
	integer, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one := parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1, Pos: parser.Position{Character: 30}}}
	boolean := parser.LiteralExpr{Value: parser.Value{Kind: parser.BooleanValue, Boolean: true, Pos: parser.Position{Character: 20}}}
	for _, name := range []string{"bit_and", "bit_or", "bit_xor"} {
		for _, phase := range []string{"valid", "signature", "filter"} {
			b := newTestExpressionBinder(t, ctx, e, sqltype.InputEnvironment{}, nil)
			b.clause.aggregateClause = "INSERT VALUES"
			raw := parser.AggregateExpr{Name: name, Pos: parser.Position{Character: 10}, Args: []parser.Expr{one}}
			want := &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42803", Message: "aggregate functions are not allowed in VALUES", Position: "10"}
			if phase != "valid" {
				raw.Args = []parser.Expr{boolean}
				want = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42883", Message: "function " + name + "(boolean) does not exist", Position: "10", Hint: "No function matches the given name and argument types. You might need to add explicit type casts."}
			}
			if phase == "filter" {
				raw.Filter = one
				want = &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42804", Message: "argument of FILTER must be type boolean, not type integer", Position: "30"}
			}
			wrapped := parser.CastExpr{Child: raw, TypeName: parser.TypeName{Name: "integer"}}
			if !containsValuesAggregateAnalysis(wrapped) {
				t.Fatal("wrapped bitwise analysis omitted")
			}
			node, err := b.bind(wrapped, expressionPolicy{context: assignmentContext, expected: integer.Usage(), mode: sqltype.Assignment, column: "i"})
			diagnostic, ok := err.(*sqlstate.Error)
			if node != nil || !ok || *diagnostic != *want || len(b.grouping.aggregates) != 0 {
				t.Fatal("VALUES precedence/registration rollback", phase, node, err, b.grouping.aggregates)
			}
		}
	}
}

// bitwiseAggregateOperator maps a bit_* identity to its operator.
func bitwiseAggregateOperator(operation functions.AggregateOperation) (sqltype.BitwiseOperator, error) {
	switch operation {
	case functions.AggregateBitAnd:
		return sqltype.BitwiseAnd, nil
	case functions.AggregateBitOr:
		return sqltype.BitwiseOr, nil
	case functions.AggregateBitXor:
		return sqltype.BitwiseXor, nil
	}
	return 0, fmt.Errorf("invalid bitwise aggregate operation %d", operation)
}
