package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func subqueryTestInteger(t *testing.T, typ *sqltype.Descriptor, n int64) sqltype.Value {
	t.Helper()
	value, err := sqltype.Integer(typ, n)
	if err != nil {
		t.Fatal(err)
	}
	return value
}

func subqueryTestExpression(t *testing.T, e *Engine, q *BoundQuery, kind parser.SubqueryKind) subqueryExpression {
	t.Helper()
	typ, ok := e.CatalogRead().LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing BOOL descriptor")
	}
	usage := typ.Usage()
	if kind == parser.SubqueryScalar {
		usage = q.Root.Schema()[0].Usage
	}
	return subqueryExpression{expressionInfo: expressionInfo{usage: usage}, kind: kind, queryPlan: q, presenceOnly: kind == parser.SubqueryExists && canSimplifyExists(q)}
}

// Replace an actual scheduler-owned target rather than mocking a query result.
func subqueryTestTarget(t *testing.T, project *projectNode, replacement BoundExpr) {
	t.Helper()
	for node := project.child; node != nil; {
		switch n := node.(type) {
		case *evaluationNode:
			if len(n.expressions) > 0 {
				n.expressions[0] = replacement
				return
			}
		case *sortNode:
			if len(n.outputs) > 0 {
				n.outputs[0] = replacement
				return
			}
		}
		left, right := queryInputs(node)
		if right != nil {
			t.Fatal("target scheduler crossed a join")
		}
		node = left
	}
	if len(project.expressions) == 0 {
		t.Fatal("missing target")
	}
	project.expressions[0] = replacement
}

func TestQuerySubqueryDemand(t *testing.T) {
	for _, tc := range []struct {
		sql     string
		ignored bool
	}{
		{"SELECT abs(k) FROM a LIMIT 1", true},
		{"SELECT abs(k) FROM a ORDER BY k LIMIT 1", true},
		{"SELECT abs(k) AS needed FROM a ORDER BY needed LIMIT 1", true},
		{"SELECT abs(k) FROM a LIMIT 1 OFFSET 0", false},
		{"SELECT abs(k) FROM a LIMIT 1 OFFSET 1", false},
		{"SELECT abs(k) FROM a ORDER BY k LIMIT 1 OFFSET 1", false},
		{"SELECT count(*) FROM a", false},
		{"SELECT 1 FROM a HAVING TRUE", false},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			e, q, source := queryJoinsFixture(t, tc.sql)
			project := q.Root.(*projectNode)
			sentinel := errors.New("final target")
			calls := 0
			probe := queryProbe(project.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Value{}, sentinel })
			// An ORDER output owns its cache; replace that key instead of assuming
			// a target ordinal is also an evaluation cache ordinal.
			ordered := false
			for node := project.child; node != nil; {
				if n, ok := node.(*sortNode); ok {
					for i, key := range n.keys {
						if key.output {
							n.keys[i].expression = probe
							ordered = true
						}
					}
				}
				node, _ = queryInputs(node)
			}
			if !ordered {
				subqueryTestTarget(t, project, probe)
			}
			if canSimplifyExists(q) != tc.ignored {
				t.Fatal("wrong demand eligibility")
			}
			plan := q
			if tc.ignored {
				var err error
				plan, err = presenceQueryPlan(q)
				if err != nil {
					t.Fatal(err)
				}
			}
			parent := queryState{source: source}
			c := parent.evaluation(context.Background())
			c.Frame = 99
			exists := subqueryTestExpression(t, e, plan, parser.SubqueryExists)
			result, err := exists.Eval(&c)
			truth, _ := result.Boolean()
			if tc.ignored {
				if err != nil || !truth || calls != 0 {
					t.Fatal(truth, err, calls)
				}
			} else if err != sentinel || calls == 0 {
				t.Fatal("retained target not demanded", err, calls)
			}
			normal := subqueryTestExpression(t, e, q, parser.SubqueryScalar)
			if _, err = normal.Eval(&c); err != sentinel || calls == 0 {
				t.Fatal("normal target demand lost", err, calls)
			}
		})
	}
	t.Run("rowmark eligibility", func(t *testing.T) {
		_, q, _ := queryJoinsFixture(t, "SELECT abs(k) FROM a FOR UPDATE")
		if canSimplifyExists(q) {
			t.Fatal("rowmark target demand stripped")
		}
	})
	t.Run("required derived projection", func(t *testing.T) {
		e, q, source := queryJoinsFixture(t, "SELECT 1 FROM (SELECT abs(k) AS x FROM a) d WHERE x=1 LIMIT 1")
		frames, _ := collectQueryFrames(q.Root)
		root := q.Root.(*projectNode)
		sentinel := errors.New("required derived target")
		calls := 0
		found := false
		for frame, project := range frames {
			if frame != root.frame {
				subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Value{}, sentinel }))
				found = true
			}
		}
		if !found {
			t.Fatal("missing derived frame")
		}
		presence, err := presenceQueryPlan(q)
		if err != nil {
			t.Fatal(err)
		}
		parent := queryState{source: source}
		c := parent.evaluation(context.Background())
		exists := subqueryTestExpression(t, e, presence, parser.SubqueryExists)
		if _, err = exists.Eval(&c); err != sentinel || calls == 0 {
			t.Fatal(err, calls)
		}
	})
}

