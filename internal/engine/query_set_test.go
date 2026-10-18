package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func querySetUnitFixture(t *testing.T, operation string, all, zeroWidth bool) (*setNode, *projectNode, *querySource) {
	t.Helper()
	_, query, source := queryCoreFixture(t, "SELECT v FROM t")
	arm := query.Root.(*projectNode)
	copy := *arm
	copy.expressions = append([]BoundExpr(nil), arm.expressions...)
	copy.querySchema = append(querySchema(nil), arm.querySchema...)
	if zeroWidth {
		copy.expressions = []BoundExpr{}
		copy.querySchema = querySchema{}
	}
	schema := append(querySchema(nil), copy.Schema()...)
	for i := range schema {
		schema[i].OriginTable = 0
		schema[i].OriginAttribute = 0
		schema[i].Nullable = false
		schema[i].NullabilityKnown = false
	}
	n := &setNode{querySchema: schema, left: &copy, right: &copy, operation: operation, all: all, leftExpressions: append([]BoundExpr(nil), copy.expressions...), rightExpressions: append([]BoundExpr(nil), copy.expressions...)}
	if operation != "union" || !all {
		n.grouping = make([]queryGrouping, len(schema))
		for i := range schema {
			var err error
			n.grouping[i].Grouping, err = sqltype.BindGrouping(schema[i].Usage)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	projection := make([]BoundExpr, len(schema))
	for i := range schema {
		projection[i] = columnExpression{expressionInfo: boundExpressionInfo(copy.expressions[i]), scope: 1, frame: copy.frame, slot: i}
	}
	return n, &projectNode{querySchema: schema, child: n, expressions: projection, frame: copy.frame}, source
}

func TestQuerySetCounts(t *testing.T) {
	for _, tc := range []struct {
		left, right                                                 int
		unionAll, union, intersectAll, intersect, exceptAll, except int
	}{
		{0, 0, 0, 0, 0, 0, 0, 0}, {0, 3, 3, 1, 0, 0, 0, 0}, {3, 0, 3, 1, 0, 0, 3, 1},
		{3, 2, 5, 1, 2, 1, 1, 0}, {2, 3, 5, 1, 2, 1, 0, 0}, {3, 3, 6, 1, 3, 1, 0, 0},
		{7, 3, 10, 1, 3, 1, 4, 0},
	} {
		for _, op := range []struct {
			name string
			all  bool
			want int
		}{
			{"union", true, tc.unionAll}, {"union", false, tc.union}, {"intersect", true, tc.intersectAll},
			{"intersect", false, tc.intersect}, {"except", true, tc.exceptAll}, {"except", false, tc.except},
		} {
			if got := setMultiplicity(op.name, op.all, tc.left, tc.right); got != op.want {
				t.Fatalf("%s ALL=%t m=%d n=%d got%d want%d", op.name, op.all, tc.left, tc.right, got, op.want)
			}
		}
	}
}

func TestQuerySetCollisionAndZeroWidth(t *testing.T) {
	for _, zero := range []bool{false, true} {
		for _, tc := range []struct {
			op           string
			all          bool
			normal, zero int
		}{
			{"union", true, 8, 8}, {"union", false, 3, 1}, {"intersect", true, 4, 4}, {"intersect", false, 3, 1}, {"except", true, 0, 0}, {"except", false, 0, 0},
		} {
			n, project, source := querySetUnitFixture(t, tc.op, tc.all, zero)
			if !zero && n.grouping != nil {
				d := n.Schema()[0].Usage.Type
				one, _ := sqltype.Integer(d, 1)
				two, _ := sqltype.Integer(d, 2)
				if i, err := findDistinctTuple(context.Background(), n.grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0}); err != nil || i != -1 {
					t.Fatal("unequal collision", i, err)
				}
				if i, err := findDistinctTuple(context.Background(), n.grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0, 1}); err != nil || i != 1 {
					t.Fatal("equal collision", i, err)
				}
			}
			rows, count, err := finalizeQuery(context.Background(), project, source, nil)
			want := tc.normal
			if zero {
				want = tc.zero
			}
			if err != nil || count != want {
				t.Fatal(tc, zero, count, err)
			}
			seen := 0
			row := make([]sqltype.Value, len(n.Schema()))
			for {
				present, err := rows.Next(context.Background(), row)
				if err != nil {
					t.Fatal(err)
				}
				if !present {
					break
				}
				seen++
			}
			if seen != want {
				t.Fatal("payload width lost occurrence", seen, want)
			}
			if err := rows.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
}

func TestQuerySetUnionAllDemand(t *testing.T) {
	n, _, source := querySetUnitFixture(t, "union", true, false)
	defer source.close()
	left, right := 0, 0
	l, r := n.leftExpressions[0], n.rightExpressions[0]
	n.leftExpressions[0] = queryProbe(l.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { left++; return l.Eval(c) })
	n.rightExpressions[0] = queryProbe(r.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { right++; return r.Eval(c) })
	state := &queryState{source: source}
	stage, err := openSetNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 || right != 0 || n.grouping != nil {
		t.Fatal("UNION ALL opened/demanded before pull", left, right, n.grouping)
	}
	if _, present, err := stage.nextOccurrence(context.Background()); err != nil || !present {
		t.Fatal(present, err)
	}
	if err := stage.Close(); err != nil {
		t.Fatal(err)
	}
	if left != 1 || right != 0 {
		t.Fatal("unused right arm evaluated", left, right)
	}
}

func TestQuerySetCancellationAndCleanup(t *testing.T) {
	for _, phase := range []string{"left", "right", "late error", "publication"} {
		t.Run(phase, func(t *testing.T) {
			n, project, source := querySetUnitFixture(t, "union", false, false)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			sentinel := errors.New("late set arm failure")
			calls := 0
			expressions := n.leftExpressions
			if phase == "right" {
				expressions = n.rightExpressions
			}
			original := expressions[0]
			expressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
				calls++
				if phase == "left" || phase == "right" {
					cancel()
				}
				if phase == "late error" && calls == 4 {
					return sqltype.Value{}, sentinel
				}
				return original.Eval(c)
			})
			if phase == "publication" {
				original := project.expressions[0]
				project.expressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { cancel(); return original.Eval(c) })
			}
			rows, count, err := finalizeQuery(ctx, project, source, nil)
			want := error(context.Canceled)
			if phase == "late error" {
				want = sentinel
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || source.view != nil {
				t.Fatal(rows, count, err, want, source.view)
			}
		})
	}
}

