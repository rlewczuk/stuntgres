package engine

import (
	"context"
	"testing"

	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineProviderCallShapes(t *testing.T) {
	e := New()
	auth := catalogAuthorization(e, e.CatalogRead())
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.query.auth = &auth
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, tc := range []struct {
		name      string
		inputs    []sqltype.ID
		signature string
	}{
		{"array_append", []sqltype.ID{1007, sqltype.Int4}, "5078,5077"},
		{"array_prepend", []sqltype.ID{sqltype.Int4, 1007}, "5077,5078"},
		{"array_cat", []sqltype.ID{1007, 1007}, "5078,5078"},
		{"array_fill", []sqltype.ID{sqltype.Int4, 1007}, "2283,1007"},
		{"array_sort", []sqltype.ID{1007}, "2277"},
		{"array_length", []sqltype.ID{1007, sqltype.Int4}, "2277,23"},
		{"array_shuffle", []sqltype.ID{1007}, "2277"},
		{"array_position", []sqltype.ID{1007, sqltype.Int4}, "5078,5077"},
		{"width_bucket", []sqltype.ID{sqltype.Int4, 1007}, "5077,5078"},
		{"lower", []sqltype.ID{sqltype.Int4Range}, "3831"},
		{"isempty", []sqltype.ID{sqltype.Int4Multirange}, "4537"},
		{"to_json", []sqltype.ID{sqltype.Int4}, "2283"},
		{"array_to_json", []sqltype.ID{1007}, "2277"},
		{"json_build_array", []sqltype.ID{sqltype.Int4, sqltype.Text}, "2276"},
		{"quote_literal", []sqltype.ID{sqltype.Int4}, "2283"},
		{"concat", []sqltype.ID{sqltype.Int4, sqltype.Text}, "2276"},
		{"parse_ident", []sqltype.ID{sqltype.Text}, "25,16"},
		{"array_agg", []sqltype.ID{sqltype.Int4}, "2776"},
		{"array_agg", []sqltype.ID{1007}, "2277"},
		{"any_value", []sqltype.ID{sqltype.Int4}, "2283"},
		{"json_agg", []sqltype.ID{sqltype.Int4}, "2283"},
		{"range_agg", []sqltype.ID{sqltype.Int4Range}, "3831"},
		{"count", []sqltype.ID{sqltype.Text}, "2276"},
		{"json_object_agg", []sqltype.ID{sqltype.Text, sqltype.Int4}, "2276,2276"},
		{"unnest", []sqltype.ID{1007}, "2277"},
		{"unnest", []sqltype.ID{sqltype.Int4Multirange}, "4537"},
		{"generate_subscripts", []sqltype.ID{1007, sqltype.Int4}, "2277,23"},
		{"num_nulls", []sqltype.ID{sqltype.Int4, sqltype.Text}, "2276"},
		{"row_number", nil, ""},
		{"ntile", []sqltype.ID{sqltype.Int4}, "23"},
		{"first_value", []sqltype.ID{sqltype.Int4}, "2283"},
		{"last_value", []sqltype.ID{sqltype.Text}, "2283"},
		{"nth_value", []sqltype.ID{sqltype.Int4, sqltype.Int4}, "2283,23"},
		{"lag", []sqltype.ID{sqltype.Int4}, "2283"},
		{"lag", []sqltype.ID{sqltype.Int4, sqltype.Int4}, "2283,23"},
		{"lag", []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8}, "5077,23,5077"},
		{"lead", []sqltype.ID{sqltype.Int4}, "2283"},
		{"lead", []sqltype.ID{sqltype.Int4, sqltype.Int4}, "2283,23"},
		{"lead", []sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8}, "5077,23,5077"},
		{"rank", nil, ""},
		{"dense_rank", nil, ""},
		{"percent_rank", nil, ""},
		{"cume_dist", nil, ""},
	} {
		t.Run(tc.name+"/"+tc.signature, func(t *testing.T) {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: make([]parser.Expr, len(tc.inputs))}
			operands := make([]functions.Argument, len(tc.inputs))
			for i, id := range tc.inputs {
				operands[i].Usage = b.Type(id).Usage()
			}
			found := false
			for d := range auth.definitionProceduresNamed(namespace, tc.name) {
				if string(d.Signature) != tc.signature {
					continue
				}
				found = true
				candidate, ok := sqlCallableShape(auth, d, raw)
				if !ok {
					t.Fatal("metadata expansion rejected existing callable shape")
				}
				candidate.targets = append([]sqltype.Usage(nil), candidate.inputs...)
				pseudo := false
				for _, input := range candidate.inputs {
					pseudo = pseudo || !input.Type.Concrete()
				}
				if pseudo && !b.probeSQLCallable(raw, operands, &candidate) {
					t.Fatal("family rejected existing callable shape")
				}
				for i, input := range candidate.inputs {
					if input.Type.Concrete() {
						if _, err := sqltype.PlanCoercion(operands[i].Usage, input, sqltype.Implicit); err != nil {
							t.Fatal(err)
						}
					}
				}
				if string(d.Signature) != tc.signature {
					t.Fatal("applicability changed declaration identity")
				}
			}
			if !found {
				t.Fatal("missing checked builtin declaration")
			}
		})
	}
}

