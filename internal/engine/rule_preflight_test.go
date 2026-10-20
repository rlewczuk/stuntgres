package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestRuleStaticOperatorPreflight(t *testing.T) {
	constant := preflightCast(t, "SELECT 1/(2-2) FROM t")
	ordinary := EvalContext{evalStatement: evalStatement{Context: context.Background()}}
	if _, changed, err := preflightCoercions(&ordinary, constant); err != nil || changed {
		t.Fatalf("ordinary arithmetic demand changed: changed=%v error=%v", changed, err)
	}
	planning := ordinary
	planning.planStaticOperators = true
	_, _, err := preflightCoercions(&planning, constant)
	var diagnostic *sqlstate.Error
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatalf("static operator error was not planned: %v", err)
	}
	parameter := preflightCast(t, "SELECT CAST($1::text AS int4)/0 FROM t").(arithmeticExpression)
	source := parameter.left.(coercionExpression).child.ResultUsage().Type
	value, err := source.Input(context.Background(), "2", -1)
	if err != nil {
		t.Fatal(err)
	}
	planning.Parameters = []sqltype.Value{value}
	mapped, changed, err := preflightCoercions(&planning, parameter)
	if err != nil || !changed {
		t.Fatalf("parameter conversion preflight: changed=%v error=%v", changed, err)
	}
	private, ok := mapped.(arithmeticExpression)
	if !ok || !private.Properties().ParameterDependent {
		t.Fatal("imported parameter became static arithmetic")
	}
	if _, ok := private.left.(literalExpression); !ok {
		t.Fatal("canonical cast preflight was skipped")
	}
	_, err = private.Eval(&planning)
	if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
		t.Fatalf("runtime operator error lost: %v", err)
	}
	t.Run("supplied_parameter_specialization", func(t *testing.T) {
		original := preflightCast(t, "SELECT 1/$1::int4 FROM t").(arithmeticExpression)
		typ := original.right.ResultUsage().Type
		for _, text := range []string{"0", "2", "0"} {
			value, err := typ.Input(context.Background(), text, -1)
			if err != nil {
				t.Fatal(err)
			}
			modeOnly := EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}, planStaticOperators: true}}
			mapped, _, err := preflightCoercions(&modeOnly, original)
			if err != nil || !mapped.Properties().ParameterDependent {
				t.Fatalf("mode-only parameter barrier lost: %v", err)
			}
			specializing := modeOnly
			specializing.specializeRuleParameters = true
			mapped, changed, err := preflightCoercions(&specializing, original)
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
		original := preflightCast(t, "SELECT 1/$1::int4 FROM t").(arithmeticExpression)
		value, err := original.right.ResultUsage().Type.Input(context.Background(), "0", -1)
		if err != nil {
			t.Fatal(err)
		}
		for _, barrier := range []expr.Properties{
			{RowDependent: true}, {StageDependent: true},
			{DomainDependent: true, RuntimeDomainConversion: true}, {Volatility: expr.Volatile},
		} {
			recipe := original
			recipe.Property = expr.MergeProperties(recipe.Property, barrier)
			evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), Parameters: []sqltype.Value{value}, planStaticOperators: true, specializeRuleParameters: true}}
			mapped, changed, err := preflightCoercions(&evaluation, recipe)
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
		schema := querySchema{{Name: "n", Usage: bad.ResultUsage()}}
		child := &projectNode{querySchema: schema, child: &singletonNode{}, expressions: []expr.Expr{bad}, frame: 2}
		derived := &derivedNode{querySchema: schema, query: &BoundQuery{Root: child}, scanFrame: 1}
		outer := &projectNode{querySchema: schema, child: derived, expressions: []expr.Expr{good}, frame: 1}
		evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), planStaticOperators: true}}
		prepared, err := preflightQuery(&evaluation, outer)
		if err != nil {
			t.Fatalf("unused pure child was prepared: %v", err)
		}
		privateChild := prepared.(*projectNode).child.(*derivedNode).query.Root.(*projectNode)
		literal, ok := privateChild.expressions[0].(literalExpression)
		if !ok || !literal.value.IsNull() || literal.ResultUsage() != bad.ResultUsage() {
			t.Fatal("unused payload did not preserve its typed output slot")
		}
		if _, ok := child.expressions[0].(arithmeticExpression); !ok || derived.query.Root != child {
			t.Fatal("derived template was mutated")
		}
		demanded := *outer
		demanded.expressions = []expr.Expr{columnExpression{
			Info:  expr.Info{Usage: bad.ResultUsage(), Property: expr.Properties{RowDependent: true}},
			frame: 1, slot: 0,
		}}
		_, err = preflightQuery(&evaluation, &demanded)
		if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
			t.Fatalf("demanded child was not prepared: %v", err)
		}
		for _, shape := range []string{"unused", "used", "dead_case"} {
			used := shape == "used"
			expression := good
			if used {
				// The mutation target owns slot zero; FROM starts at slot one.
				expression = columnExpression{
					Info:  expr.Info{Usage: bad.ResultUsage(), Property: expr.Properties{RowDependent: true}},
					frame: 1, slot: 1,
				}
			}
			if shape == "dead_case" {
				expression, _, err = rewriteViewExpression(catalog.Read{}, preflightCast(t, "SELECT CASE WHEN false THEN $1::int4 ELSE 7 END FROM t"), nil, func(node expr.Expr) (expr.Expr, bool, error) {
					if parameter, ok := node.(parameterExpression); ok {
						info := parameter.Info
						info.Property = expr.Properties{RowDependent: true}
						return columnExpression{Info: info, frame: 1, slot: 1}, true, nil
					}
					return node, false, nil
				})
				if err != nil {
					t.Fatal(err)
				}
			}
			original := binding{relation: &catalog.Relation{Columns: []catalog.Column{schema[0]}},
				update: &boundUpdate{from: derived, sourceWidth: 1, assignments: []expr.Expr{expression}}}
			private, err := preflightUpdate(&evaluation, original.update, func(prepared *boundUpdate) (LogicalNode, error) {
				current := original
				current.update = prepared
				demanded, err := ruleMutationProducerDemand(current)
				if err != nil {
					return nil, err
				}
				return demanded.update.from, nil
			})
			if used {
				if !errors.As(err, &diagnostic) || diagnostic.Code != "22012" {
					t.Fatalf("external mutation demand was not prepared: %v", err)
				}
			} else if err != nil || private.from == original.update.from {
				t.Fatalf("unused mutation payload was not pruned privately: %v", err)
			}
			if original.update.from != derived || derived.query.Root != child {
				t.Fatal("mutation source template was mutated")
			}
		}
	})
	t.Run("composed_correlated_runtime_demand", func(t *testing.T) {
		e, auth, src, _ := ruleUnitCatalog(t)
		bind := func(sql string, rule bool) (*parser.Select, *BoundQuery) {
			statements, err := parser.Parse(sql)
			if err != nil {
				t.Fatal(err)
			}
			statement := statements[0].(*parser.Select)
			binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			binder.query.frame, binder.query.nextQueryFrame = 1, 1
			if rule {
				binder.target.ruleNamespace = &ruleNamespace{relation: src, ref: ruleUnitRef(e, src), event: "INSERT"}
			}
			query, err := binder.bindSelect(statement, true)
			if err != nil {
				t.Fatal(err)
			}
			return statement, query
		}
		_, supplied := bind("SELECT 0 AS v", false)
		original := ruleUnitInsert(e, src, 1, 0).binding
		original.expressions = nil
		original.targets = []int{1}
		original.query = supplied
		original.commandQuery, original.insert.subquerySource = true, true
		for _, shape := range []string{"demanded", "unused", "dead_case"} {
			output := "q.n"
			if shape == "unused" {
				output = "7"
			}
			if shape == "dead_case" {
				output = "CASE WHEN FALSE THEN q.n ELSE 7 END"
			}
			statement, query := bind("SELECT "+output+" AS out FROM (SELECT 1/NEW.v AS n) AS q", true)
			template := query.Root.(*projectNode)
			childTemplate := template.child.(*derivedNode).query.Root.(*projectNode)
			action := ruleAction{syntax: statement, binding: binding{query: query, commandQuery: true}}
			composed, err := composeRuleAction(auth, original, "INSERT", ruleDefinition{}, action)
			if err != nil {
				t.Fatal(err)
			}
			joined := composed.query.Root.(*projectNode).child.(*joinNode)
			if !joined.correlatedRight || !joined.right.(*projectNode).ruleInvocation {
				t.Fatal("unit did not reach the real correlated rule invocation")
			}
			planning := EvalContext{evalStatement: evalStatement{Context: context.Background(), catalog: auth.Catalog, planStaticOperators: true}}
			prepared, err := preflightQuery(&planning, composed.query.Root)
			if err != nil {
				t.Fatalf("row-dependent payload was evaluated during planning: %v", err)
			}
			if shape == "demanded" {
				supplier := prepared.(*projectNode).child.(*joinNode).left.(*derivedNode).query.Root.(*projectNode).expressions[0]
				value, err := supplier.Eval(&planning)
				integer, accessErr := value.Integer()
				if err != nil || accessErr != nil || integer != 0 {
					t.Fatalf("inherited-frame supplier datum was pruned: value=%v error=%v access=%v", value, err, accessErr)
				}
			}
			rows, count, err := finalizeQuery(context.Background(), prepared, &querySource{}, nil)
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
			walkBoundExpression(childTemplate.expressions[0], func(node expr.Expr) {
				if column, ok := node.(columnExpression); ok && column.scope == rulePseudoScope {
					pseudoRetained = true
				}
			})
			if query.Root != template || !pseudoRetained ||
				original.query.Root != supplied.Root || supplied.Root.(*projectNode).expressions[0].(literalExpression).value.IsNull() {
				t.Fatal("composition or demand preparation mutated a stored provider recipe")
			}
		}
	})
	t.Run("delete_dead_case_source_frontier", func(t *testing.T) {
		bad := preflightCast(t, "SELECT 1/0 FROM t")
		schema := querySchema{{Name: "n", Usage: bad.ResultUsage()}}
		child := &projectNode{querySchema: schema, child: &singletonNode{}, expressions: []expr.Expr{bad}, frame: 2}
		source := &derivedNode{querySchema: schema, query: &BoundQuery{Root: child}, scanFrame: 1}
		predicate, _, err := rewriteViewExpression(catalog.Read{}, preflightCast(t, "SELECT CASE WHEN FALSE THEN $1::int4=0 ELSE TRUE END FROM t"), nil, func(node expr.Expr) (expr.Expr, bool, error) {
			if parameter, ok := node.(parameterExpression); ok {
				info := parameter.Info
				info.Property = expr.Properties{RowDependent: true}
				return columnExpression{Info: info, scope: 1, frame: 1, slot: 1}, true, nil
			}
			return node, false, nil
		})
		if err != nil {
			t.Fatal(err)
		}
		original := binding{relation: &catalog.Relation{Columns: []catalog.Column{schema[0]}}, delete: deleteBinding{using: source}, predicate: predicate}
		private, err := ruleMutationProducerDemand(original)
		if err != nil {
			t.Fatal(err)
		}
		evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), planStaticOperators: true}}
		// Match DELETE's established order: source preparation first, then
		// predicate preparation. Liveness must not evaluate the predicate early.
		prepared, err := preflightQuery(&evaluation, private.delete.using)
		if err != nil {
			t.Fatalf("dead CASE revived source payload demand: %v", err)
		}
		preparedPredicate, _, err := preflightCoercions(&evaluation, private.predicate)
		if err != nil {
			t.Fatal(err)
		}
		value, err := preparedPredicate.Eval(&evaluation)
		if err != nil {
			t.Fatal(err)
		}
		truth, err := TruthOf(value)
		if err != nil || truth != True {
			t.Fatalf("prepared DELETE predicate truth=%v error=%v", truth, err)
		}
		rows, count, err := finalizeQuery(context.Background(), prepared.(*derivedNode).query.Root, &querySource{}, nil)
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
		if original.delete.using != source || source.query.Root != child {
			t.Fatal("DELETE source template was mutated")
		}
		if _, ok := child.expressions[0].(arithmeticExpression); !ok {
			t.Fatal("DELETE source recipe was erased")
		}
	})
}