func TestQuerySetCoercionFrames(t *testing.T) {
	e := New()
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ast := statement(t, "SELECT NULL UNION (SELECT NULL UNION SELECT 1)")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	if err != nil {
		e.mu.Unlock()
		t.Fatal(err)
	}
	b, err := e.describe(op.ctx, ast, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, op, nil, nil)
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project := b.query.Root.(*projectNode)
	n := project.child.(*setNode)
	if n.left.frame == n.right.frame || n.left.frame == project.frame || n.right.frame == project.frame {
		t.Fatal("set arms share frame", project.frame, n.left.frame, n.right.frame)
	}
	frames, queries := collectQueryFrames(project)
	if len(frames) != 5 || len(queries) != 5 {
		t.Fatal("nested arm frame registration", len(frames), len(queries))
	}
	for _, schema := range []querySchema{project.querySchema, n.querySchema, n.left.querySchema, n.right.querySchema} {
		for _, column := range schema {
			if column.Usage.Type.ID() != sqltype.Int4 || column.OriginTable != 0 || column.OriginAttribute != 0 || column.NullabilityKnown {
				t.Fatal(column)
			}
		}
	}
	if n.left.expressions[0].ResultUsage().Type == nil {
		t.Fatal("unknown output escaped parent resolution")
	}
	if canSimplifyExists(b.query) {
		t.Fatal("set presence discarded key demand")
	}
	if _, discard := pruneIdentityQueryProducer(n, project.expressions[0]); discard {
		t.Fatal("identity pruning discarded set key")
	}
}

func TestQuerySetImmutableRewriteRemap(t *testing.T) {
	n, project, source := querySetUnitFixture(t, "intersect", true, false)
	defer source.close()
	original := n.leftExpressions[0]
	replacement := &queryProbeExpression{expressionInfo: boundExpressionInfo(original), eval: func(c *EvalContext) (sqltype.Value, error) { return original.Eval(c) }}
	rewritten, err := rewriteQueryExpressions(project, func(e BoundExpr) (BoundExpr, bool, error) {
		if e == original {
			return replacement, true, nil
		}
		return e, false, nil
	})
	if err != nil || rewritten == project {
		t.Fatal(rewritten, err)
	}
	copy := rewritten.(*projectNode).child.(*setNode)
	if copy == n || n.leftExpressions[0] != original || copy.leftExpressions[0] != replacement {
		t.Fatal("rewrite mutated held binary plan")
	}
	if copy.operation != n.operation || copy.all != n.all || len(copy.grouping) != len(n.grouping) {
		t.Fatal("rewrite lost set contract")
	}
	typ := n.Schema()[0].Usage.Type
	destination := *typ
	mapped, err := remapViewQuery(New().CatalogRead(), project, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == typ {
			return &destination
		}
		return old
	})
	if err != nil {
		t.Fatal(err)
	}
	reminted := mapped.(*projectNode).child.(*setNode)
	if reminted == n || reminted.left == n.left || reminted.right == n.right || n.Schema()[0].Usage.Type != typ {
		t.Fatal("remap mutated held set")
	}
	for _, expression := range []BoundExpr{reminted.leftExpressions[0], reminted.rightExpressions[0]} {
		if expression.ResultUsage().Type != &destination {
			t.Fatal("side recipe kept source-owner type")
		}
	}
	value, err := sqltype.Integer(&destination, 1)
	if err != nil {
		t.Fatal(err)
	}
	key, err := reminted.grouping[0].PrepareKey(context.Background(), value)
	if err != nil || key.Type() != &destination {
		t.Fatal("grouping kept source-owner type", key, err)
	}
	equal, err := reminted.grouping[0].Equal(context.Background(), key, value)
	if err != nil || !equal {
		t.Fatal(equal, err)
	}
}

