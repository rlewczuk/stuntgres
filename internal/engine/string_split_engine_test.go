package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func stringSplitMarker(t *testing.T, b *expressionBinder, pos int, args ...BoundExpr) stringExpression {
	t.Helper()
	expr, err := b.stringNode("string_to_table", b.typ(sqltype.Text).Usage(), parser.Position{Offset: pos, Character: pos + 1}, args, [4]bool{true, true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	return expr.(stringExpression)
}
func stringSplitLiteral(b *expressionBinder, s string) BoundExpr {
	text := b.typ(sqltype.Text)
	return literalExpression{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, s)}
}
func TestStringSplitExpansion(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	t.Run("relative_target_attempt_order", func(t *testing.T) {
		scalarFailure := errors.New("earlier scalar target failed")
		sourceFailure := errors.New("set source argument failed")
		for _, scalarFirst := range []bool{true, false} {
			query := "SELECT 's',string_to_table('a,b',',')"
			prefix := "scalar_first"
			if !scalarFirst {
				query, prefix = "SELECT string_to_table('a,b',','),'s'", "srf_first"
			}
			for _, scenario := range []struct {
				name, input              string
				scalarError, sourceError error
				wantRows                 int
			}{
				{"multirow", "a,b", nil, nil, 2},
				{"empty", "", nil, nil, 0},
				{"scalar_error", "a,b", scalarFailure, nil, 0},
				{"source_error", "a,b", nil, sourceFailure, 0},
			} {
				t.Run(prefix+"/"+scenario.name, func(t *testing.T) {
					binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
					binder.aggregateClause = "SELECT"
					raw := statement(t, query).(*parser.Select)
					requests := make([]BoundExpr, len(raw.Targets))
					for i, target := range raw.Targets {
						var err error
						requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
						if err != nil {
							t.Fatal(err)
						}
					}
					root, err := binder.lowerStringSets(&singletonNode{}, requests, nil, true)
					if err != nil {
						t.Fatal(err)
					}
					node := root.(*setProjectNode)
					text := binder.typ(sqltype.Text)
					var observed []int
					node.scalars[0] = mathObservedArgument{expressionInfo: boundExpressionInfo(node.scalars[0]), value: sqltype.String(text, "s"), order: &observed, index: 0, err: scenario.scalarError}
					node.calls[0].args[0] = mathObservedArgument{expressionInfo: boundExpressionInfo(node.calls[0].args[0]), value: sqltype.String(text, scenario.input), order: &observed, index: 1, err: scenario.sourceError}
					stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
					if err != nil {
						t.Fatal(err)
					}
					defer stage.Close()
					rows := 0
					var reached error
					for {
						_, present, err := stage.nextOccurrence(context.Background())
						if err != nil {
							reached = err
							break
						}
						if !present {
							break
						}
						rows++
					}
					wantOrder := []int{0, 1, 0, 0}
					if !scalarFirst {
						wantOrder = []int{1, 0, 0, 0}
					}
					wantError := scenario.scalarError
					if scenario.sourceError != nil {
						wantError = scenario.sourceError
					}
					if scenario.name == "empty" {
						wantOrder = wantOrder[:2]
					} else if scenario.scalarError != nil {
						if scalarFirst {
							wantOrder = []int{0}
						} else {
							wantOrder = []int{1, 0}
						}
					} else if scenario.sourceError != nil {
						if scalarFirst {
							wantOrder = []int{0, 1}
						} else {
							wantOrder = []int{1}
						}
					}
					if rows != scenario.wantRows || !errors.Is(reached, wantError) || !reflect.DeepEqual(observed, wantOrder) {
						t.Fatal("relative attempt/error frontier mismatch", rows, reached, observed, wantOrder)
					}
				})
			}
		}
	})
	t.Run("effective_shallow_scalar_attempts", func(t *testing.T) {
		scalarFailure := errors.New("transformed shallow scalar failed")
		sourceFailure := errors.New("deeper set argument failed")
		for _, scalarFirst := range []bool{true, false} {
			prefix := "scalar_first"
			if !scalarFirst {
				prefix = "set_first"
			}
			for _, scenario := range []struct {
				name, input              string
				scalarError, sourceError error
				wantRows                 [][]string
			}{
				{"multirow", "a:b", nil, nil, [][]string{{"px", "a"}, {"px", "b"}}},
				{"empty", "", nil, nil, nil},
				{"scalar_error", "a:b", scalarFailure, nil, nil},
				{"source_error", "a:b", nil, sourceFailure, nil},
			} {
				t.Run(prefix+"/"+scenario.name, func(t *testing.T) {
					binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
					binder.aggregateClause = "SELECT"
					shallow := "'p'||string_to_table('x',',')"
					deep := "string_to_table(string_to_table('" + scenario.input + "',','),':')"
					query := "SELECT " + shallow + "," + deep
					if !scalarFirst {
						query = "SELECT " + deep + "," + shallow
					}
					raw := statement(t, query).(*parser.Select)
					requests := make([]BoundExpr, len(raw.Targets))
					schema := make(querySchema, len(raw.Targets))
					for i, target := range raw.Targets {
						var err error
						requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
						if err != nil {
							t.Fatal(err)
						}
						schema[i] = ColumnDescriptor{Usage: requests[i].ResultUsage()}
					}
					root, err := binder.lowerStringSets(&singletonNode{}, requests, nil, true)
					if err != nil {
						t.Fatal(err)
					}
					owner := &projectNode{querySchema: schema, child: root, expressions: requests, frame: 1, hasTargetStringSet: true}
					private, err := preflightQuery(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, catalog: binder.bindingCatalog()}, owner)
					if err != nil {
						t.Fatal(err)
					}
					project := private.(*projectNode)
					expansion := project.child.(*setProjectNode)
					for _, scalar := range expansion.scalars {
						if stringSetDepth(scalar) != 0 || scalar.ResultUsage().Type == nil {
							t.Fatal("effective scalar retained an executable set marker or unresolved usage", scalar)
						}
					}
					var observed []int
					instrument := func(expression BoundExpr) (BoundExpr, error) {
						next, _, err := mapBoundExpression(binder.bindingCatalog(), expression,
							func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ },
							func(query LogicalNode) (LogicalNode, error) { return query, nil },
							func(expression BoundExpr) (BoundExpr, bool, error) {
								literal, ok := expression.(literalExpression)
								if !ok || literal.value.IsNull() {
									return expression, false, nil
								}
								value, err := literal.value.Text()
								if err != nil || value != "p" && value != ":" {
									return expression, false, nil
								}
								index, failure := 0, scenario.scalarError
								if value == ":" {
									index, failure = 1, scenario.sourceError
								}
								info := boundExpressionInfo(expression)
								info.properties.Volatility = Volatile
								return mathObservedArgument{expressionInfo: info, value: literal.value, order: &observed, index: index, err: failure}, true, nil
							})
						return next, err
					}
					for i, expression := range project.expressions {
						project.expressions[i], err = instrument(expression)
						if err != nil {
							t.Fatal(err)
						}
					}
					for i, expression := range expansion.scalars {
						expansion.scalars[i], err = instrument(expression)
						if err != nil {
							t.Fatal(err)
						}
					}
					for i, call := range expansion.calls {
						for j, argument := range call.args {
							expansion.calls[i].args[j], err = instrument(argument)
							if err != nil {
								t.Fatal(err)
							}
						}
					}
					executable, err := openQueryNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, private)
					if err != nil {
						t.Fatal(err)
					}
					defer executable.Close()
					values := make([]sqltype.Value, len(raw.Targets))
					var rows [][]string
					var reached error
					for {
						present, err := executable.nextProjected(context.Background(), values)
						if err != nil {
							reached = err
							break
						}
						if !present {
							break
						}
						row := make([]string, len(values))
						for i, value := range values {
							row[i], err = value.Text()
							if err != nil {
								t.Fatal(err)
							}
						}
						if !scalarFirst {
							row[0], row[1] = row[1], row[0]
						}
						rows = append(rows, row)
					}
					wantOrder := []int{0, 1, 0, 0}
					if !scalarFirst {
						wantOrder = []int{1, 0, 0, 0}
					}
					wantError := scenario.scalarError
					if scenario.name == "empty" {
						wantOrder = wantOrder[:2]
					} else if scenario.scalarError != nil {
						if scalarFirst {
							wantOrder = []int{0}
						} else {
							wantOrder = []int{1, 0}
						}
					} else if scenario.sourceError != nil {
						wantError = scenario.sourceError
						if scalarFirst {
							wantOrder = []int{0, 1}
						} else {
							wantOrder = []int{1}
						}
					}
					if !reflect.DeepEqual(rows, scenario.wantRows) || !errors.Is(reached, wantError) || !reflect.DeepEqual(observed, wantOrder) {
						t.Fatal("effective scalar rows/terminal/error frontier mismatch", rows, reached, observed, wantOrder)
					}
				})
			}
		}
	})
	t.Run("interleaved_target_schedule", func(t *testing.T) {
		binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		binder.aggregateClause = "SELECT"
		raw := statement(t, "SELECT 's',string_to_table('a,b',','),'t',string_to_table('x',',')").(*parser.Select)
		requests := make([]BoundExpr, len(raw.Targets))
		for i, target := range raw.Targets {
			var err error
			requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
		}
		root, err := binder.lowerStringSets(&singletonNode{}, requests, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		node := root.(*setProjectNode)
		trace := []int{}
		for i, value := range []string{"s", "t"} {
			node.scalars[i] = mathObservedArgument{expressionInfo: expressionInfo{usage: node.scalars[i].ResultUsage(), properties: ExprProperties{Volatility: Volatile}}, value: sqltype.String(node.scalars[i].ResultUsage().Type, value), order: &trace, index: i}
		}
		for i, value := range []string{"a,b", "x"} {
			node.calls[i].args = append([]BoundExpr(nil), node.calls[i].args...)
			node.calls[i].args[0] = mathObservedArgument{expressionInfo: expressionInfo{usage: node.calls[i].args[0].ResultUsage(), properties: ExprProperties{Volatility: Volatile}}, value: sqltype.String(node.calls[i].args[0].ResultUsage().Type, value), order: &trace, index: 10 + i}
		}
		stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
		if err != nil {
			t.Fatal(err)
		}
		defer stage.Close()
		rows := 0
		for {
			_, present, err := stage.nextOccurrence(context.Background())
			if err != nil {
				t.Fatal(err)
			}
			if !present {
				break
			}
			rows++
		}
		if rows != 2 || !reflect.DeepEqual(trace, []int{0, 10, 1, 11, 0, 1, 0, 1}) {
			t.Fatal("target events reordered or exhausted source reopened", rows, trace)
		}
	})
	text := b.typ(sqltype.Text)
	var order []int
	input := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, "a,b"), order: &order, index: 0}
	delimiter := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.String(text, ","), order: &order, index: 1}
	marker := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 2}
	first := stringSplitMarker(t, b, 1, input, delimiter, marker)
	second := stringSplitMarker(t, b, 2, stringSplitLiteral(b, "x"), stringSplitLiteral(b, ","))
	scalar := mathObservedArgument{expressionInfo: expressionInfo{usage: text.Usage(), properties: ExprProperties{Volatility: Volatile}}, value: sqltype.String(text, "s"), order: &order, index: 3}
	node := &setProjectNode{child: &singletonNode{}, calls: []stringExpression{first, second}, callSlots: []int{0, 1}, markerWidth: 2, scalars: []BoundExpr{scalar}, scalarSlots: []int{0}, steps: []stringSetStep{{kind: stringSetCallStep, index: 0}, {kind: stringSetCallStep, index: 1}, {kind: stringSetScalarStep, index: 0}}, frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}}
	t.Run("sparse_slot_invariants", func(t *testing.T) {
		for _, tc := range []struct {
			name, message string
			slots         []int
		}{
			{"negative", "stuntgres: invalid string set call slot", []int{-1, 1}},
			{"outside_layout", "stuntgres: invalid string set call slot", []int{0, 2}},
			{"duplicate", "stuntgres: duplicate string set call slot", []int{0, 0}},
			{"missing", "stuntgres: invalid string set frame", []int{0}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				private := *node
				private.callSlots = tc.slots
				if _, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil || err.Error() != tc.message {
					t.Fatal("invalid fixed marker layout admitted", err)
				}
			})
		}
	})
	t.Run("scalar_slot_invariants", func(t *testing.T) {
		for _, slots := range [][]int{{}, {-1}, {1}} {
			private := *node
			private.scalarSlots = slots
			if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
				stage.Close()
				t.Fatal("invalid explicit scalar slot admitted", slots)
			}
		}
		private := *node
		private.querySchema = append(append(querySchema(nil), node.querySchema...), ColumnDescriptor{Usage: scalar.ResultUsage()})
		private.scalars = []BoundExpr{scalar, scalar}
		private.scalarSlots = []int{0, 0}
		if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
			stage.Close()
			t.Fatal("duplicate scalar slot admitted")
		}
		private = *node
		private.scalars = []BoundExpr{first}
		if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
			stage.Close()
			t.Fatal("unlowered marker admitted as scalar event")
		}
	})
	t.Run("invalid_target_schedule", func(t *testing.T) {
		for _, steps := range [][]stringSetStep{
			nil,
			{{kind: stringSetStepKind(255), index: 0}, {kind: stringSetCallStep, index: 1}, {kind: stringSetScalarStep, index: 0}},
			{{kind: stringSetCallStep, index: -1}, {kind: stringSetCallStep, index: 1}, {kind: stringSetScalarStep, index: 0}},
			{{kind: stringSetCallStep, index: 2}, {kind: stringSetCallStep, index: 1}, {kind: stringSetScalarStep, index: 0}},
			{{kind: stringSetCallStep, index: 0}, {kind: stringSetCallStep, index: 0}, {kind: stringSetScalarStep, index: 0}},
			{{kind: stringSetCallStep, index: 0}, {kind: stringSetCallStep, index: 1}, {kind: stringSetScalarStep, index: 1}},
		} {
			private := *node
			private.steps = steps
			if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
				stage.Close()
				t.Fatal("malformed target schedule admitted", steps)
			}
		}
		if err := validateStringSetStageOrder(
			[]stringSetStep{{kind: stringSetCallStep, index: 0}, {kind: stringSetScalarStep, index: 0}, {kind: stringSetCallStep, index: 1}},
			node.steps, node.callSlots, node.scalarSlots, len(node.scalars)); err == nil {
			t.Fatal("valid event coverage concealed a reordered schedule")
		}
	})
	stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	var rows [][]any
	for {
		i, present, err := stage.nextOccurrence(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		row := stage.source.row(stage.index(i))
		values := make([]any, len(row))
		for j, v := range row {
			if !v.IsNull() {
				values[j], _ = v.Text()
			}
		}
		rows = append(rows, values)
	}
	if !reflect.DeepEqual(rows, [][]any{{"a", "x", "s"}, {"b", nil, "s"}}) || !reflect.DeepEqual(order, []int{0, 1, 2, 3, 3, 3}) {
		t.Fatal(rows, order)
	}
	if first.plan.Name() != "string_to_table" || !first.Properties().StageDependent || conditionalConstantInputs(first) {
		t.Fatal("lost set marker", first.Properties())
	}
	if _, err := first.Eval(&EvalContext{Context: context.Background()}); err == nil {
		t.Fatal("unlowered set expression evaluated")
	}
}
func TestStringSplitFrames(t *testing.T) {
	t.Run("maximum_depth_scalar_ownership", func(t *testing.T) {
		for position, query := range []string{
			"SELECT 7,upper(string_to_table('x,y',',')),string_to_table(string_to_table('a:b,c:d',','),':')",
			"SELECT 7,string_to_table(string_to_table('a:b,c:d',','),':'),upper(string_to_table('x,y',','))",
		} {
			binder := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
			binder.aggregateClause = "SELECT"
			raw := statement(t, query).(*parser.Select)
			requests := make([]BoundExpr, len(raw.Targets))
			schema := make(querySchema, len(raw.Targets))
			for i, target := range raw.Targets {
				var err error
				requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				schema[i] = ColumnDescriptor{Usage: requests[i].ResultUsage()}
			}
			root, err := binder.lowerStringSets(&singletonNode{}, requests, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			owner := &projectNode{querySchema: schema, child: root, expressions: requests, frame: 1, hasTargetStringSet: true}
			executable, err := openQueryNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, owner)
			if err != nil {
				t.Fatal(err)
			}
			var rows [][]any
			values := make([]sqltype.Value, len(raw.Targets))
			for {
				present, err := executable.nextProjected(context.Background(), values)
				if err != nil {
					executable.Close()
					t.Fatal(err)
				}
				if !present {
					break
				}
				n, err := values[0].Integer()
				if err != nil {
					executable.Close()
					t.Fatal(err)
				}
				shallow, err := values[1].Text()
				if err != nil {
					executable.Close()
					t.Fatal(err)
				}
				deep, err := values[2].Text()
				if err != nil {
					executable.Close()
					t.Fatal(err)
				}
				if position == 1 {
					shallow, deep = deep, shallow
				}
				rows = append(rows, []any{n, shallow, deep})
			}
			executable.Close()
			if !reflect.DeepEqual(rows, [][]any{{int64(7), "X", "a"}, {int64(7), "X", "b"}, {int64(7), "Y", "c"}, {int64(7), "Y", "d"}}) {
				t.Fatal("mixed-depth scalar ownership changed projected values", rows)
			}
		}
	})
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.queryFrame = 7
	inner := stringSplitMarker(t, b, 11, stringSplitLiteral(b, "a:b,c:d"), stringSplitLiteral(b, ","))
	outer := stringSplitMarker(t, b, 12, inner, stringSplitLiteral(b, ":"))
	peer := stringSplitMarker(t, b, 13, stringSplitLiteral(b, "x,y,z"), stringSplitLiteral(b, ","))
	projection := []BoundExpr{outer, peer}
	companion := []BoundExpr{peer}
	root, err := b.lowerStringSets(&singletonNode{}, projection, [][]BoundExpr{companion}, true)
	if err != nil {
		t.Fatal(err)
	}
	upper := root.(*setProjectNode)
	lower := upper.child.(*setProjectNode)
	if len(lower.calls) != 2 || len(upper.calls) != 1 || len(upper.Schema()) != 3 || stringSetDepth(projection[0]) != 0 || stringSetDepth(projection[1]) != 0 {
		t.Fatal("wrong expansion levels", lower, upper)
	}
	if projection[1].(columnExpression).slot != companion[0].(columnExpression).slot || projection[0].(columnExpression).frame != 7 {
		t.Fatal("lost alias/frame occurrence")
	}
	stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 7}, upper)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	var rows [][]string
	evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 7}
	for {
		i, present, err := stage.nextOccurrence(context.Background())
		if err != nil {
			t.Fatal(err)
		}
		if !present {
			break
		}
		evaluation.Row = stage.source.row(stage.index(i))
		row := make([]string, 2)
		for j, expr := range projection {
			v, err := expr.Eval(&evaluation)
			if err != nil {
				t.Fatal(err)
			}
			row[j], _ = v.Text()
		}
		rows = append(rows, row)
	}
	if !reflect.DeepEqual(rows, [][]string{{"a", "x"}, {"b", "x"}, {"c", "y"}, {"d", "y"}}) {
		t.Fatal(rows)
	}
}
func TestStringSplitBinding(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	for _, q := range []string{"SELECT string_to_table('a,b',',')", "SELECT string_to_array('a,b',',',NULL)", "SELECT split_part('a,b',',',-1)"} {
		expr, err := b.bind(statement(t, q).(*parser.Select).Targets[0], expressionPolicy{context: projectionContext})
		if err != nil {
			t.Fatal(q, err)
		}
		fresh := New().CatalogRead()
		mapped, changed, err := retagBoundTypes(fresh, expr, func(d *sqltype.Descriptor) *sqltype.Descriptor { next, _ := fresh.LookupType(d.ID()); return next })
		if err != nil || !changed || mapped.ResultUsage().Type == expr.ResultUsage().Type {
			t.Fatal(q, mapped, changed, err)
		}
		n := mapped.(stringExpression)
		if n.plan.Name() == "string_to_table" {
			if conditionalConstantInputs(n) || !n.Properties().StageDependent {
				t.Fatal("foldable set marker")
			}
			continue
		}
		if _, err := n.Eval(&EvalContext{Context: context.Background()}); err != nil {
			t.Fatal(q, err)
		}
	}
	b.stringSetGuard = "CASE"
	b.queryHasTargetStringSet = true
	restore := b.enterQuery(false)
	if b.stringSetGuard != "" || b.queryHasTargetStringSet {
		t.Fatal("query guard/flag crossed boundary")
	}
	restore()
	if b.stringSetGuard != "CASE" || !b.queryHasTargetStringSet {
		t.Fatal("query guard/flag not restored")
	}
	b.queryHasTargetStringSet = false
	child := b.enterQuery(false)
	b.queryHasTargetStringSet = true
	child()
	if b.queryHasTargetStringSet {
		t.Fatal("child query set flag escaped owner")
	}
	b.stringSetGuard = ""
	raw := statement(t, "SELECT replace(string_to_table('a,b',','),NULL,'x')").(*parser.Select)
	expr, err := b.bind(raw.Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	folded, _, err := simplifyConditional(&EvalContext{Context: context.Background()}, expr, false)
	if err != nil {
		t.Fatal(err)
	}
	literal, ok := folded.(literalExpression)
	if !ok || !literal.value.IsNull() || !b.queryHasTargetStringSet {
		t.Fatal("lost original set analysis after strict folding", folded)
	}
	descriptor := &relationDescriptor{view: &viewDefinition{syntax: raw, query: &BoundQuery{Root: &projectNode{child: &singletonNode{}, expressions: []BoundExpr{folded}, hasTargetStringSet: b.queryHasTargetStringSet}}}}
	if _, _, _, reason := viewWriteShape(descriptor); reason != "Views that return set-returning functions are not automatically updatable." {
		t.Fatal(reason)
	}
}
func TestStringSplitLifecycle(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	t.Run("retained_template_analysis", func(t *testing.T) {
		call := stringSplitMarker(t, b, 21, stringSplitLiteral(b, "a,b"), stringSplitLiteral(b, ","))
		frame := uint32(1)
		makeStage := func(analysis unknownOutput) *setProjectNode {
			return &setProjectNode{child: &singletonNode{}, calls: []stringExpression{call}, callSlots: []int{0},
				markerWidth: 1, frame: frame, querySchema: querySchema{{Usage: call.ResultUsage()}}, steps: []stringSetStep{{kind: stringSetCallStep, index: 0}},
				template: &stringSetTemplate{group: &frame, expressions: []BoundExpr{analysis, call}, origins: []BoundExpr{call}, attempts: true, steps: []stringSetStep{{kind: stringSetCallStep, index: 0}}}}
		}
		for _, kind := range []parser.ValueKind{parser.NullValue, parser.StringValue} {
			stage := makeStage(unknownOutput{raw: parser.LiteralExpr{Value: parser.Value{Kind: kind, Text: "retained"}}})
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err != nil {
				t.Fatal("genuine contextual template root rejected", err)
			}
			for range 2 {
				mapped, err := remapViewQuery(b.bindingCatalog(), stage, func(ref ObjectRef) ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
				if err != nil {
					t.Fatal(err)
				}
				if mapped.(*setProjectNode).template.group != stage.template.group {
					t.Fatal("capture changed local-group identity")
				}
				if !reflect.DeepEqual(mapped.(*setProjectNode).steps, []stringSetStep{{kind: stringSetCallStep, index: 0}}) ||
					!reflect.DeepEqual(mapped.(*setProjectNode).template.steps, []stringSetStep{{kind: stringSetCallStep, index: 0}}) {
					t.Fatal("capture changed target schedule")
				}
			}
		}
		for _, program := range [][]stringSetStep{nil, {{kind: stringSetStepKind(255), index: 0}}, {{kind: stringSetCallStep, index: 1}}} {
			stage := makeStage(unknownOutput{raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}})
			stage.steps = program
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
				t.Fatal("stored executable schedule admitted malformed event", program)
			}
			stage.steps = []stringSetStep{{kind: stringSetCallStep, index: 0}}
			stage.template.steps = program
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
				t.Fatal("stored template schedule admitted malformed event", program)
			}
		}
		for _, malformed := range []unknownOutput{
			{raw: parser.ColumnExpr{}},
			{raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}},
			{expressionInfo: expressionInfo{usage: call.ResultUsage()}, raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}},
		} {
			stage := makeStage(malformed)
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil || err.Error() != "stuntgres: invalid contextual view output" {
				t.Fatal("malformed contextual template accepted", err)
			}
		}
	})
	t.Run("scalar_slot_capture", func(t *testing.T) {
		call := stringSplitMarker(t, b, 31, stringSplitLiteral(b, "a,b"), stringSplitLiteral(b, ","))
		scalar := stringSplitLiteral(b, "s")
		frame := uint32(1)
		program := []stringSetStep{{kind: stringSetScalarStep, index: 0}, {kind: stringSetCallStep, index: 0}}
		makeStage := func() *setProjectNode {
			return &setProjectNode{child: &singletonNode{}, frame: frame, calls: []stringExpression{call}, callSlots: []int{0},
				markerWidth: 1, scalars: []BoundExpr{scalar}, steps: program,
				querySchema: querySchema{{Usage: call.ResultUsage()}, {Usage: scalar.ResultUsage()}},
				template: &stringSetTemplate{group: &frame, expressions: []BoundExpr{scalar, call}, origins: []BoundExpr{call},
					scalars: []BoundExpr{scalar}, scalarRequests: []int{0}, attempts: true, steps: program}}
		}
		stage := makeStage()
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err != nil {
			t.Fatal("valid identity scalar reservation rejected", err)
		}
		for range 2 {
			mapped, err := remapViewQuery(b.bindingCatalog(), stage, func(ref ObjectRef) ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mapped.(*setProjectNode).scalarSlots, stage.scalarSlots) || !reflect.DeepEqual(mapped.(*setProjectNode).template.scalarRequests, stage.template.scalarRequests) {
				t.Fatal("capture changed scalar candidate identity")
			}
		}
		for _, slots := range [][]int{{}, {-1}, {1}} {
			stage := makeStage()
			stage.scalarSlots = slots
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
				t.Fatal("malformed stored explicit scalar reservation admitted", slots)
			}
		}
		stage = makeStage()
		stage.template.scalarRequests = []int{1}
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
			t.Fatal("scalar candidate mapped to a call owner")
		}
	})
	t.Run("contextual_view_analysis", func(t *testing.T) {
		value := stringSplitLiteral(b, "kept")
		unknown := unknownOutput{raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
		mapped, changed, err := retagBoundTypes(New().CatalogRead(), unknown, func(*sqltype.Descriptor) *sqltype.Descriptor {
			t.Fatal("contextual analysis invented a descriptor to retag")
			return nil
		})
		if err != nil || changed || !reflect.DeepEqual(mapped, unknown) {
			t.Fatal("contextual analysis changed during retag", mapped, changed, err)
		}
		captured, err := remapBoundExpression(unknown, func(ref ObjectRef) ObjectRef {
			t.Fatal("contextual analysis invented a captured reference", ref)
			return ref
		}, nil)
		if err != nil || !reflect.DeepEqual(captured, unknown) {
			t.Fatal("contextual analysis changed during capture", captured, err)
		}
		if _, err := remapBoundExpression(unknownOutput{raw: parser.ColumnExpr{}}, nil, nil); err == nil || err.Error() != "stuntgres: invalid contextual view output" {
			t.Fatal("nonliteral contextual capture accepted", err)
		}
		project := &projectNode{
			querySchema:         querySchema{{Usage: value.ResultUsage()}},
			child:               &singletonNode{},
			expressions:         []BoundExpr{value},
			analysisExpressions: []BoundExpr{unknown},
		}
		definition := &viewDefinition{query: &BoundQuery{Root: project}}
		if err := validateViewLeaves(b.bindingCatalog(), definition); err != nil {
			t.Fatal("retained contextual literal rejected", err)
		}
		foreignType := *value.ResultUsage().Type
		foreignUsage := value.ResultUsage()
		foreignUsage.Type = &foreignType
		missingCollation := value.ResultUsage()
		missingCollation.Collation.ID = ^uint32(0)
		for _, tc := range []struct {
			name, message string
			usage         sqltype.Usage
		}{
			{"invalid_project_schema", "stuntgres: invalid view query usage", sqltype.Usage{}},
			{"foreign_project_schema", "stuntgres: foreign view query type", foreignUsage},
			{"missing_project_collation", "stuntgres: invalid view query usage", missingCollation},
		} {
			t.Run(tc.name, func(t *testing.T) {
				private := *project
				private.querySchema = querySchema{{Usage: tc.usage}}
				if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: &private}}); err == nil || err.Error() != tc.message {
					t.Fatal("malformed executable schema accepted", err)
				}
			})
		}
		private := *project
		private.expressions = []BoundExpr{unknown}
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: &private}}); err == nil || err.Error() != "stuntgres: invalid view query usage" {
			t.Fatal("unresolved executable output accepted", err)
		}
		private = *project
		private.analysisExpressions = []BoundExpr{unknownOutput{raw: parser.ColumnExpr{}}}
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: &private}}); err == nil || err.Error() != "stuntgres: invalid contextual view output" {
			t.Fatal("nonliteral contextual placeholder accepted", err)
		}
	})
	call := stringSplitMarker(t, b, 1, stringSplitLiteral(b, "a,b"), stringSplitLiteral(b, ","))
	node := &stringTableNode{calls: []stringExpression{call}, querySchema: querySchema{{Usage: call.ResultUsage()}}, frame: 1}
	open := func() (indexedStage, error) {
		return openStringTableNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
	}
	stage, err := open()
	if err != nil {
		t.Fatal(err)
	}
	if _, present, err := stage.nextOccurrence(context.Background()); err != nil || !present {
		t.Fatal(present, err)
	}
	source := stage.source
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := stage.nextOccurrence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if !stage.closed || source.flatCount != 1 {
		t.Fatal("canceled invocation published later fields")
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	failures := make(chan error, 4)
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			next, err := open()
			if err == nil {
				err = next.drain(context.Background())
				if err == nil && next.source.flatCount != 2 {
					err = errors.New("retained blueprint shared cursor progress")
				}
				_ = next.Close()
			}
			failures <- err
		}()
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		if err != nil {
			t.Fatal(err)
		}
	}
}