func TestQuerySubqueryCancellation(t *testing.T) {
	e, q, source := queryJoinsFixture(t, "SELECT abs(k) FROM a LIMIT 1 OFFSET 1")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sentinelCalls := 0
	subqueryTestTarget(t, q.Root.(*projectNode), queryProbe(q.Root.Schema()[0].Usage, func(*EvalContext) (sqltype.Value, error) {
		sentinelCalls++
		cancel()
		return sqltype.Value{}, context.Canceled
	}))
	parent := queryState{source: source}
	c := parent.evaluation(ctx)
	scalar := subqueryTestExpression(t, e, q, parser.SubqueryScalar)
	if _, err := scalar.Eval(&c); err != context.Canceled || sentinelCalls != 1 {
		t.Fatal(err, sentinelCalls)
	}
	if _, err := scalar.Eval(&c); err != context.Canceled || sentinelCalls != 1 {
		t.Fatal("cancelled reopen demanded data", err, sentinelCalls)
	}
}

func TestQuerySubqueryCleanup(t *testing.T) {
	for _, phase := range []string{"producer", "projection", "close"} {
		t.Run(phase, func(t *testing.T) {
			primary := errors.New("primary")
			secondary := errors.New("close")
			closes := 0
			stage := indexedStage{source: &querySource{singleton: true}, finish: func() error { closes++; return secondary }}
			switch phase {
			case "producer":
				stage.selection = []int{}
				stage.produce = func(context.Context, *indexedStage) (bool, error) { return false, primary }
			case "projection":
				stage.schema = []ColumnDescriptor{{}}
				stage.expressions = []BoundExpr{queryProbe(sqltype.Usage{}, func(*EvalContext) (sqltype.Value, error) { return sqltype.Value{}, primary })}
				stage.evaluation = EvalContext{Context: context.Background()}
			case "close":
				if err := stage.Close(); err != secondary {
					t.Fatal(err)
				}
			}
			if phase != "close" {
				var err error
				if phase == "producer" {
					_, _, err = stage.nextOccurrence(context.Background())
				} else {
					_, err = stage.Next(context.Background(), make([]sqltype.Value, 1))
				}
				if err != primary {
					t.Fatal("cleanup replaced primary failure", err)
				}
				if _, _, err = stage.nextOccurrence(context.Background()); err != primary {
					t.Fatal("terminal error not retained", err)
				}
			}
			if closes != 1 {
				t.Fatal(closes)
			}
			if err := stage.Close(); err != nil || closes != 1 {
				t.Fatal("non-idempotent close", err, closes)
			}
			if _, present, err := stage.nextOccurrence(context.Background()); present || err != nil {
				t.Fatal("closed stage reopened", present, err)
			}
		})
	}
}