func TestQuerySetHashCollisionCancellation(t *testing.T) {
	n, project, source := querySetUnitFixture(t, "union", false, false)
	var text *sqltype.Descriptor
	for _, typ := range sqltype.Builtins() {
		if typ.ID() == sqltype.Text {
			text = typ
			break
		}
	}
	usage := text.Usage()
	n.querySchema[0].Usage = usage
	var err error
	n.grouping[0].Grouping, err = sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &aggregateCancelContext{Context: context.Background(), at: 4}
	n.leftExpressions[0] = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
		ctx.armed = true
		return sqltype.String(text, "set cancellation payload"), nil
	})
	rows, count, err := finalizeQuery(ctx, project, source, nil)
	if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.view != nil {
		t.Fatal(rows, count, err)
	}
	zeroCtx := &aggregateCancelContext{Context: context.Background(), armed: true, at: 2}
	if _, err := findDistinctTuple(zeroCtx, []sqltype.Grouping{}, []sqltype.Value{}, []sqltype.Value{}, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal("zero-width collision ignored cancellation", err)
	}
}

func TestQuerySetArmCleanupErrorPriority(t *testing.T) {
	primary, cleanup := errors.New("primary arm failure"), errors.New("arm close failure")
	for _, failure := range []error{primary, nil} {
		closes := 0
		arm := setArm{inner: indexedStage{selection: []int{}, produce: func(context.Context, *indexedStage) (bool, error) { return false, failure }, finish: func() error { closes++; return cleanup }}}
		_, _, err := arm.next(context.Background())
		if failure != nil {
			if err != primary {
				t.Fatal("cleanup replaced primary", err)
			}
			if err := arm.Close(); err != nil {
				t.Fatal(err)
			}
		} else if err := arm.Close(); err != cleanup {
			t.Fatal("successful arm lost required cleanup failure", err)
		}
		if err := arm.Close(); err != nil || closes != 1 {
			t.Fatal("arm closed more than once", closes, err)
		}
	}
}

func TestQuerySetInsertNoBatchOnLateArmFailure(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT id FROM t")
	project := b.query.Root.(*projectNode)
	arm := *project
	arm.expressions = append([]BoundExpr(nil), project.expressions...)
	arm.querySchema = append(querySchema(nil), project.querySchema...)
	n := &setNode{querySchema: arm.querySchema, left: &arm, right: &arm, operation: "union", all: true, leftExpressions: append([]BoundExpr(nil), arm.expressions...), rightExpressions: append([]BoundExpr(nil), arm.expressions...)}
	original := n.rightExpressions[0]
	sentinel := errors.New("late INSERT set arm")
	calls := 0
	n.rightExpressions[0] = queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	project.child = n
	for i, expression := range project.expressions {
		project.expressions[i] = columnExpression{expressionInfo: boundExpressionInfo(expression), scope: 1, frame: project.frame, slot: i}
	}
	rows, err := evaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.values != nil || err != sentinel || source.view != nil || source.anonymous != nil {
		t.Fatal("set INSERT published partial batch", rows, err, source)
	}
}

func TestQuerySetAssignmentSchemaOwnership(t *testing.T) {
	for _, tc := range []struct {
		query  string
		source sqltype.ID
	}{
		{"SELECT 1.1 UNION SELECT 1.2", sqltype.Numeric},
		{"SELECT 1.1 UNION ALL SELECT 1.2 ORDER BY 1 LIMIT 2", sqltype.Numeric},
		{"SELECT 1 UNION ALL SELECT 2147483648::BIGINT", sqltype.Int8},
	} {
		t.Run(tc.query, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE set_dest(n INT)")
			op, done, err := testOperation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ast := statement(t, "INSERT INTO set_dest "+tc.query).(*parser.Insert)
			e.mu.Lock()
			auth, err := e.authorizationLocked(op)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			ref, err := e.resolveReference(auth, ast.Name)
			if err != nil {
				e.mu.Unlock()
				t.Fatal(err)
			}
			bound, err := e.describe(op.ctx, ast, auth, ref, sqltype.InputEnvironment{}, op, nil, nil)
			e.mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			project := bound.query.Root.(*projectNode)
			if project.Schema()[0].Usage.Type.ID() != sqltype.Int4 || project.expressions[0].ResultUsage().Type.ID() != sqltype.Int4 {
				t.Fatal("destination projection is not assigned INT4", project.Schema())
			}
			node := project.child
			for {
				if node.Schema()[0].Usage.Type.ID() != tc.source {
					t.Fatal("assignment changed source schema", node.Schema(), tc.source)
				}
				if _, ok := node.(*setNode); ok {
					break
				}
				child, sibling := queryInputs(node)
				if child == nil || sibling != nil {
					t.Fatal("unexpected set tail", node)
				}
				node = child
			}
		})
	}
}
