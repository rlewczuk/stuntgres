package core_test

import (
	"context"
	"iter"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestSQLRoutineProviderCallShapes(t *testing.T) {
	e := engine.New()
	auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.Query.Auth = &auth
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
			for d := range definitionProceduresNamed(auth, namespace, tc.name) {
				if string(d.Signature) != tc.signature {
					continue
				}
				found = true
				candidate, ok := analyze.SQLCallableShape(auth.Catalog, auth.Definition, d, raw)
				if !ok {
					t.Fatal("metadata expansion rejected existing callable shape")
				}
				candidate.Targets = append([]sqltype.Usage(nil), candidate.Inputs...)
				pseudo := false
				for _, input := range candidate.Inputs {
					pseudo = pseudo || !input.Type.Concrete()
				}
				if pseudo && !b.ProbeSQLCallable(raw, operands, &candidate) {
					t.Fatal("family rejected existing callable shape")
				}
				for i, input := range candidate.Inputs {
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
	builtin, ok := auth.Catalog.Procedure(ids.ObjectAddress{ClassID: ids.ProcedureClassID, ObjectID: 3955})
	if !ok {
		t.Fatal("missing declared json_array_elements output owner")
	}
	full, valid := analyze.SQLCallableShape(auth.Catalog, auth.Definition, builtin, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 2)})
	if !valid || len(full.Inputs) != 2 || full.Inputs[0].Type.ID() != sqltype.JSON || full.Inputs[1].Type.ID() != sqltype.JSON || full.Procedure != builtin {
		t.Fatal("builtin CALL omitted full OUT projection", full, valid)
	}
	if _, valid := analyze.SQLCallableShape(auth.Catalog, auth.Definition, builtin, parser.FunctionExpr{ProcedureCall: true, Args: make([]parser.Expr, 1)}); valid {
		t.Fatal("builtin CALL omitted required OUT")
	}
	if _, valid := analyze.SQLCallableShape(auth.Catalog, auth.Definition, builtin, parser.FunctionExpr{Args: make([]parser.Expr, 1)}); !valid {
		t.Fatal("builtin SELECT acquired OUT actual")
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
		fixed := analyze.SQLCallable{Inputs: input, Path: 0}
		variadic := analyze.SQLCallable{Inputs: input, Path: 0, Variadic: true}
		first, second := fixed, variadic
		if reverse {
			first, second = second, first
		}
		got := analyze.AppendSQLCallable([]analyze.SQLCallable{first}, second)
		if len(got) != 1 || got[0].Variadic {
			t.Fatal("nonvariadic declaration did not win its namespace", reverse)
		}
	}
	early := analyze.SQLCallable{Inputs: input, Path: 0, Variadic: true}
	later := analyze.SQLCallable{Inputs: input, Path: 1}
	if got := analyze.AppendSQLCallable([]analyze.SQLCallable{early}, later); len(got) != 1 || got[0].Path != 0 {
		t.Fatal("later namespace replaced identical expanded inputs")
	}
	later.Inputs = []sqltype.Usage{wide.Usage()}
	if got := analyze.AppendSQLCallable([]analyze.SQLCallable{early}, later); len(got) != 2 {
		t.Fatal("different input signature was masked")
	}
	// Equally defaultable declarations in one namespace do not get an
	// iteration-order winner merely because omitted tails are different.
	left := analyze.SQLCallable{Inputs: input, Path: 0}
	if got := analyze.AppendSQLCallable([]analyze.SQLCallable{left}, left); len(got) != 2 {
		t.Fatal("ambiguous supplied inputs got an arbitrary winner")
	}
}

func TestSQLRoutineOriginalArgumentRecipes(t *testing.T) {
	e := engine.New()
	auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.Query.Auth = &auth
	raw := enginetest.Statement(t, "SELECT 1+2").(*parser.Select).Targets[0]
	node := &plan.LiteralExpr{Info: expr.Info{Usage: b.Type(sqltype.Int4).Usage()}}
	b.Clause.SelectedOperatorArgs = []expr.Expr{node}
	b.Clause.SelectedOperatorSyntax = []parser.Expr{raw}
	b.Clause.SelectedCallArguments = true
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
	if len(b.Params.Parameters) != 0 {
		t.Fatal("private carrier entered public parameter namespace")
	}
}

func TestSQLRoutineWindowCallShapeTargets(t *testing.T) {
	e := engine.New()
	auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.Query.Auth = &auth
	raw := parser.FunctionExpr{
		Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "lag"}}},
		Args: make([]parser.Expr, 3),
	}
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	var template analyze.SQLCallable
	for d := range definitionProceduresNamed(auth, namespace, "lag") {
		if d.Signature == "5077,23,5077" {
			var ok bool
			template, ok = analyze.SQLCallableShape(auth.Catalog, auth.Definition, d, raw)
			if !ok {
				t.Fatal("missing declared window call shape")
			}
		}
	}
	if template.Procedure == nil {
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
			candidate.Targets = append([]sqltype.Usage(nil), template.Inputs...)
			if got := b.ProbeSQLCallable(raw, operands[:], &candidate); got != tc.viable {
				t.Fatalf("viability=%v, want %v", got, tc.viable)
			}
			if tc.viable && (candidate.Targets[0].Type.ID() != tc.target || candidate.Targets[2].Type.ID() != tc.target) {
				t.Fatal("wrong shared value/default targets", candidate.Targets)
			}
			if candidate.Inputs[0].Type.ID() != 5077 || candidate.Inputs[2].Type.ID() != 5077 {
				t.Fatal("resolved targets replaced declared ranking identities")
			}
			if len(b.Params.Parameters) != 0 {
				t.Fatal("applicability inferred public parameters")
			}
		})
	}
}