func TestQuerySubqueryFrames(t *testing.T) {
	_, q, source := queryJoinsFixture(t, "SELECT (SELECT (SELECT a.k)) FROM a")
	rows, count, err := finalizeQuery(context.Background(), q.Root, source, nil)
	if err != nil || count != 4 {
		t.Fatal("grandparent query cardinality", count, err)
	}
	defer rows.Close()
	scratch := make([]sqltype.Value, 1)
	for _, want := range []struct {
		value int64
		null  bool
	}{{1, false}, {1, false}, {0, true}, {2, false}} {
		present, err := rows.Next(context.Background(), scratch)
		got, _ := scratch[0].Integer()
		if !present || err != nil || scratch[0].IsNull() != want.null || !want.null && got != want.value {
			t.Fatal("wrong lexical frame result", scratch[0], present, err)
		}
	}
	if present, err := rows.Next(context.Background(), scratch); present || err != nil {
		t.Fatal("grandparent query did not end", present, err)
	}
	binder := expressionBinder{queryNamespace: &queryNamespace{}, queryFrame: 7, aggregateClause: "RETURNING"}
	original := binder.queryNamespace
	restore := binder.enterQuery(false)
	if binder.queryNamespace != nil || binder.frameID() == 7 || len(binder.queryScopes) != 1 {
		t.Fatal("scope entry")
	}
	restore()
	if binder.queryNamespace != original || binder.frameID() != 7 || binder.aggregateClause != "RETURNING" || len(binder.queryScopes) != 0 {
		t.Fatal("scope restoration")
	}
}

