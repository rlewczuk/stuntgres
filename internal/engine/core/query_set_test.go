package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/core"
	"stuntgres/internal/engine/dml"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/engine/stored"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func querySetUnitFixture(t *testing.T, operation string, all, zeroWidth bool) (*plan.SetNode, *plan.ProjectNode, *exec.Source) {
	t.Helper()
	_, query, source := queryCoreFixture(t, "SELECT v FROM t")
	arm := query.Root.(*plan.ProjectNode)
	copy := *arm
	copy.Expressions = append([]expr.Expr(nil), arm.Expressions...)
	copy.QuerySchema = append(plan.QuerySchema(nil), arm.QuerySchema...)
	if zeroWidth {
		copy.Expressions = []expr.Expr{}
		copy.QuerySchema = plan.QuerySchema{}
	}
	schema := append(plan.QuerySchema(nil), copy.Schema()...)
	for i := range schema {
		schema[i].OriginTable = 0
		schema[i].OriginAttribute = 0
		schema[i].Nullable = false
		schema[i].NullabilityKnown = false
	}
	n := &plan.SetNode{QuerySchema: schema, Left: &copy, Right: &copy, Operation: operation, All: all, LeftExpressions: append([]expr.Expr(nil), copy.Expressions...), RightExpressions: append([]expr.Expr(nil), copy.Expressions...)}
	if operation != "union" || !all {
		n.Grouping = make([]plan.Grouping, len(schema))
		for i := range schema {
			var err error
			n.Grouping[i].Grouping, err = sqltype.BindGrouping(schema[i].Usage)
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	projection := make([]expr.Expr, len(schema))
	for i := range schema {
		projection[i] = plan.ColumnExpr{Info: expr.InfoOf(copy.Expressions[i]), Scope: 1, Frame: copy.Frame, Slot: i}
	}
	return n, &plan.ProjectNode{QuerySchema: schema, Child: n, Expressions: projection, Frame: copy.Frame}, source
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
			if !zero && n.Grouping != nil {
				d := n.Schema()[0].Usage.Type
				one, _ := sqltype.Integer(d, 1)
				two, _ := sqltype.Integer(d, 2)
				if i, err := exec.FindDistinctTuple(context.Background(), n.Grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0}); err != nil || i != -1 {
					t.Fatal("unequal collision", i, err)
				}
				if i, err := exec.FindDistinctTuple(context.Background(), n.Grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0, 1}); err != nil || i != 1 {
					t.Fatal("equal collision", i, err)
				}
			}
			rows, count, err := exec.Finalize(context.Background(), project, source, nil)
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
	defer source.Close()
	left, right := 0, 0
	l, r := n.LeftExpressions[0], n.RightExpressions[0]
	n.LeftExpressions[0] = queryProbe(l.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { left++; return l.Eval(c) })
	n.RightExpressions[0] = queryProbe(r.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { right++; return r.Eval(c) })
	state := &exec.State{Source: source}
	stage, err := exec.OpenSetNode(context.Background(), state, n)
	if err != nil {
		t.Fatal(err)
	}
	if left != 0 || right != 0 || n.Grouping != nil {
		t.Fatal("UNION ALL opened/demanded before pull", left, right, n.Grouping)
	}
	if _, present, err := stage.NextOccurrence(context.Background()); err != nil || !present {
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
			expressions := n.LeftExpressions
			if phase == "right" {
				expressions = n.RightExpressions
			}
			original := expressions[0]
			expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
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
				original := project.Expressions[0]
				project.Expressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) { cancel(); return original.Eval(c) })
			}
			rows, count, err := exec.Finalize(ctx, project, source, nil)
			want := error(context.Canceled)
			if phase == "late error" {
				want = sentinel
			}
			if rows != nil || count != 0 || !errors.Is(err, want) || source.View != nil {
				t.Fatal(rows, count, err, want, source.View)
			}
		})
	}
}

