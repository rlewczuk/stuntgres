package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/engine/view"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func TestRuleStaticOperatorPreflight(t *testing.T) {
	constant := preflightCast(t, "SELECT 1/(2-2) FROM t")
	ordinary := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
	if _, changed, err := exec.PreflightCoercions(&ordinary, constant); err != nil || changed {
		t.Fatalf("ordinary arithmetic demand changed: changed=%v error=%v", changed, err)
	}
	planning := ordinary
	planning.PlanStaticOperators = true
	_, _, err := exec.PreflightCoercions(&planning, constant)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatalf("static operator error was not planned: %v", err)
	}
	parameter := preflightCast(t, "SELECT CAST($1::text AS int4)/0 FROM t").(plan.ArithmeticExpr)
	source := parameter.Left.(plan.CoercionExpr).Child.ResultUsage().Type
	value, err := source.Input(context.Background(), "2", -1)
	if err != nil {
		t.Fatal(err)
	}
	planning.Parameters = []sqltype.Value{value}
	mapped, changed, err := exec.PreflightCoercions(&planning, parameter)
	if err != nil || !changed {
		t.Fatalf("parameter conversion preflight: changed=%v error=%v", changed, err)
	}
	private, ok := mapped.(plan.ArithmeticExpr)
	if !ok || !private.Properties().ParameterDependent {
		t.Fatal("imported parameter became static arithmetic")
	}
	if _, ok := private.Left.(plan.LiteralExpr); !ok {
		t.Fatal("canonical cast preflight was skipped")
	}
	_, err = private.Eval(&planning)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatalf("runtime operator error lost: %v", err)
	}
	t.Run("supplied_parameter_specialization", func(t *testing.T) {
		original := preflightCast(t, "SELECT 1/$1::int4 FROM t").(plan.ArithmeticExpr)
		typ := original.Right.ResultUsage().Type
		for _, text := range []string{"0", "2", "0"} {
			value, err := typ.Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
			modeOnly := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}, PlanStaticOperators: true}}
			mapped, _, err := exec.PreflightCoercions(&modeOnly, original)
			if err != nil || !mapped.Properties().ParameterDependent {
				t.Fatalf("mode-only parameter barrier lost: %v", err)
			}
			specializing := modeOnly
			specializing.SpecializeRuleParameters = true
			mapped, changed, err := exec.PreflightCoercions(&specializing, original)
			if text == "0" {
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
					t.Fatalf("supplied zero was not prepared: %v", err)
				}
			} else {
				if err != nil || !changed || mapped.Properties().ParameterDependent {
					t.Fatalf("fresh supplied frame was not specialized: %v", err)
				}
				value, err := mapped.Eval(&specializing)
				integer, accessErr := value.Integer()
				if err != nil || accessErr != nil || integer != 0 {
					t.Fatalf("fresh supplied frame result: %v %v %v", value, err, accessErr)
				}
			}
			if !original.Properties().ParameterDependent {
				t.Fatal("stored parameter recipe was mutated")
			}
		}
	})
	t.Run("specialization_preserves_barriers", func(t *testing.T) {
		original := preflightCast(t, "SELECT 1/$1::int4 FROM t").(plan.ArithmeticExpr)
		value, err := original.Right.ResultUsage().Type.Input(context.Background(), "0", -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, barrier := range []expr.Properties{
			{RowDependent: true}, {StageDependent: true},
			{DomainDependent: true, RuntimeDomainConversion: true}, {Volatility: expr.Volatile},
		} {
			recipe := original
			recipe.Property = expr.MergeProperties(recipe.Property, barrier)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}, PlanStaticOperators: true, SpecializeRuleParameters: true}}
			mapped, changed, err := exec.PreflightCoercions(&evaluation, recipe)
			if err != nil || !changed {
				t.Fatalf("specialization crossed runtime barrier %+v: %v", barrier, err)
			}
			properties := mapped.Properties()
			if properties.RowDependent != barrier.RowDependent || properties.StageDependent != barrier.StageDependent ||
				properties.DomainDependent != barrier.DomainDependent || properties.RuntimeDomainConversion != barrier.RuntimeDomainConversion ||
				properties.Volatility != barrier.Volatility {
				t.Fatalf("specialization lost runtime barrier %+v: %+v", barrier, properties)
			}
			if _, err := mapped.Eval(&evaluation); !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
				t.Fatalf("runtime failure was suppressed: %v", err)
			}
		}
	})
	t.Run("derived_payload_demand", func(t *testing.T) {
		bad := preflightCast(t, "SELECT 1/0 FROM t")
		good := preflightCast(t, "SELECT 7 FROM t")
		schema := plan.QuerySchema{{Name: "n", Usage: bad.ResultUsage()}}
		child := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{bad}, Frame: 2}
		derived := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: child}, ScanFrame: 1}
		outer := &plan.ProjectNode{QuerySchema: schema, Child: derived, Expressions: []expr.Expr{good}, Frame: 1}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), PlanStaticOperators: true}}
		prepared, err := view.PreflightQuery(&evaluation, outer)
		if err != nil {
			t.Fatalf("unused pure child was prepared: %v", err)
		}
		privateChild := prepared.(*plan.ProjectNode).Child.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode)
		literal, ok := privateChild.Expressions[0].(plan.LiteralExpr)
		if !ok || !literal.Value.IsNull() || literal.ResultUsage() != bad.ResultUsage() {
			t.Fatal("unused payload did not preserve its typed output slot")
		}
		if _, ok := child.Expressions[0].(plan.ArithmeticExpr); !ok || derived.Query.Root != child {
			t.Fatal("derived template was mutated")
		}
		demanded := *outer
		demanded.Expressions = []expr.Expr{plan.ColumnExpr{
			Info:  expr.Info{Usage: bad.ResultUsage(), Property: expr.Properties{RowDependent: true}},
			Frame: 1, Slot: 0,
		}}
		_, err = view.PreflightQuery(&evaluation, &demanded)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
			t.Fatalf("demanded child was not prepared: %v", err)
		}
		for _, shape := range []string{"unused", "used", "dead_case"} {
			used := shape == "used"
			expression := good
			if used {
				// The mutation target owns slot zero; FROM starts at slot one.
				expression = plan.ColumnExpr{
					Info:  expr.Info{Usage: bad.ResultUsage(), Property: expr.Properties{RowDependent: true}},
					Frame: 1, Slot: 1,
				}
			}
			if shape == "dead_case" {
				expression, _, err = stored.RewriteViewExpression(catalog.Read{}, preflightCast(t, "SELECT CASE WHEN false THEN $1::int4 ELSE 7 END FROM t"), nil, func(node expr.Expr) (expr.Expr, bool, error) {
					if parameter, ok := node.(plan.ParameterExpr); ok {
						info := parameter.Info
						info.Property = expr.Properties{RowDependent: true}
						return plan.ColumnExpr{Info: info, Frame: 1, Slot: 1}, true, nil
					}
					return node, false, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			original := analyze.Binding{Relation: &catalog.Relation{Columns: []catalog.Column{schema[0]}},
				Update: &analyze.UpdateBinding{From: derived, SourceWidth: 1, Assignments: []expr.Expr{expression}}}
			private, err := preflightUpdate(&evaluation, original.Update, func(prepared *analyze.UpdateBinding) (plan.Node, error) {
				current := original
				current.Update = prepared
				demanded, err := view.RuleMutationProducerDemand(current)
				if err != nil {
					return nil, err
				}
				return demanded.Update.From, nil
			})
			if used {
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
					t.Fatalf("external mutation demand was not prepared: %v", err)
				}
			} else if err != nil || private.From == original.Update.From {
				t.Fatalf("unused mutation payload was not pruned privately: %v", err)
			}
			if original.Update.From != derived || derived.Query.Root != child {
				t.Fatal("mutation source template was mutated")
			}
		}
	})
	t.Run("composed_correlated_runtime_demand", func(t *testing.T) {
		e, auth, src, _ := ruleUnitCatalog(t)
		bind := func(sql string, rule bool) (*parser.Select, *plan.Query) {
			statements, err := parser.Parse(sql)
			if err != nil {
				t.Fatal(err)
			}
			statement := statements[0].(*parser.Select)
			binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			binder.Query.Frame, binder.Query.NextQueryFrame = 1, 1
			if rule {
				binder.Target.RuleNamespace = &analyze.RuleNamespace{Relation: src, Ref: ruleUnitRef(e, src), Event: "INSERT"}
			}
			query, err := binder.BindSelect(statement, true)
			if err != nil {
				t.Fatal(err)
			}
			return statement, query
		}
		_, supplied := bind("SELECT 0 AS v", false)
		original := ruleUnitInsert(e, src, 1, 0).Binding()
		original.Expressions = nil
		original.Targets = []int{1}
		original.Query = supplied
		original.CommandQuery, original.Insert.SubquerySource = true, true
		for _, shape := range []string{"demanded", "unused", "dead_case"} {
			output := "q.n"
			if shape == "unused" {
				output = "7"
			}
			if shape == "dead_case" {
				output = "CASE WHEN FALSE THEN q.n ELSE 7 END"
			}
			statement, query := bind("SELECT "+output+" AS out FROM (SELECT 1/NEW.v AS n) AS q", true)
			template := query.Root.(*plan.ProjectNode)
			childTemplate := template.Child.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode)
			action := stored.NewRuleAction(stored.RuleActionParts{Syntax: statement, Binding: analyze.Binding{Query: query, CommandQuery: true}})
			composed, err := view.ComposeRuleAction(auth, original, "INSERT", stored.Rule{}, action)
			if err != nil {
				t.Fatal(err)
			}
			joined := composed.Query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
			if !joined.CorrelatedRight || !joined.Right.(*plan.ProjectNode).RuleInvocation {
				t.Fatal("unit did not reach the real correlated rule invocation")
			}
			planning := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), Catalog: auth.Catalog, PlanStaticOperators: true}}
			prepared, err := view.PreflightQuery(&planning, composed.Query.Root)
			if err != nil {
				t.Fatalf("row-dependent payload was evaluated during planning: %v", err)
			}
			if shape == "demanded" {
				supplier := prepared.(*plan.ProjectNode).Child.(*plan.JoinNode).Left.(*plan.DerivedNode).Query.Root.(*plan.ProjectNode).Expressions[0]
				value, err := supplier.Eval(&planning)
				integer, accessErr := value.Integer()
				if err != nil || accessErr != nil || integer != 0 {
					t.Fatalf("inherited-frame supplier datum was pruned: value=%v error=%v access=%v", value, err, accessErr)
				}
			}
			rows, count, err := exec.Finalize(context.Background(), prepared, &exec.Source{}, nil)
			if shape == "demanded" {
				if err == nil && rows != nil {
					_, err = rows.Next(context.Background(), make([]sqltype.Value, 1))
				}
				if rows != nil {
					rows.Close()
				}
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
					t.Fatalf("retained correlated zero was lost: error=%v", err)
				}
			} else {
				if err != nil || rows == nil || count != 1 {
					t.Fatalf("unused correlated payload was evaluated: count=%d error=%v", count, err)
				}
				values := make([]sqltype.Value, 1)
				if present, err := rows.Next(context.Background(), values); err != nil || !present {
					rows.Close()
					t.Fatalf("expected projected row: present=%v error=%v", present, err)
				}
				integer, err := values[0].Integer()
				rows.Close()
				if err != nil || integer != 7 {
					t.Fatalf("unused projection result=%d error=%v", integer, err)
				}
			}
			pseudoRetained := false
			plan.WalkExpression(childTemplate.Expressions[0], func(node expr.Expr) {
				if column, ok := node.(plan.ColumnExpr); ok && column.Scope == analyze.RulePseudoScope {
					pseudoRetained = true
				}
			})
			if query.Root != template || !pseudoRetained ||
				original.Query.Root != supplied.Root || supplied.Root.(*plan.ProjectNode).Expressions[0].(plan.LiteralExpr).Value.IsNull() {
				t.Fatal("composition or demand preparation mutated a stored provider recipe")
			}
		}
	})
	t.Run("delete_dead_case_source_frontier", func(t *testing.T) {
		bad := preflightCast(t, "SELECT 1/0 FROM t")
		schema := plan.QuerySchema{{Name: "n", Usage: bad.ResultUsage()}}
		child := &plan.ProjectNode{QuerySchema: schema, Child: &plan.SingletonNode{}, Expressions: []expr.Expr{bad}, Frame: 2}
		source := &plan.DerivedNode{QuerySchema: schema, Query: &plan.Query{Root: child}, ScanFrame: 1}
		predicate, _, err := stored.RewriteViewExpression(catalog.Read{}, preflightCast(t, "SELECT CASE WHEN FALSE THEN $1::int4=0 ELSE TRUE END FROM t"), nil, func(node expr.Expr) (expr.Expr, bool, error) {
			if parameter, ok := node.(plan.ParameterExpr); ok {
				info := parameter.Info
				info.Property = expr.Properties{RowDependent: true}
				return plan.ColumnExpr{Info: info, Scope: 1, Frame: 1, Slot: 1}, true, nil
			}
			return node, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		original := analyze.Binding{Relation: &catalog.Relation{Columns: []catalog.Column{schema[0]}}, Delete: analyze.DeleteBinding{Using: source}, Predicate: predicate}
		private, err := view.RuleMutationProducerDemand(original)
		if err != nil {
			t.Fatal(err)
		}
		evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background(), PlanStaticOperators: true}}
		// Match DELETE's established order: source preparation first, then
		// predicate preparation. Liveness must not evaluate the predicate early.
		prepared, err := view.PreflightQuery(&evaluation, private.Delete.Using)
		if err != nil {
			t.Fatalf("dead CASE revived source payload demand: %v", err)
		}
		preparedPredicate, _, err := exec.PreflightCoercions(&evaluation, private.Predicate)
		if err != nil {
			t.Fatal(err)
		}
		value, err := preparedPredicate.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		truth, err := plan.TruthOf(value)
		if err != nil || truth != plan.True {
			t.Fatalf("prepared DELETE predicate truth=%v error=%v", truth, err)
		}
		rows, count, err := exec.Finalize(context.Background(), prepared.(*plan.DerivedNode).Query.Root, &exec.Source{}, nil)
		if err != nil || rows == nil || count != 1 {
			t.Fatalf("row-presence source was lost: count=%d error=%v", count, err)
		}
		values := make([]sqltype.Value, 1)
		if present, err := rows.Next(context.Background(), values); err != nil || !present {
			rows.Close()
			t.Fatalf("expected row-presence source: present=%v error=%v", present, err)
		}
		rows.Close()
		if !values[0].IsNull() {
			t.Fatal("unused source did not retain a typed NULL payload slot")
		}
		if original.Delete.Using != source || source.Query.Root != child {
			t.Fatal("DELETE source template was mutated")
		}
		if _, ok := child.Expressions[0].(plan.ArithmeticExpr); !ok {
			t.Fatal("DELETE source recipe was erased")
		}
	})
}