func TestQuerySubqueryManifest(t *testing.T) {
	e := New()
	for _, sql := range []string{"CREATE TABLE a(k INT)", "CREATE TABLE b(k INT)", "CREATE TABLE c(k INT)"} {
		run(t, e, sql)
	}
	op, done, err := testOperation(e, context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer done()
	scope, err := e.StartStatement(op)
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := scope.Finish(nil); err != nil {
			t.Error(err)
		}
	}()
	ast := statement(t, "SELECT (SELECT b.k FROM b) FROM a WHERE EXISTS(SELECT c.k FROM c FOR UPDATE)")
	if _, err = scope.Describe(ast); err != nil {
		t.Fatal(err)
	}
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var bound binding
	if err == nil {
		bound, err = e.describe(op.ctx, ast, auth, scope.lockedRef, scope.input, op, nil, &scope.queryLockAdmission)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	if len(bound.querySources) != 3 {
		t.Fatal("missing admitted occurrence", len(bound.querySources))
	}
	if err = scope.validateQuerySources(bound.querySources); err != nil {
		t.Fatal(err)
	}
	for i, name := range []string{"a", "b", "c"} {
		if bound.querySources[i].relation.name != name {
			t.Fatal(i, bound.querySources[i].relation.name)
		}
	}
	if bound.querySources[0].mark.mode != 0 || bound.querySources[1].mark.mode != 0 || bound.querySources[2].mark.mode == 0 {
		t.Fatal("row marks leaked across query boundaries")
	}
	changed := append([]queryLeaf(nil), bound.querySources...)
	changed[0], changed[1] = changed[1], changed[0]
	if err = scope.validateQuerySources(changed); err != ErrCatalogReplaced {
		t.Fatal("reordered admission accepted", err)
	}
	changed = append([]queryLeaf(nil), bound.querySources...)
	changed[2].mark = queryRowMark{}
	if err = scope.validateQuerySources(changed); err != ErrCatalogReplaced {
		t.Fatal("weakened row mark admitted", err)
	}
	if err = scope.validateQuerySources(bound.querySources[:2]); err != ErrCatalogReplaced {
		t.Fatal("omitted occurrence admitted", err)
	}
}

func TestQuerySubqueryAggregateOwnership(t *testing.T) {
	for _, tc := range []struct {
		sql   string
		rows  int
		value int64
	}{
		{"SELECT count((SELECT count(b.k) FROM b)) FROM a", 1, 4},
		{"SELECT (SELECT (SELECT count((SELECT a.k)))) FROM a", 1, 3},
		{"SELECT (SELECT (SELECT count((SELECT b.k))) FROM b) FROM a", 4, 3},
	} {
		t.Run(tc.sql, func(t *testing.T) {
			_, q, source := queryJoinsFixture(t, tc.sql)
			rows, count, err := finalizeQuery(context.Background(), q.Root, source, nil)
			if err != nil || count != tc.rows {
				t.Fatal("aggregate ownership cardinality", count, err)
			}
			defer rows.Close()
			scratch := make([]sqltype.Value, 1)
			for range tc.rows {
				present, err := rows.Next(context.Background(), scratch)
				got, valueErr := scratch[0].Integer()
				if !present || err != nil || valueErr != nil || got != tc.value {
					t.Fatal("aggregate ownership result", scratch[0], present, err, valueErr)
				}
			}
			if present, err := rows.Next(context.Background(), scratch); present || err != nil {
				t.Fatal("aggregate ownership query did not end", present, err)
			}
		})
	}
	t.Run("same owner across sublink", func(t *testing.T) {
		e := New()
		run(t, e, "CREATE TABLE a(k INT)")
		binder := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
		sql := "SELECT count((SELECT count(a.k))) FROM a"
		e.mu.Lock()
		auth, err := e.authorizationLocked(binder.op)
		binder.queryAuth = &auth
		if err == nil {
			_, err = binder.bindSelect(statement(t, sql).(*parser.Select), false)
		}
		e.mu.Unlock()
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != "42803" || diagnostic.Message != "aggregate function calls cannot be nested" {
			t.Fatal(err)
		}
	})
}

func TestQuerySubqueryRowComparison(t *testing.T) {
	e, q, _ := queryJoinsFixture(t, "SELECT k FROM a")
	integer := q.Root.Schema()[0].Usage.Type
	boolean, ok := e.CatalogRead().LookupType(sqltype.Bool)
	if !ok {
		t.Fatal("missing BOOL")
	}
	one, two := subqueryTestInteger(t, integer, 1), subqueryTestInteger(t, integer, 2)
	null := sqltype.Null(integer)
	for _, tc := range []struct {
		operator    sqltype.ComparisonOperator
		text        string
		left, right []sqltype.Value
		want        bool
		unknown     bool
	}{
		{sqltype.CompareEqual, "=", []sqltype.Value{null, one}, []sqltype.Value{one, two}, false, false},
		{sqltype.CompareEqual, "=", []sqltype.Value{null, one}, []sqltype.Value{one, one}, false, true},
		{sqltype.CompareNotEqual, "<>", []sqltype.Value{null, one}, []sqltype.Value{one, two}, true, false},
		{sqltype.CompareLess, "<", []sqltype.Value{one, null}, []sqltype.Value{two, one}, true, false},
		{sqltype.CompareLess, "<", []sqltype.Value{one, null}, []sqltype.Value{one, two}, false, true},
		{sqltype.CompareGreaterEqual, ">=", []sqltype.Value{one, one}, []sqltype.Value{one, one}, true, false},
	} {
		t.Run(tc.text, func(t *testing.T) {
			predicate, err := sqltype.BindComparisonPredicate(tc.operator, integer, integer, boolean)
			if err != nil {
				t.Fatal(err)
			}
			comparison, err := sqltype.BindComparison(integer, integer)
			if err != nil {
				t.Fatal(err)
			}
			expr := subqueryExpression{expressionInfo: expressionInfo{usage: boolean.Usage()}, operator: tc.text}
			for i := range tc.right {
				expr.comparisons = append(expr.comparisons, comparisonExpression{right: columnExpression{expressionInfo: expressionInfo{usage: integer.Usage()}, scope: 1, frame: 9, slot: i}, predicate: predicate})
				if tc.operator > sqltype.CompareNotEqual {
					expr.ordering = append(expr.ordering, comparison)
				}
			}
			c := EvalContext{Context: context.Background(), Scope: 1, Frame: 9, Row: tc.right}
			answer, err := expr.compare(&c, tc.left)
			truth, _ := answer.Boolean()
			if err != nil || answer.IsNull() != tc.unknown || (!tc.unknown && truth != tc.want) {
				t.Fatal(answer, err)
			}
		})
	}
}

func TestQuerySubqueryOperandDemand(t *testing.T) {
	e, q, source := queryJoinsFixture(t, "SELECT k FROM a")
	project := q.Root.(*projectNode)
	integer := project.Schema()[0].Usage.Type
	expr := subqueryTestExpression(t, e, q, parser.SubqueryAny)
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareEqual, integer, integer, expr.usage.Type)
	if err != nil {
		t.Fatal(err)
	}
	leftCalls, targetCalls := 0, 0
	project.expressions[0] = queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) { targetCalls++; return sqltype.Integer(integer, 1) })
	expr.comparisons = []comparisonExpression{{left: queryProbe(integer.Usage(), func(*EvalContext) (sqltype.Value, error) { leftCalls++; return sqltype.Integer(integer, 1) }), right: columnExpression{expressionInfo: expressionInfo{usage: integer.Usage()}, scope: 1, frame: project.frame, slot: 0}, predicate: predicate}}
	parent := queryState{source: source}
	c := parent.evaluation(context.Background())
	for demand := 1; demand <= 2; demand++ {
		answer, err := expr.Eval(&c)
		truth, _ := answer.Boolean()
		if err != nil || !truth || leftCalls != demand || targetCalls != demand {
			t.Fatal("operand repeated, speculative child row, or cached correlated demand", err, leftCalls, targetCalls)
		}
	}
}