func TestSQLRoutineNamedProviderShapes(t *testing.T) {
	e := engine.New()
	auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
	b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
	b.Query.Auth = &auth
	namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
	for _, tc := range []struct {
		name, signature        string
		labels                 []string
		actual, ranked, target []sqltype.ID
	}{
		{"make_interval", "23,23,23,23,23,23,701", []string{"secs"}, []sqltype.ID{sqltype.Float8}, []sqltype.ID{sqltype.Float8}, []sqltype.ID{sqltype.Float8}},
		{"make_time", "23,23,701", []string{"sec", "hour", "min"}, []sqltype.ID{sqltype.Float8, sqltype.Int4, sqltype.Int4}, []sqltype.ID{sqltype.Float8, sqltype.Int4, sqltype.Int4}, []sqltype.ID{sqltype.Float8, sqltype.Int4, sqltype.Int4}},
		{"regexp_replace", "25,25,25,23", []string{"start", "replacement", "pattern", "string"}, []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Text, sqltype.Text}, []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Text, sqltype.Text}, []sqltype.ID{sqltype.Int4, sqltype.Text, sqltype.Text, sqltype.Text}},
		{"array_sort", "2277,16", []string{"descending", "array"}, []sqltype.ID{sqltype.Bool, 1007}, []sqltype.ID{sqltype.Bool, 2277}, []sqltype.ID{sqltype.Bool, 1007}},
		{"parse_ident", "25,16", []string{"strict", "str"}, []sqltype.ID{sqltype.Bool, sqltype.Text}, []sqltype.ID{sqltype.Bool, sqltype.Text}, []sqltype.ID{sqltype.Bool, sqltype.Text}},
		{"json_strip_nulls", "114,16", []string{"strip_in_arrays", "target"}, []sqltype.ID{sqltype.Bool, sqltype.JSON}, []sqltype.ID{sqltype.Bool, sqltype.JSON}, []sqltype.ID{sqltype.Bool, sqltype.JSON}},
		{"json_populate_record", "2283,114,16", []string{"from_json", "base"}, []sqltype.ID{sqltype.JSON, sqltype.Int4}, []sqltype.ID{sqltype.JSON, 2283}, []sqltype.ID{sqltype.JSON, sqltype.Int4}},
		{"pg_get_acl", "26,26,23", []string{"objsubid", "classid", "objid"}, []sqltype.ID{sqltype.Int4, sqltype.Oid, sqltype.Oid}, []sqltype.ID{sqltype.Int4, sqltype.Oid, sqltype.Oid}, []sqltype.ID{sqltype.Int4, sqltype.Oid, sqltype.Oid}},
		{"uuidv7", "1186", []string{"shift"}, []sqltype.ID{sqltype.Interval}, []sqltype.ID{sqltype.Interval}, []sqltype.ID{sqltype.Interval}},
	} {
		t.Run(tc.name+"/"+tc.signature, func(t *testing.T) {
			raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: make([]parser.Expr, len(tc.actual)), ArgNames: make([]parser.Identifier, len(tc.actual))}
			args := make([]functions.Argument, len(tc.actual))
			for i, id := range tc.actual {
				args[i].Usage = b.Type(id).Usage()
				raw.ArgNames[i].Text = tc.labels[i]
			}
			found := false
			for d := range definitionProceduresNamed(auth, namespace, tc.name) {
				if string(d.Signature) != tc.signature {
					continue
				}
				found = true
				candidate, valid := analyze.SQLCallableShape(auth.Catalog, auth.Definition, d, raw)
				if !valid || !b.ProbeSQLCallable(raw, args, &candidate) {
					t.Fatal("named provider rejected checked shape", candidate)
				}
				for i := range args {
					if candidate.Inputs[i].Type.ID() != tc.ranked[i] || candidate.Targets[i].Type.ID() != tc.target[i] {
						t.Fatal("written ranking/target projection", candidate)
					}
				}
				bad := raw
				bad.ArgNames = append([]parser.Identifier(nil), raw.ArgNames...)
				bad.ArgNames[0].Text = "not_a_formal"
				if b.ProbeSQLCallable(bad, args, &candidate) {
					t.Fatal("provider ignored unmatched label")
				}
				if raw.ArgNames[0].Text != tc.labels[0] || string(d.Signature) != tc.signature {
					t.Fatal("probe mutated caller or catalog")
				}
			}
			if !found {
				t.Fatal("missing checked declaration")
			}
		})
	}
}