func TestSQLRoutineCallableShapeMasking(t *testing.T) {
	var integer, wide *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Int4 {
			integer = typ
		}
		if typ.ID() == sqltype.Int8 {
			wide = typ
		}
	}
	input := []sqltype.Usage{integer.Usage()}
	for _, reverse := range []bool{false, true} {
		fixed := sqlCallable{inputs: input, path: 0}
		variadic := sqlCallable{inputs: input, path: 0, variadic: true}
		first, second := fixed, variadic
		if reverse {
			first, second = second, first
		}
		got := appendSQLCallable([]sqlCallable{first}, second)
		if len(got) != 1 || got[0].variadic {
			t.Fatal("nonvariadic declaration did not win its namespace", reverse)
		}
	}
	early := sqlCallable{inputs: input, path: 0, variadic: true}
	later := sqlCallable{inputs: input, path: 1}
	if got := appendSQLCallable([]sqlCallable{early}, later); len(got) != 1 || got[0].path != 0 {
		t.Fatal("later namespace replaced identical expanded inputs")
	}
	later.inputs = []sqltype.Usage{wide.Usage()}
	if got := appendSQLCallable([]sqlCallable{early}, later); len(got) != 2 {
		t.Fatal("different input signature was masked")
	}
	// Equally defaultable declarations in one namespace do not get an
	// iteration-order winner merely because omitted tails are different.
	left := sqlCallable{inputs: input, path: 0}
	if got := appendSQLCallable([]sqlCallable{left}, left); len(got) != 2 {
		t.Fatal("ambiguous supplied inputs got an arbitrary winner")
	}
}

func TestSQLRoutineOriginalArgumentRecipes(t *testing.T) {
	e := New()
	auth := catalogAuthorization(e, e.CatalogRead())
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.query.auth = &auth
	raw := statement(t, "SELECT 1+2").(*parser.Select).Targets[0]
	node := &literalExpression{Info: expr.Info{Usage: b.Type(sqltype.Int4).Usage()}}
	b.clause.selectedOperatorArgs = []expr.Expr{node}
	b.clause.selectedOperatorSyntax = []parser.Expr{raw}
	b.clause.selectedCallArguments = true
	carrier := parser.ParameterExpr{Number: -1, Pos: raw.SourcePosition()}
	argument, err := b.AnalyzeArgument(carrier, functions.AnalysisRequest{Intent: functions.ComparisonArgument})
	if err != nil || argument.Expression != node {
		t.Fatal("selected operand was rebound", err)
	}
	original, selected := functions.OriginalArgument(b, carrier)
	if !selected || original.SourcePosition() != raw.SourcePosition() {
		t.Fatal("lost original argument recipe")
	}
	if _, ok := argument.Raw.(parser.OperatorExpr); !ok {
		t.Fatal("analyzed argument exposed private carrier syntax")
	}
	coerced, err := b.CoerceArgument(argument, functions.CoercionRequest{Intent: functions.AssignmentFormalArgument, Target: node.ResultUsage()})
	if err != nil || coerced != node {
		t.Fatal("selected assignment-formal argument was rebound", err)
	}
	_, issue, err := functions.AnalyzeNullCountArgument(b, carrier)
	if err != nil || !issue.UnsupportedArithmetic {
		t.Fatal("private carrier concealed native syntax boundary", issue, err)
	}
	if len(b.params.parameters) != 0 {
		t.Fatal("private carrier entered public parameter namespace")
	}
}

func TestSQLRoutineWindowCallShapeTargets(t *testing.T) {
	e := New()
	auth := catalogAuthorization(e, e.CatalogRead())
	b := newExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.query.auth = &auth
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lag"}}},
		Args: make([]parser.Expr, 3),
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	var template sqlCallable
	for d := range auth.definitionProceduresNamed(namespace, "lag") {
		if d.Signature == "5077,23,5077" {
			var ok bool
			template, ok = sqlCallableShape(auth, d, raw)
			if !ok {
				t.Fatal("missing declared window call shape")
			}
		}
	}
	if template.procedure == nil {
		t.Fatal("missing checked lag declaration")
	}
	for _, tc := range []struct {
		name   string
		inputs [3]sqltype.ID
		target sqltype.ID
		viable bool
	}{
		{"widen default", [3]sqltype.ID{sqltype.Int4, sqltype.Int4, sqltype.Int8}, sqltype.Int8, true},
		{"unknown value", [3]sqltype.ID{0, sqltype.Int4, sqltype.Int8}, sqltype.Int8, true},
		{"unknown value and default", [3]sqltype.ID{0, sqltype.Int4, 0}, sqltype.Text, true},
		{"wrong offset", [3]sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Int4}, 0, false},
		{"conflicting default", [3]sqltype.ID{sqltype.Bool, sqltype.Int4, sqltype.Int4}, 0, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var operands [3]functions.Argument
			for i, id := range tc.inputs {
				if id != 0 {
					operands[i].Usage = b.Type(id).Usage()
				}
			}
			candidate := template
			candidate.targets = append([]sqltype.Usage(nil), template.inputs...)
			if got := b.probeSQLCallable(raw, operands[:], &candidate); got != tc.viable {
				t.Fatalf("viability=%v, want %v", got, tc.viable)
			}
			if tc.viable && (candidate.targets[0].Type.ID() != tc.target || candidate.targets[2].Type.ID() != tc.target) {
				t.Fatal("wrong shared value/default targets", candidate.targets)
			}
			if candidate.inputs[0].Type.ID() != 5077 || candidate.inputs[2].Type.ID() != 5077 {
				t.Fatal("resolved targets replaced declared ranking identities")
			}
			if len(b.params.parameters) != 0 {
				t.Fatal("applicability inferred public parameters")
			}
		})
	}
}