func TestQuerySetCoercionFrames(t *testing.T) {
	e := engine.New()
	op, done, err := enginetest.Operation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	ast := enginetest.Statement(t, "SELECT NULL UNION (SELECT NULL UNION SELECT 1)")
	e.Mu.Lock()
	auth, err := e.AuthorizationLocked(op)
	if err != nil {
		e.Mu.Unlock()
		t.Fatal(err)
	}
	b, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ids.ObjectRef{}, sqltype.InputEnvironment{}, nil, nil)
	e.Mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	project := b.Query.Root.(*plan.ProjectNode)
	n := project.Child.(*plan.SetNode)
	if n.Left.Frame == n.Right.Frame || n.Left.Frame == project.Frame || n.Right.Frame == project.Frame {
		t.Fatal("set arms share frame", project.Frame, n.Left.Frame, n.Right.Frame)
	}
	frames, queries := analyze.CollectQueryFrames(project)
	if len(frames) != 5 || len(queries) != 5 {
		t.Fatal("nested arm frame registration", len(frames), len(queries))
	}
	for _, schema := range []plan.QuerySchema{project.QuerySchema, n.QuerySchema, n.Left.QuerySchema, n.Right.QuerySchema} {
		for _, column := range schema {
			if column.Usage.Type.ID() != sqltype.Int4 || column.OriginTable != 0 || column.OriginAttribute != 0 || column.NullabilityKnown {
				t.Fatal(column)
			}
		}
	}
	if n.Left.Expressions[0].ResultUsage().Type == nil {
		t.Fatal("unknown output escaped parent resolution")
	}
	if analyze.CanSimplifyExists(b.Query) {
		t.Fatal("set presence discarded key demand")
	}
	if _, discard := analyze.PruneIdentityQueryProducer(n, project.Expressions[0]); discard {
		t.Fatal("identity pruning discarded set key")
	}
}

func TestQuerySetImmutableRewriteRemap(t *testing.T) {
	n, project, source := querySetUnitFixture(t, "intersect", true, false)
	defer source.Close()
	original := n.LeftExpressions[0]
	replacement := &queryProbeExpression{Info: expr.InfoOf(original), eval: func(c *exec.Context) (sqltype.Value, error) { return original.Eval(c) }}
	rewritten, err := plan.RewriteQueryExpressions(project, func(e expr.Expr) (expr.Expr, bool, error) {
		if e == original {
			return replacement, true, nil
		}
		return e, false, nil
	})
	if err != nil || rewritten == project {
		t.Fatal(rewritten, err)
	}
	copy := rewritten.(*plan.ProjectNode).Child.(*plan.SetNode)
	if copy == n || n.LeftExpressions[0] != original || copy.LeftExpressions[0] != replacement {
		t.Fatal("rewrite mutated held binary plan")
	}
	if copy.Operation != n.Operation || copy.All != n.All || len(copy.Grouping) != len(n.Grouping) {
		t.Fatal("rewrite lost set contract")
	}
	typ := n.Schema()[0].Usage.Type
	destination := *typ
	mapped, err := stored.RemapViewQuery(engine.New().CatalogRead(), project, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == typ {
			return &destination
		}
		return old
	})
	if err != nil {
		t.Fatal(err)
	}
	reminted := mapped.(*plan.ProjectNode).Child.(*plan.SetNode)
	if reminted == n || reminted.Left == n.Left || reminted.Right == n.Right || n.Schema()[0].Usage.Type != typ {
		t.Fatal("remap mutated held set")
	}
	for _, expression := range []expr.Expr{reminted.LeftExpressions[0], reminted.RightExpressions[0]} {
		if expression.ResultUsage().Type != &destination {
			t.Fatal("side recipe kept source-owner type")
		}
	}
	value, err := sqltype.Integer(&destination, 1)
	if err != nil {
		t.Fatal(err)
	}
	key, err := reminted.Grouping[0].PrepareKey(context.Background(), value)
	if err != nil || key.Type() != &destination {
		t.Fatal("grouping kept source-owner type", key, err)
	}
	equal, err := reminted.Grouping[0].Equal(context.Background(), key, value)
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
	n.QuerySchema[0].Usage = usage
	var err error
	n.Grouping[0].Grouping, err = sqltype.BindGrouping(usage)
	if err != nil {
		t.Fatal(err)
	}
	ctx := &aggregateCancelContext{Context: context.Background(), at: 4}
	n.LeftExpressions[0] = queryProbe(usage, func(*exec.Context) (sqltype.Value, error) {
		ctx.armed = true
		return sqltype.String(text, "set cancellation payload"), nil
	})
	rows, count, err := exec.Finalize(ctx, project, source, nil)
	if rows != nil || count != 0 || !errors.Is(err, context.Canceled) || source.View != nil {
		t.Fatal(rows, count, err)
	}
	zeroCtx := &aggregateCancelContext{Context: context.Background(), armed: true, at: 2}
	if _, err := exec.FindDistinctTuple(zeroCtx, []sqltype.Grouping{}, []sqltype.Value{}, []sqltype.Value{}, []int{0, 1}); !errors.Is(err, context.Canceled) {
		t.Fatal("zero-width collision ignored cancellation", err)
	}
}