func TestStringSplitPreflight(t *testing.T) {
	t.Run("scalar_owner_boundary", func(t *testing.T) {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		text := b.typ(sqltype.Text)
		info := expressionInfo{usage: text.Usage()}
		lowered := columnExpression{expressionInfo: info, scope: 1, frame: 1, slot: 0}
		reserved := columnExpression{expressionInfo: info, scope: 1, frame: 1, slot: 1}
		for _, tc := range []struct {
			name             string
			scheduled, upper bool
			want             string
		}{
			{"inactive_lowered_owner", false, false, "call-output"},
			{"scheduled_owner", true, false, "scheduled-value"},
			{"upper_owner", false, true, "call-output"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				node := &setProjectNode{scalarUpper: tc.upper, scalarBindings: []stringSetSlot{{original: lowered, column: reserved, scheduled: tc.scheduled}}}
				expression, changed := stringScalarOwner(node, reserved)
				if !changed {
					t.Fatal("owner mapping did not resolve reserved reference")
				}
				value, err := expression.Eval(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.String(text, "call-output"), sqltype.String(text, "scheduled-value")}})
				if err != nil {
					t.Fatal(err)
				}
				got, err := value.Text()
				if err != nil || got != tc.want {
					t.Fatal("owner read stale reservation or lost lowered output", got, err, tc.want)
				}
			})
		}
	})
	t.Run("surviving_group_ownership", func(t *testing.T) {
		for _, tc := range []struct {
			name, query string
			upper       bool
			rows        [][]any
		}{
			{"erased_wrapper", "SELECT 7,replace(string_to_table('a,b',','),NULL,'x'),string_to_table('c,d',',')", false, [][]any{{int64(7), nil, "c"}, {int64(7), nil, "d"}}},
			{"decreased_depth", "SELECT 7,string_to_table(replace(string_to_table('a,b',','),NULL,'x'),','),string_to_table('c,d',',')", false, [][]any{{int64(7), nil, "c"}, {int64(7), nil, "d"}}},
			{"surviving_wrapper", "SELECT 7,upper(string_to_table('a,b',','))", true, [][]any{{int64(7), "A"}, {int64(7), "B"}}},
			{"zero_depth", "SELECT 7,replace(string_to_table('a,b',','),NULL,'x')", false, [][]any{{int64(7), nil}}},
		} {
			t.Run(tc.name, func(t *testing.T) {
				b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
				b.aggregateClause = "SELECT"
				raw := statement(t, tc.query).(*parser.Select)
				expressions := make([]BoundExpr, len(raw.Targets))
				schema := make(querySchema, len(raw.Targets))
				for i, target := range raw.Targets {
					expression, err := b.bind(target, expressionPolicy{context: projectionContext})
					if err != nil {
						t.Fatal(err)
					}
					expressions[i], schema[i] = expression, ColumnDescriptor{Usage: expression.ResultUsage()}
				}
				child, err := b.lowerStringSets(&singletonNode{}, expressions, nil, true)
				if err != nil {
					t.Fatal(err)
				}
				retained := child.(*setProjectNode)
				callSlots := append([]int(nil), retained.callSlots...)
				steps := append([]stringSetStep(nil), retained.steps...)
				templateSteps := append([]stringSetStep(nil), retained.template.steps...)
				owner := &projectNode{querySchema: schema, child: child, expressions: expressions, frame: 1, hasTargetStringSet: true}
				for range 3 {
					private, err := preflightQuery(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, owner)
					if err != nil {
						t.Fatal(err)
					}
					project := private.(*projectNode)
					stage := project.child.(*setProjectNode)
					if _, nested := stage.child.(*setProjectNode); nested {
						t.Fatal("obsolete deeper expansion stage survived simplification")
					}
					_, cached := project.expressions[0].(columnExpression)
					if cached == tc.upper {
						t.Fatal("scalar owner does not match surviving maximum depth", project.expressions, stage.scalars)
					}
					var trace []int
					if tc.name == "decreased_depth" {
						evaluation := &EvalContext{Context: context.Background(), Scope: 1, Frame: 1}
						value, err := stage.scalars[0].Eval(evaluation)
						if err != nil {
							t.Fatal(err)
						}
						stage.scalars[0] = mathObservedArgument{expressionInfo: boundExpressionInfo(stage.scalars[0]), value: value, order: &trace, index: 0}
						for i, call := range stage.calls {
							value, err := call.args[0].Eval(evaluation)
							if err != nil {
								t.Fatal(err)
							}
							event := 1 // Outer target's NULL source precedes the live peer.
							if !value.IsNull() {
								event = 2
							}
							stage.calls[i].args[0] = mathObservedArgument{expressionInfo: boundExpressionInfo(call.args[0]), value: value, order: &trace, index: event}
						}
					}
					executable, err := openQueryNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, private)
					if err != nil {
						t.Fatal(err)
					}
					var rows [][]any
					values := make([]sqltype.Value, len(raw.Targets))
					for {
						present, err := executable.nextProjected(context.Background(), values)
						if err != nil {
							executable.Close()
							t.Fatal(err)
						}
						if !present {
							break
						}
						row := make([]any, len(values))
						for i, value := range values {
							if value.IsNull() {
								continue
							}
							if i == 0 {
								row[i], err = value.Integer()
							} else {
								row[i], err = value.Text()
							}
							if err != nil {
								executable.Close()
								t.Fatal(err)
							}
						}
						rows = append(rows, row)
					}
					executable.Close()
					if !reflect.DeepEqual(rows, tc.rows) {
						t.Fatal("surviving expansion changed projected values", rows, tc.rows)
					}
					if tc.name == "decreased_depth" && !reflect.DeepEqual(trace, []int{0, 1, 2, 0, 0}) {
						t.Fatal("decreased-depth expansion reordered target effects or reopened arguments", trace)
					}
					if retained.template == nil || !reflect.DeepEqual(retained.callSlots, callSlots) || !reflect.DeepEqual(retained.steps, steps) || !reflect.DeepEqual(retained.template.steps, templateSteps) || !owner.hasTargetStringSet {
						t.Fatal("preflight mutated analyzed group")
					}
				}
			})
		}
	})
	for _, peer := range []bool{false, true} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.aggregateClause = "SELECT"
		query := "SELECT replace(string_to_table('a,b',','),NULL,'x') AS v"
		if peer {
			query += ",string_to_table('c,d,e',',') AS w"
		}
		raw := statement(t, query).(*parser.Select)
		projection := make([]BoundExpr, len(raw.Targets))
		schema := make(querySchema, len(raw.Targets))
		for i, target := range raw.Targets {
			expr, err := b.bind(target, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			projection[i], schema[i] = expr, ColumnDescriptor{Usage: expr.ResultUsage()}
		}
		requests := projection
		if peer {
			requests = []BoundExpr{projection[1]}
		}
		requests, templates := stringSetPeerClosure(requests, [][]BoundExpr{projection})
		lowered, err := b.lowerStringSets(&singletonNode{}, requests, [][]BoundExpr{projection}, false, templates)
		if err != nil {
			t.Fatal(err)
		}
		retained := lowered.(*setProjectNode)
		root := &projectNode{querySchema: schema, child: lowered, expressions: projection, frame: 1, hasTargetStringSet: true}
		for range 3 {
			ctx, cancel := context.WithCancel(context.Background())
			evaluation := &EvalContext{Context: ctx, Scope: 1, Frame: 1}
			private, err := preflightQuery(evaluation, root)
			if err != nil {
				t.Fatal(err)
			}
			executable := private.(*projectNode).child.(*setProjectNode)
			wantCalls, wantRows := 0, 1
			if peer {
				wantCalls, wantRows = 1, 3
			}
			if executable == retained || executable.template != nil || len(executable.calls) != wantCalls ||
				retained.template == nil || len(retained.calls) != len(raw.Targets) || len(retained.callSlots) != len(retained.calls) {
				t.Fatal("preflight mutated retained level template")
			}
			state := &queryState{source: &querySource{}, frame: 1}
			stage, err := openQueryNode(ctx, state, private)
			if err != nil {
				t.Fatal(err)
			}
			values := make([]sqltype.Value, len(raw.Targets))
			count := 0
			for {
				present, err := stage.nextProjected(ctx, values)
				if err != nil {
					t.Fatal(err)
				}
				if !present {
					break
				}
				if !values[0].IsNull() {
					t.Fatal("strict wrapper was not NULL", values[0])
				}
				if peer {
					got, err := values[1].Text()
					if err != nil || count >= 3 || got != []string{"c", "d", "e"}[count] {
						t.Fatal("lost surviving peer slot", got, err)
					}
				}
				count++
			}
			if count != wantRows {
				t.Fatal("incorrect re-lowered cardinality", count, wantRows)
			}
			stage.Close()
			cancel()
		}
	}
}

func TestStringSplitPreflightSiblingError(t *testing.T) {
	b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
	b.aggregateClause = "SELECT"
	raw := statement(t, "SELECT replace(string_to_table('a,b',','),NULL,split_part('x',',',0))").(*parser.Select)
	expr, err := b.bind(raw.Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal("immutable sibling error moved into binding", err)
	}
	projection := []BoundExpr{expr}
	root, err := b.lowerStringSets(&singletonNode{}, projection, nil, true)
	if err != nil {
		t.Fatal("immutable sibling error moved into lowering", err)
	}
	retained := root.(*setProjectNode)
	_, err = preflightQuery(&EvalContext{Context: context.Background(), Scope: 1, Frame: 1}, retained)
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22023" {
		t.Fatal("strict NULL suppressed immutable sibling error", err)
	}
	if retained.template == nil || len(retained.calls) != 1 || len(retained.callSlots) != 1 || retained.callSlots[0] != 0 {
		t.Fatal("failed preflight mutated retained level")
	}
}