func TestSQLRoutineNamedCatalogCallShapes(t *testing.T) {
	for _, mode := range []string{"e", "a", "i"} {
		t.Run(mode, func(t *testing.T) {
			e := engine.New()
			for i, target := range []sqltype.ID{sqltype.Int4, sqltype.Float8, sqltype.Text, sqltype.Bool, sqltype.JSON, sqltype.Interval} {
				cast := &catalog.Cast{OID: ids.OID(90000 + i), ObjectRevision: 1, Source: sqltype.Xid, Target: target, Context: mode, Method: "i"}
				e.Catalog.State.SetDescriptor(cast.Address(), cast)
			}
			auth := enginetest.CatalogAuthorization(e, e.CatalogRead())
			b := core.NewExpressionBinder(context.Background(), e, sqltype.InputEnvironment{}, nil, nil)
			b.Query.Auth = &auth
			namespace := ids.ObjectAddress{ClassID: ids.NamespaceClassID, ObjectID: ids.PGCatalogNamespaceOID}
			for _, tc := range []struct {
				name, signature string
				labels          []string
				targets         []sqltype.ID
			}{
				{"make_time", "23,23,701", []string{"sec", "hour", "min"}, []sqltype.ID{sqltype.Float8, sqltype.Int4, sqltype.Int4}},
				{"parse_ident", "25,16", []string{"strict", "str"}, []sqltype.ID{sqltype.Bool, sqltype.Text}},
				{"json_strip_nulls", "114,16", []string{"strip_in_arrays", "target"}, []sqltype.ID{sqltype.Bool, sqltype.JSON}},
				{"uuidv7", "1186", []string{"shift"}, []sqltype.ID{sqltype.Interval}},
			} {
				raw := parser.FunctionExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: tc.name}}}, Args: make([]parser.Expr, len(tc.labels)), ArgNames: make([]parser.Identifier, len(tc.labels))}
				args := make([]functions.Argument, len(tc.labels))
				for i, label := range tc.labels {
					raw.ArgNames[i].Text = label
					args[i].Usage = b.Type(sqltype.Xid).Usage()
				}
				found := false
				for d := range definitionProceduresNamed(auth, namespace, tc.name) {
					if string(d.Signature) != tc.signature {
						continue
					}
					found = true
					candidate, ok := analyze.SQLCallableShape(auth.Catalog, auth.Definition, d, raw)
					if !ok {
						t.Fatal("missing checked named shape", tc.name)
					}
					if got := b.ProbeSQLCallable(raw, args, &candidate); got != (mode == "i") {
						t.Fatal("named catalog admission", tc.name, mode, got)
					}
					if mode == "i" {
						for i, target := range tc.targets {
							if candidate.Inputs[i].Type.ID() != target || candidate.Targets[i].Type.ID() != target || args[i].Usage.Type.ID() != sqltype.Xid {
								t.Fatal("named probe changed ranking or original operands", tc.name, i)
							}
						}
					}
				}
				if !found || len(b.Params.Parameters) != 0 {
					t.Fatal("missing provider or probe inferred parameters", tc.name)
				}
			}
		})
	}
}

// definitionProceduresNamed lists the procedures named name in namespace as
// a's definition reader sees them.
func definitionProceduresNamed(a core.AuthorizationView, namespace ids.ObjectAddress, name string) iter.Seq[*catalog.Procedure] {
	return catalog.DefinitionProceduresNamedAt(a.Catalog, a.Definition, namespace, name)
}