func TestQuerySubqueryDMLFrameRestoration(t *testing.T) {
	for _, sql := range []string{"SELECT 1", "SELECT missing"} {
		t.Run(sql, func(t *testing.T) {
			e := New()
			run(t, e, "CREATE TABLE target(k INT)")
			run(t, e, "INSERT INTO target VALUES(77)")
			b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
			b.ref = roleTableRef(t, e, "target")
			b.relation = e.CatalogRead().state.descriptors[b.ref.Address].(*relationDescriptor)
			b.targetName = "target"
			e.mu.Lock()
			auth, authErr := e.authorizationLocked(b.op)
			e.mu.Unlock()
			if authErr != nil {
				t.Fatal(authErr)
			}
			b.queryAuth = &auth
			b.updateParameters = true
			b.aggregateClause = "RETURNING"
			_, err := b.bindSelect(statement(t, sql).(*parser.Select), false)
			if (sql == "SELECT missing") != (err != nil) {
				t.Fatal(err)
			}
			if b.queryNamespace != nil || b.queryFrame != 0 || b.aggregateClause != "RETURNING" || len(b.queryScopes) != 0 {
				t.Fatal("synthesized ancestor leaked after binding", err)
			}
			if b.relation.name != "target" {
				t.Fatal("target relation changed")
			}
		})
	}
}

func TestQuerySubqueryViewRemap(t *testing.T) {
	e, q, _ := queryJoinsFixture(t, "SELECT EXISTS(SELECT (SELECT b.k FROM b) FROM a ORDER BY k LIMIT 1)")
	before, syntax := collectQueryFrames(q.Root)
	integer, ok := e.CatalogRead().LookupType(sqltype.Int4)
	if !ok {
		t.Fatal("missing INT4")
	}
	replacement := *integer
	types := func(typ *sqltype.Descriptor) *sqltype.Descriptor {
		if typ == integer {
			return &replacement
		}
		return typ
	}
	refs := func(ref ObjectRef) ObjectRef { ref.Address.ObjectID += 100; return ref }
	if !queryDescriptorsChanged(q.Root, types) {
		t.Fatal("nested type change invisible")
	}
	mapped, err := remapViewQuery(New().CatalogRead(), q.Root, refs, types)
	if err != nil {
		t.Fatal(err)
	}
	after, queries := collectQueryFrames(mapped)
	if len(after) != len(before) || len(queries) != len(syntax) {
		t.Fatal("remapping dropped analyzed ignored targets")
	}
	for frame, old := range before {
		project := after[frame]
		if project == nil || project == old || project.syntax != old.syntax {
			t.Fatal("frame identity or immutability lost", frame)
		}
		for i, column := range old.Schema() {
			if project.Schema()[i].Usage.Type != types(column.Usage.Type) {
				t.Fatal("nested output type not retagged", frame, i)
			}
		}
	}
	if queryDescriptorsChanged(mapped, types) {
		t.Fatal("remap retained old nested descriptors")
	}
	if !queryDescriptorsChanged(q.Root, types) {
		t.Fatal("remapping mutated the original analyzed query")
	}
	originalRefs := make(map[int]ObjectRef)
	capture := true
	var walk func(LogicalNode)
	walk = func(node LogicalNode) {
		if n, ok := node.(*sourceNode); ok {
			if capture {
				originalRefs[n.occurrence] = n.ref
			} else {
				old, found := originalRefs[n.occurrence]
				if !found || n.ref != refs(old) {
					t.Fatal("nested physical identity not remapped", n.occurrence, n.ref)
				}
			}
		}
		left, right := queryInputs(node)
		if left != nil {
			walk(left)
		}
		if right != nil {
			walk(right)
		}
		queryExpressions(node, func(expr BoundExpr) {
			walkBoundExpression(expr, func(expr BoundExpr) {
				if child, ok := expr.(subqueryExpression); ok {
					walk(child.queryPlan.Root)
				}
			})
		})
	}
	walk(q.Root)
	capture = false
	walk(mapped)
}

