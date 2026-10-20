package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"sync"
	"testing"
)

func stringSplitMarker(t *testing.T, b *expressionBinder, pos int, args ...expr.Expr) functions.String {
	t.Helper()
	expr, err := b.stringNode("string_to_table", b.typ(sqltype.Text).Usage(), parser.Position{Offset: pos, Character: pos + 1}, args, [4]bool{true, true, true}, parser.OrdinaryStringSyntax)
	if err != nil {
		t.Fatal(err)
	}
	return expr.(functions.String)
}
func stringSplitLiteral(b *expressionBinder, s string) expr.Expr {
	text := b.typ(sqltype.Text)
	return literalExpression{Info: expr.Info{Usage: text.Usage()}, Value: sqltype.String(text, s)}
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
					binder.clause.aggregateClause = "SELECT"
					raw := statement(t, query).(*parser.Select)
					requests := make([]expr.Expr, len(raw.Targets))
					for i, target := range raw.Targets {
						var err error
						requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
						if err != nil {
							t.Fatal(err)
						}
					}
					root, err := binder.lowerQuerySets(&singletonNode{}, requests, nil, true)
					if err != nil {
						t.Fatal(err)
					}
					node := root.(*setProjectNode)
					text := binder.typ(sqltype.Text)
					var observed []int
					node.Scalars[0] = mathObservedArgument{Info: expr.InfoOf(node.Scalars[0]), value: sqltype.String(text, "s"), order: &observed, index: 0, err: scenario.scalarError}
					functionSetUnitArgument(t, node.Calls, 0, 0, mathObservedArgument{Info: expr.InfoOf(expr.Expr(node.Calls[0]).(expr.Composite).Children()[0]), value: sqltype.String(text, scenario.input), order: &observed, index: 1, err: scenario.sourceError})
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
					binder.clause.aggregateClause = "SELECT"
					shallow := "'p'||string_to_table('x',',')"
					deep := "string_to_table(string_to_table('" + scenario.input + "',','),':')"
					query := "SELECT " + shallow + "," + deep
					if !scalarFirst {
						query = "SELECT " + deep + "," + shallow
					}
					raw := statement(t, query).(*parser.Select)
					requests := make([]expr.Expr, len(raw.Targets))
					schema := make(querySchema, len(raw.Targets))
					for i, target := range raw.Targets {
						var err error
						requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
						if err != nil {
							t.Fatal(err)
						}
						schema[i] = catalog.Column{Usage: requests[i].ResultUsage()}
					}
					root, err := binder.lowerQuerySets(&singletonNode{}, requests, nil, true)
					if err != nil {
						t.Fatal(err)
					}
					owner := &projectNode{querySchema: schema, Child: root, Expressions: requests, Frame: 1, HasTargetSet: true}
					private, err := preflightQuery(&EvalContext{evalStatement: evalStatement{Context: context.Background(), catalog: binder.bindingCatalog()}, evalFrame: evalFrame{Scope: 1, Frame: 1}}, owner)
					if err != nil {
						t.Fatal(err)
					}
					project := private.(*projectNode)
					expansion := project.Child.(*setProjectNode)
					for _, scalar := range expansion.Scalars {
						if querySetDepth(scalar) != 0 || scalar.ResultUsage().Type == nil {
							t.Fatal("effective scalar retained an executable set marker or unresolved usage", scalar)
						}
					}
					var observed []int
					instrument := func(expression expr.Expr) (expr.Expr, error) {
						next, _, err := mapBoundExpression(binder.bindingCatalog(), expression,
							func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ },
							func(query LogicalNode) (LogicalNode, error) { return query, nil },
							nil,
							func(expression expr.Expr) (expr.Expr, bool, error) {
								literal, ok := expression.(literalExpression)
								if !ok || literal.Value.IsNull() {
									return expression, false, nil
								}
								value, err := literal.Value.Text()
								if err != nil || value != "p" && value != ":" {
									return expression, false, nil
								}
								index, failure := 0, scenario.scalarError
								if value == ":" {
									index, failure = 1, scenario.sourceError
								}
								info := expr.InfoOf(expression)
								info.Property.Volatility = expr.Volatile
								return mathObservedArgument{Info: info, value: literal.Value, order: &observed, index: index, err: failure}, true, nil
							})
						return next, err
					}
					for i, expression := range project.Expressions {
						project.Expressions[i], err = instrument(expression)
						if err != nil {
							t.Fatal(err)
						}
					}
					for i, expression := range expansion.Scalars {
						expansion.Scalars[i], err = instrument(expression)
						if err != nil {
							t.Fatal(err)
						}
					}
					for i, call := range expansion.Calls {
						for j, argument := range expr.Expr(call).(expr.Composite).Children() {
							var next expr.Expr
							next, err = instrument(argument)
							if err != nil {
								t.Fatal(err)
							}
							functionSetUnitArgument(t, expansion.Calls, i, j, next)
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
		binder.clause.aggregateClause = "SELECT"
		raw := statement(t, "SELECT 's',string_to_table('a,b',','),'t',string_to_table('x',',')").(*parser.Select)
		requests := make([]expr.Expr, len(raw.Targets))
		for i, target := range raw.Targets {
			var err error
			requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
		}
		root, err := binder.lowerQuerySets(&singletonNode{}, requests, nil, true)
		if err != nil {
			t.Fatal(err)
		}
		node := root.(*setProjectNode)
		trace := []int{}
		for i, value := range []string{"s", "t"} {
			node.Scalars[i] = mathObservedArgument{Info: expr.Info{Usage: node.Scalars[i].ResultUsage(), Property: expr.Properties{Volatility: expr.Volatile}}, value: sqltype.String(node.Scalars[i].ResultUsage().Type, value), order: &trace, index: i}
		}
		for i, value := range []string{"a,b", "x"} {
			argument := expr.Expr(node.Calls[i]).(expr.Composite).Children()[0]
			functionSetUnitArgument(t, node.Calls, i, 0, mathObservedArgument{Info: expr.Info{Usage: argument.ResultUsage(), Property: expr.Properties{Volatility: expr.Volatile}}, value: sqltype.String(argument.ResultUsage().Type, value), order: &trace, index: 10 + i})
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
	input := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, "a,b"), order: &order, index: 0}
	delimiter := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.String(text, ","), order: &order, index: 1}
	marker := mathObservedArgument{Info: expr.Info{Usage: text.Usage()}, value: sqltype.Null(text), order: &order, index: 2}
	first := stringSplitMarker(t, b, 1, input, delimiter, marker)
	second := stringSplitMarker(t, b, 2, stringSplitLiteral(b, "x"), stringSplitLiteral(b, ","))
	scalar := mathObservedArgument{Info: expr.Info{Usage: text.Usage(), Property: expr.Properties{Volatility: expr.Volatile}}, value: sqltype.String(text, "s"), order: &order, index: 3}
	node := &setProjectNode{Child: &singletonNode{}, Calls: []expr.Expr{first, second}, CallSlots: []int{0, 1}, MarkerWidth: 2, Scalars: []expr.Expr{scalar}, ScalarSlots: []int{0}, Steps: []querySetStep{{Kind: querySetCallStep, Index: 0}, {Kind: querySetCallStep, Index: 1}, {Kind: querySetScalarStep, Index: 0}}, Frame: 1, querySchema: querySchema{{Usage: text.Usage()}, {Usage: text.Usage()}, {Usage: text.Usage()}}}
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
				private.CallSlots = tc.slots
				if _, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil || err.Error() != tc.message {
					t.Fatal("invalid fixed marker layout admitted", err)
				}
			})
		}
	})
	t.Run("scalar_slot_invariants", func(t *testing.T) {
		for _, slots := range [][]int{{}, {-1}, {1}} {
			private := *node
			private.ScalarSlots = slots
			if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
				stage.Close()
				t.Fatal("invalid explicit scalar slot admitted", slots)
			}
		}
		private := *node
		private.querySchema = append(append(querySchema(nil), node.querySchema...), catalog.Column{Usage: scalar.ResultUsage()})
		private.Scalars = []expr.Expr{scalar, scalar}
		private.ScalarSlots = []int{0, 0}
		if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
			stage.Close()
			t.Fatal("duplicate scalar slot admitted")
		}
		private = *node
		private.Scalars = []expr.Expr{first}
		if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
			stage.Close()
			t.Fatal("unlowered marker admitted as scalar event")
		}
	})
	t.Run("invalid_target_schedule", func(t *testing.T) {
		for _, steps := range [][]querySetStep{
			nil,
			{{Kind: querySetStepKind(255), Index: 0}, {Kind: querySetCallStep, Index: 1}, {Kind: querySetScalarStep, Index: 0}},
			{{Kind: querySetCallStep, Index: -1}, {Kind: querySetCallStep, Index: 1}, {Kind: querySetScalarStep, Index: 0}},
			{{Kind: querySetCallStep, Index: 2}, {Kind: querySetCallStep, Index: 1}, {Kind: querySetScalarStep, Index: 0}},
			{{Kind: querySetCallStep, Index: 0}, {Kind: querySetCallStep, Index: 0}, {Kind: querySetScalarStep, Index: 0}},
			{{Kind: querySetCallStep, Index: 0}, {Kind: querySetCallStep, Index: 1}, {Kind: querySetScalarStep, Index: 1}},
		} {
			private := *node
			private.Steps = steps
			if stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, &private); err == nil {
				stage.Close()
				t.Fatal("malformed target schedule admitted", steps)
			}
		}
		if err := validateQuerySetStageOrder(
			[]querySetStep{{Kind: querySetCallStep, Index: 0}, {Kind: querySetScalarStep, Index: 0}, {Kind: querySetCallStep, Index: 1}},
			node.Steps, node.CallSlots, node.ScalarSlots, len(node.Scalars)); err == nil {
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
	if first.Plan.Name() != "string_to_table" || !first.Properties().StageDependent || conditionalConstantInputs(first) {
		t.Fatal("lost set marker", first.Properties())
	}
	if _, err := first.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}); err == nil {
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
			binder.clause.aggregateClause = "SELECT"
			raw := statement(t, query).(*parser.Select)
			requests := make([]expr.Expr, len(raw.Targets))
			schema := make(querySchema, len(raw.Targets))
			for i, target := range raw.Targets {
				var err error
				requests[i], err = binder.bind(target, expressionPolicy{context: projectionContext})
				if err != nil {
					t.Fatal(err)
				}
				schema[i] = catalog.Column{Usage: requests[i].ResultUsage()}
			}
			root, err := binder.lowerQuerySets(&singletonNode{}, requests, nil, true)
			if err != nil {
				t.Fatal(err)
			}
			owner := &projectNode{querySchema: schema, Child: root, Expressions: requests, Frame: 1, HasTargetSet: true}
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
	b.query.frame = 7
	inner := stringSplitMarker(t, b, 11, stringSplitLiteral(b, "a:b,c:d"), stringSplitLiteral(b, ","))
	outer := stringSplitMarker(t, b, 12, inner, stringSplitLiteral(b, ":"))
	peer := stringSplitMarker(t, b, 13, stringSplitLiteral(b, "x,y,z"), stringSplitLiteral(b, ","))
	projection := []expr.Expr{outer, peer}
	companion := []expr.Expr{peer}
	root, err := b.lowerQuerySets(&singletonNode{}, projection, [][]expr.Expr{companion}, true)
	if err != nil {
		t.Fatal(err)
	}
	upper := root.(*setProjectNode)
	lower := upper.Child.(*setProjectNode)
	if len(lower.Calls) != 2 || len(upper.Calls) != 1 || len(upper.Schema()) != 3 || querySetDepth(projection[0]) != 0 || querySetDepth(projection[1]) != 0 {
		t.Fatal("wrong expansion levels", lower, upper)
	}
	if projection[1].(columnExpression).Slot != companion[0].(columnExpression).Slot || projection[0].(columnExpression).Frame != 7 {
		t.Fatal("lost alias/frame occurrence")
	}
	stage, err := openSetProjectNode(context.Background(), &queryState{source: &querySource{}, frame: 7}, upper)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	var rows [][]string
	evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 7}}
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
	b.clause.aggregateClause = "SELECT"
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
		n := mapped.(functions.String)
		if n.Plan.Name() == "string_to_table" {
			if conditionalConstantInputs(n) || !n.Properties().StageDependent {
				t.Fatal("foldable set marker")
			}
			continue
		}
		if _, err := n.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}); err != nil {
			t.Fatal(q, err)
		}
	}
	b.clause.stringSetGuard = "CASE"
	b.clause.queryHasTargetSet = true
	restore := b.enterQuery(false)
	if b.clause.stringSetGuard != "" || b.clause.queryHasTargetSet {
		t.Fatal("query guard/flag crossed boundary")
	}
	restore()
	if b.clause.stringSetGuard != "CASE" || !b.clause.queryHasTargetSet {
		t.Fatal("query guard/flag not restored")
	}
	b.clause.queryHasTargetSet = false
	child := b.enterQuery(false)
	b.clause.queryHasTargetSet = true
	child()
	if b.clause.queryHasTargetSet {
		t.Fatal("child query set flag escaped owner")
	}
	b.clause.stringSetGuard = ""
	raw := statement(t, "SELECT replace(string_to_table('a,b',','),NULL,'x')").(*parser.Select)
	bound, err := b.bind(raw.Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal(err)
	}
	folded, _, err := simplifyConditional(&EvalContext{evalStatement: evalStatement{Context: context.Background()}}, bound, false)
	if err != nil {
		t.Fatal(err)
	}
	literal, ok := folded.(literalExpression)
	if !ok || !literal.Value.IsNull() || !b.clause.queryHasTargetSet {
		t.Fatal("lost original set analysis after strict folding", folded)
	}
	descriptor := &catalog.Relation{View: &viewDefinition{syntax: raw, query: &BoundQuery{Root: &projectNode{Child: &singletonNode{}, Expressions: []expr.Expr{folded}, HasTargetSet: b.clause.queryHasTargetSet}}}}
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
			return &setProjectNode{Child: &singletonNode{}, Calls: []expr.Expr{call}, CallSlots: []int{0},
				MarkerWidth: 1, Frame: frame, querySchema: querySchema{{Usage: call.ResultUsage()}}, Steps: []querySetStep{{Kind: querySetCallStep, Index: 0}},
				Template: &querySetTemplate{Group: &frame, Expressions: []expr.Expr{analysis, call}, Origins: []expr.Expr{call}, Attempts: true, Steps: []querySetStep{{Kind: querySetCallStep, Index: 0}}}}
		}
		for _, kind := range []parser.ValueKind{parser.NullValue, parser.StringValue} {
			stage := makeStage(unknownOutput{Raw: parser.LiteralExpr{Value: parser.Value{Kind: kind, Text: "retained"}}})
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err != nil {
				t.Fatal("genuine contextual template root rejected", err)
			}
			for range 2 {
				mapped, err := remapViewQuery(b.bindingCatalog(), stage, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
				if err != nil {
					t.Fatal(err)
				}
				if mapped.(*setProjectNode).Template.Group != stage.Template.Group {
					t.Fatal("capture changed local-group identity")
				}
				if !reflect.DeepEqual(mapped.(*setProjectNode).Steps, []querySetStep{{Kind: querySetCallStep, Index: 0}}) ||
					!reflect.DeepEqual(mapped.(*setProjectNode).Template.Steps, []querySetStep{{Kind: querySetCallStep, Index: 0}}) {
					t.Fatal("capture changed target schedule")
				}
			}
		}
		for _, program := range [][]querySetStep{nil, {{Kind: querySetStepKind(255), Index: 0}}, {{Kind: querySetCallStep, Index: 1}}} {
			stage := makeStage(unknownOutput{Raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}})
			stage.Steps = program
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
				t.Fatal("stored executable schedule admitted malformed event", program)
			}
			stage.Steps = []querySetStep{{Kind: querySetCallStep, Index: 0}}
			stage.Template.Steps = program
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
				t.Fatal("stored template schedule admitted malformed event", program)
			}
		}
		for _, malformed := range []unknownOutput{
			{Raw: parser.ColumnExpr{}},
			{Raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.IntegerValue, Integer: 1}}},
			{Info: expr.Info{Usage: call.ResultUsage()}, Raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}},
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
		program := []querySetStep{{Kind: querySetScalarStep, Index: 0}, {Kind: querySetCallStep, Index: 0}}
		makeStage := func() *setProjectNode {
			return &setProjectNode{Child: &singletonNode{}, Frame: frame, Calls: []expr.Expr{call}, CallSlots: []int{0},
				MarkerWidth: 1, Scalars: []expr.Expr{scalar}, Steps: program,
				querySchema: querySchema{{Usage: call.ResultUsage()}, {Usage: scalar.ResultUsage()}},
				Template: &querySetTemplate{Group: &frame, Expressions: []expr.Expr{scalar, call}, Origins: []expr.Expr{call},
					Scalars: []expr.Expr{scalar}, ScalarRequests: []int{0}, Attempts: true, Steps: program}}
		}
		stage := makeStage()
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err != nil {
			t.Fatal("valid identity scalar reservation rejected", err)
		}
		for range 2 {
			mapped, err := remapViewQuery(b.bindingCatalog(), stage, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(typ *sqltype.Descriptor) *sqltype.Descriptor { return typ })
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(mapped.(*setProjectNode).ScalarSlots, stage.ScalarSlots) || !reflect.DeepEqual(mapped.(*setProjectNode).Template.ScalarRequests, stage.Template.ScalarRequests) {
				t.Fatal("capture changed scalar candidate identity")
			}
		}
		for _, slots := range [][]int{{}, {-1}, {1}} {
			stage := makeStage()
			stage.ScalarSlots = slots
			if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
				t.Fatal("malformed stored explicit scalar reservation admitted", slots)
			}
		}
		stage = makeStage()
		stage.Template.ScalarRequests = []int{1}
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: stage}}); err == nil {
			t.Fatal("scalar candidate mapped to a call owner")
		}
	})
	t.Run("contextual_view_analysis", func(t *testing.T) {
		value := stringSplitLiteral(b, "kept")
		unknown := unknownOutput{Raw: parser.LiteralExpr{Value: parser.Value{Kind: parser.NullValue}}}
		mapped, changed, err := retagBoundTypes(New().CatalogRead(), unknown, func(*sqltype.Descriptor) *sqltype.Descriptor {
			t.Fatal("contextual analysis invented a descriptor to retag")
			return nil
		})
		if err != nil || changed || !reflect.DeepEqual(mapped, unknown) {
			t.Fatal("contextual analysis changed during retag", mapped, changed, err)
		}
		captured, err := remapBoundExpression(unknown, func(ref ids.ObjectRef) ids.ObjectRef {
			t.Fatal("contextual analysis invented a captured reference", ref)
			return ref
		}, nil)
		if err != nil || !reflect.DeepEqual(captured, unknown) {
			t.Fatal("contextual analysis changed during capture", captured, err)
		}
		if _, err := remapBoundExpression(unknownOutput{Raw: parser.ColumnExpr{}}, nil, nil); err == nil || err.Error() != "stuntgres: invalid contextual view output" {
			t.Fatal("nonliteral contextual capture accepted", err)
		}
		project := &projectNode{
			querySchema:         querySchema{{Usage: value.ResultUsage()}},
			Child:               &singletonNode{},
			Expressions:         []expr.Expr{value},
			AnalysisExpressions: []expr.Expr{unknown},
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
		private.Expressions = []expr.Expr{unknown}
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: &private}}); err == nil || err.Error() != "stuntgres: invalid view query usage" {
			t.Fatal("unresolved executable output accepted", err)
		}
		private = *project
		private.AnalysisExpressions = []expr.Expr{unknownOutput{Raw: parser.ColumnExpr{}}}
		if err := validateViewLeaves(b.bindingCatalog(), &viewDefinition{query: &BoundQuery{Root: &private}}); err == nil || err.Error() != "stuntgres: invalid contextual view output" {
			t.Fatal("nonliteral contextual placeholder accepted", err)
		}
	})
	call := stringSplitMarker(t, b, 1, stringSplitLiteral(b, "a,b"), stringSplitLiteral(b, ","))
	node := &functionFromNode{Calls: []expr.Expr{call}, querySchema: querySchema{{Usage: call.ResultUsage()}}, Frame: 1}
	open := func() (indexedStage, error) {
		return openFunctionFromNode(context.Background(), &queryState{source: &querySource{}, frame: 1}, node)
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
		info := expr.Info{Usage: text.Usage()}
		lowered := columnExpression{Info: info, Scope: 1, Frame: 1, Slot: 0}
		reserved := columnExpression{Info: info, Scope: 1, Frame: 1, Slot: 1}
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
				node := &setProjectNode{ScalarUpper: tc.upper, ScalarBindings: []querySetSlot{{Original: lowered, Column: reserved, Scheduled: tc.scheduled}}}
				expression, changed := queryScalarOwner(node, reserved)
				if !changed {
					t.Fatal("owner mapping did not resolve reserved reference")
				}
				value, err := expression.Eval(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1, Row: []sqltype.Value{sqltype.String(text, "call-output"), sqltype.String(text, "scheduled-value")}}})
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
				b.clause.aggregateClause = "SELECT"
				raw := statement(t, tc.query).(*parser.Select)
				expressions := make([]expr.Expr, len(raw.Targets))
				schema := make(querySchema, len(raw.Targets))
				for i, target := range raw.Targets {
					expression, err := b.bind(target, expressionPolicy{context: projectionContext})
					if err != nil {
						t.Fatal(err)
					}
					expressions[i], schema[i] = expression, catalog.Column{Usage: expression.ResultUsage()}
				}
				child, err := b.lowerQuerySets(&singletonNode{}, expressions, nil, true)
				if err != nil {
					t.Fatal(err)
				}
				retained := child.(*setProjectNode)
				callSlots := append([]int(nil), retained.CallSlots...)
				steps := append([]querySetStep(nil), retained.Steps...)
				templateSteps := append([]querySetStep(nil), retained.Template.Steps...)
				owner := &projectNode{querySchema: schema, Child: child, Expressions: expressions, Frame: 1, HasTargetSet: true}
				for range 3 {
					private, err := preflightQuery(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1}}, owner)
					if err != nil {
						t.Fatal(err)
					}
					project := private.(*projectNode)
					stage := project.Child.(*setProjectNode)
					if _, nested := stage.Child.(*setProjectNode); nested {
						t.Fatal("obsolete deeper expansion stage survived simplification")
					}
					_, cached := project.Expressions[0].(columnExpression)
					if cached == tc.upper {
						t.Fatal("scalar owner does not match surviving maximum depth", project.Expressions, stage.Scalars)
					}
					var trace []int
					if tc.name == "decreased_depth" {
						evaluation := &EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
						value, err := stage.Scalars[0].Eval(evaluation)
						if err != nil {
							t.Fatal(err)
						}
						stage.Scalars[0] = mathObservedArgument{Info: expr.InfoOf(stage.Scalars[0]), value: value, order: &trace, index: 0}
						for i, call := range stage.Calls {
							value, err := expr.Expr(call).(expr.Composite).Children()[0].Eval(evaluation)
							if err != nil {
								t.Fatal(err)
							}
							event := 1 // Outer target's NULL source precedes the live peer.
							if !value.IsNull() {
								event = 2
							}
							functionSetUnitArgument(t, stage.Calls, i, 0, mathObservedArgument{Info: expr.InfoOf(expr.Expr(call).(expr.Composite).Children()[0]), value: value, order: &trace, index: event})
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
					if retained.Template == nil || !reflect.DeepEqual(retained.CallSlots, callSlots) || !reflect.DeepEqual(retained.Steps, steps) || !reflect.DeepEqual(retained.Template.Steps, templateSteps) || !owner.HasTargetSet {
						t.Fatal("preflight mutated analyzed group")
					}
				}
			})
		}
	})
	for _, peer := range []bool{false, true} {
		b := newTestExpressionBinder(t, context.Background(), New(), sqltype.InputEnvironment{}, nil)
		b.clause.aggregateClause = "SELECT"
		query := "SELECT replace(string_to_table('a,b',','),NULL,'x') AS v"
		if peer {
			query += ",string_to_table('c,d,e',',') AS w"
		}
		raw := statement(t, query).(*parser.Select)
		projection := make([]expr.Expr, len(raw.Targets))
		schema := make(querySchema, len(raw.Targets))
		for i, target := range raw.Targets {
			expr, err := b.bind(target, expressionPolicy{context: projectionContext})
			if err != nil {
				t.Fatal(err)
			}
			projection[i], schema[i] = expr, catalog.Column{Usage: expr.ResultUsage()}
		}
		requests := projection
		if peer {
			requests = []expr.Expr{projection[1]}
		}
		requests, templates := querySetPeerClosure(requests, [][]expr.Expr{projection})
		lowered, err := b.lowerQuerySets(&singletonNode{}, requests, [][]expr.Expr{projection}, false, templates)
		if err != nil {
			t.Fatal(err)
		}
		retained := lowered.(*setProjectNode)
		root := &projectNode{querySchema: schema, Child: lowered, Expressions: projection, Frame: 1, HasTargetSet: true}
		for range 3 {
			ctx, cancel := context.WithCancel(context.Background())
			evaluation := &EvalContext{evalStatement: evalStatement{Context: ctx}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
			private, err := preflightQuery(evaluation, root)
			if err != nil {
				t.Fatal(err)
			}
			executable := private.(*projectNode).Child.(*setProjectNode)
			wantCalls, wantRows := 0, 1
			if peer {
				wantCalls, wantRows = 1, 3
			}
			if executable == retained || executable.Template != nil || len(executable.Calls) != wantCalls ||
				retained.Template == nil || len(retained.Calls) != len(raw.Targets) || len(retained.CallSlots) != len(retained.Calls) {
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
	b.clause.aggregateClause = "SELECT"
	raw := statement(t, "SELECT replace(string_to_table('a,b',','),NULL,split_part('x',',',0))").(*parser.Select)
	bound, err := b.bind(raw.Targets[0], expressionPolicy{context: projectionContext})
	if err != nil {
		t.Fatal("immutable sibling error moved into binding", err)
	}
	projection := []expr.Expr{bound}
	root, err := b.lowerQuerySets(&singletonNode{}, projection, nil, true)
	if err != nil {
		t.Fatal("immutable sibling error moved into lowering", err)
	}
	retained := root.(*setProjectNode)
	_, err = preflightQuery(&EvalContext{evalStatement: evalStatement{Context: context.Background()}, evalFrame: evalFrame{Scope: 1, Frame: 1}}, retained)
	var diagnostic interface{ SQLState() string }
	if !errors.As(err, &diagnostic) || diagnostic.SQLState() != "22023" {
		t.Fatal("strict NULL suppressed immutable sibling error", err)
	}
	if retained.Template == nil || len(retained.Calls) != 1 || len(retained.CallSlots) != 1 || retained.CallSlots[0] != 0 {
		t.Fatal("failed preflight mutated retained level")
	}
}