func TestQuerySetInsertNoBatchOnLateArmFailure(t *testing.T) {
	b, source := insertSelectEvaluationFixture(t, "SELECT id FROM t")
	project := b.Query.Root.(*plan.ProjectNode)
	arm := *project
	arm.Expressions = append([]expr.Expr(nil), project.Expressions...)
	arm.QuerySchema = append(plan.QuerySchema(nil), project.QuerySchema...)
	n := &plan.SetNode{QuerySchema: arm.QuerySchema, Left: &arm, Right: &arm, Operation: "union", All: true, LeftExpressions: append([]expr.Expr(nil), arm.Expressions...), RightExpressions: append([]expr.Expr(nil), arm.Expressions...)}
	original := n.RightExpressions[0]
	sentinel := errors.New("late INSERT set arm")
	calls := 0
	n.RightExpressions[0] = queryProbe(original.ResultUsage(), func(c *exec.Context) (sqltype.Value, error) {
		calls++
		if calls == 3 {
			return sqltype.Value{}, sentinel
		}
		return original.Eval(c)
	})
	project.Child = n
	for i, expression := range project.Expressions {
		project.Expressions[i] = plan.ColumnExpr{Info: expr.InfoOf(expression), Scope: 1, Frame: project.Frame, Slot: i}
	}
	rows, err := dml.EvaluateInsertQuery(context.Background(), b, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	if rows.Values != nil || err != sentinel || source.View != nil || source.Anonymous != nil {
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
			e := engine.New()
			enginetest.Run(t, e, "CREATE TABLE set_dest(n INT)")
			op, done, err := enginetest.Operation(e, context.Background())
			if err != nil {
				t.Fatal(err)
			}
			defer done()
			ast := enginetest.Statement(t, "INSERT INTO set_dest "+tc.query).(*parser.Insert)
			e.Mu.Lock()
			auth, err := e.AuthorizationLocked(op)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			ref, err := e.ResolveReference(auth, ast.Name)
			if err != nil {
				e.Mu.Unlock()
				t.Fatal(err)
			}
			bound, err := core.Describe(op.Ctx, e.AnalysisHost(op), ast, auth, ref, sqltype.InputEnvironment{}, nil, nil)
			e.Mu.Unlock()
			if err != nil {
				t.Fatal(err)
			}
			project := bound.Query.Root.(*plan.ProjectNode)
			if project.Schema()[0].Usage.Type.ID() != sqltype.Int4 || project.Expressions[0].ResultUsage().Type.ID() != sqltype.Int4 {
				t.Fatal("destination projection is not assigned INT4", project.Schema())
			}
			node := project.Child
			for {
				if node.Schema()[0].Usage.Type.ID() != tc.source {
					t.Fatal("assignment changed source schema", node.Schema(), tc.source)
				}
				if _, ok := node.(*plan.SetNode); ok {
					break
				}
				child, sibling := plan.QueryInputs(node)
				if child == nil || sibling != nil {
					t.Fatal("unexpected set tail", node)
				}
				node = child
			}
		})
	}
}