func TestQuerySubqueryDerivedDuplicateSchema(t *testing.T) {
	_, q, _ := queryJoinsFixture(t, "SELECT d.* FROM (SELECT 1 AS x,2 AS x)d")
	schema := q.Root.Schema()
	if len(schema) != 2 || schema[0].Name != "x" || schema[1].Name != "x" || schema[0].Usage.Type.ID() != sqltype.Int4 || schema[1].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal("derived positional names/types changed", schema)
	}
	frames, _ := collectQueryFrames(q.Root)
	for frame, project := range frames {
		if frame != q.Root.(*projectNode).frame {
			if project.Schema()[0].Name != "x" || project.Schema()[1].Name != "x" {
				t.Fatal("child names changed", project.Schema())
			}
		}
	}
}

func TestQuerySubqueryAuthorization(t *testing.T) {
	e := New()
	op, done := roleOperation(t, e, "postgres")
	defer done()
	for _, sql := range []string{"CREATE USER subquery_reader", "CREATE TABLE o(id INT)", "CREATE TABLE c(k INT,v INT)"} {
		roleExecute(t, e, op, sql)
	}
	reader, _ := e.Roles().Lookup("subquery_reader")
	grantor := e.roles.names["postgres"]
	outer, child := roleTableRef(t, e, "o"), roleTableRef(t, e, "c")
	grant := func(ref ObjectRef, rights Privileges) {
		t.Helper()
		if err := e.SetObjectACL(context.Background(), ref, ACLValue{Explicit: true, Entries: []ACLEntry{{Grantor: grantor, Grantee: ACLGrantee{Role: reader.Ref}, Privileges: rights}}}); err != nil {
			t.Fatal(err)
		}
	}
	grant(outer, SelectPrivilege)
	grant(child, 0)
	key, value := child, child
	key.Address.SubID = 1
	value.Address.SubID = 2
	grant(key, SelectPrivilege)
	roleExecute(t, e, op, "SET SESSION AUTHORIZATION subquery_reader")
	ast := statement(t, "SELECT EXISTS(SELECT c.v FROM c WHERE c.k=o.id) FROM o WHERE FALSE")
	e.mu.Lock()
	auth, err := e.authorizationLocked(op)
	var bound binding
	if err == nil {
		bound, err = e.describe(op.ctx, ast, auth, outer, sqltype.InputEnvironment{}, op, nil, nil)
	}
	e.mu.Unlock()
	if err != nil {
		t.Fatal(err)
	}
	check := func(denied bool) {
		t.Helper()
		e.mu.Lock()
		current, err := e.authorizationLocked(op)
		if err == nil {
			err = current.checkBinding(bound, ast)
		}
		e.mu.Unlock()
		var diagnostic *sqlstate.Error
		if denied {
			want := sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42501", Message: "permission denied for table c"}
			if !errors.As(err, &diagnostic) || *diagnostic != want {
				t.Fatal("unused/empty child escaped column ACL", err)
			}
		} else if err != nil {
			t.Fatal(err)
		}
	}
	check(true)
	grant(value, SelectPrivilege)
	check(false)
	grant(value, 0)
	check(true)
}
