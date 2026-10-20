package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func recursiveUnitFixture(t *testing.T, all bool, seeds []int64, terminal int64) (*cteInvocation, *queryState, *plan.DerivedNode) {
	t.Helper()
	typ := cteUnitType(t)
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	schema := plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}
	d := &plan.CTEDefinition{Owner: 1, Recursive: true, Name: "r", Schema: schema}
	column := func(frame uint32) plan.ColumnExpr {
		return plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, Scope: 1, Frame: frame, Slot: 0}
	}
	rows := make([][]expr.Expr, len(seeds))
	for i, n := range seeds {
		rows[i] = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: cteUnitValue(t, typ, n)}}
	}
	left := &plan.ProjectNode{QuerySchema: schema, Child: &plan.ValuesNode{QuerySchema: schema, Rows: rows}, Expressions: []expr.Expr{column(2)}, Frame: 2}
	self := &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{QuerySchema: schema, CTE: d, ScanFrame: 3}}
	read := column(3)
	predicate := queryProbe(boolean.Usage(), func(c *EvalContext) (sqltype.Value, error) {
		value, err := read.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		n, err := value.Integer()
		return sqltype.Boolean(boolean, n < terminal), err
	})
	successor := queryProbe(typ.Usage(), func(c *EvalContext) (sqltype.Value, error) {
		value, err := read.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		n, err := value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		return sqltype.Integer(typ, n+1)
	})
	right := &plan.ProjectNode{QuerySchema: schema, Child: &plan.FilterNode{QuerySchema: schema, Child: self, Predicate: predicate}, Expressions: []expr.Expr{successor}, Frame: 3}
	n := &plan.RecursiveUnionNode{SetNode: plan.SetNode{QuerySchema: schema, Left: left, Right: right, Operation: "union", All: all, LeftExpressions: left.Expressions, RightExpressions: right.Expressions}, Definition: d}
	if !all {
		grouping, err := sqltype.BindGrouping(typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		n.Grouping = []plan.Grouping{{Grouping: grouping}}
	}
	project := &plan.ProjectNode{QuerySchema: schema, Child: n, Expressions: []expr.Expr{column(4)}, Frame: 4}
	d.Query = &plan.Query{Root: project}
	state := &queryState{Source: &querySource{Materialized: true}}
	owner := &cteInvocation{Frame: 1, State: *state}
	state.Ctes = owner
	return owner, state, &plan.DerivedNode{QuerySchema: schema, Query: d.Query, CTE: d}
}

func recursiveUnitGeneration(t *testing.T, s *recursiveUnionStage, indexes []int) []int64 {
	t.Helper()
	var values []int64
	for _, index := range indexes {
		n, err := s.Source.Row(index)[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, n)
	}
	return values
}

func TestQueryRecursiveCTECurrentGenerationAndSharing(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1, 2}, 3)
	defer owner.Close()
	first, err := OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	second, err := OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Producers[node.CTE].Opened {
		t.Fatal("eager recursive open")
	}
	var got []int64
	for i, want := range []int64{1, 2, 2, 3, 3} {
		row := cteUnitNext(t, &first)
		n, err := row[0].Integer()
		if err != nil || n != want {
			t.Fatal(n, want, err)
		}
		got = append(got, n)
		stage := owner.Producers[node.CTE].Recursion
		if stage.Seen != nil {
			t.Fatal("ALL allocated deduplication state")
		}
		if i == 3 && !reflect.DeepEqual(recursiveUnitGeneration(t, stage, stage.Current), []int64{1, 2}) {
			t.Fatal("RHS replayed accumulated output")
		}
		if i == 4 && !reflect.DeepEqual(recursiveUnitGeneration(t, stage, stage.Current), []int64{2, 3}) {
			t.Fatal("incorrect generation swap")
		}
		if i == 0 {
			if replay := cteUnitNext(t, &second); replay[0] != row[0] {
				t.Fatal("independent reader lost seed")
			}
			if err := second.Close(); err != nil {
				t.Fatal(err)
			}
		}
	}
	if _, present, err := first.NextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	if !reflect.DeepEqual(got, []int64{1, 2, 2, 3, 3}) {
		t.Fatal(got)
	}
	if node.Query.Root != node.CTE.Query.Root {
		t.Fatal("mutated canonical query")
	}
}

func TestQueryRecursiveCTEDistinctAnchorPassAndCollision(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, false, []int64{1, 1, 2}, 3)
	defer owner.Close()
	stage, err := OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []int64{1, 2, 3} {
		row := cteUnitNext(t, &stage)
		n, err := row[0].Integer()
		if err != nil || n != want {
			t.Fatal(n, want, err)
		}
	}
	if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	n := node.Query.Root.(*plan.ProjectNode).Child.(*plan.RecursiveUnionNode)
	one, two := cteUnitValue(t, n.Schema()[0].Usage.Type, 1), cteUnitValue(t, n.Schema()[0].Usage.Type, 2)
	if index, err := FindDistinctTuple(context.Background(), n.Grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0}); index != -1 || err != nil {
		t.Fatal("unequal forced collision", index, err)
	}
	if index, err := FindDistinctTuple(context.Background(), n.Grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0, 1}); index != 1 || err != nil {
		t.Fatal("equal forced collision", index, err)
	}
}

func TestQueryRecursiveCTEDemandCancellationAndClose(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 100)
	defer owner.Close()
	stage, err := OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	producer := owner.Producers[node.CTE]
	if producer.Recursion.Phase != 0 || producer.Recursion.Source.FlatCount != 1 {
		t.Fatal("seed demand opened a recursive pass")
	}
	cteUnitNext(t, &stage)
	if producer.Recursion.Phase != 1 {
		t.Fatal("did not enter recursive pass")
	}
	sibling, err := OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	closes := 0
	finish := producer.Recursion.Arm.Inner.Finish
	producer.Recursion.Arm.Inner.Finish = func() error {
		closes++
		if finish != nil {
			return finish()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := stage.NextOccurrence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := sibling.NextOccurrence(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal("sibling replay escaped producer failure", err)
	}
	if err := owner.Close(); !errors.Is(err, context.Canceled) {
		t.Fatal("lost sticky cancellation", err)
	}
	if closes != 1 {
		t.Fatal("recursive child close count", closes)
	}
	if _, err := producer.Ensure(context.Background(), 0); !errors.Is(err, context.Canceled) {
		t.Fatal("replay cleared primary", err)
	}
	if err := owner.Close(); !errors.Is(err, context.Canceled) || closes != 1 {
		t.Fatal(err, closes)
	}
}

func TestQueryRecursiveCTENullAndZeroWidth(t *testing.T) {
	for _, zero := range []bool{false, true} {
		owner, state, node := recursiveUnitFixture(t, false, []int64{1, 1}, 2)
		n := node.Query.Root.(*plan.ProjectNode).Child.(*plan.RecursiveUnionNode)
		if zero {
			n.QuerySchema = plan.QuerySchema{}
			n.Left.QuerySchema = plan.QuerySchema{}
			n.Left.Child.(*plan.ValuesNode).QuerySchema = plan.QuerySchema{}
			n.Left.Child.(*plan.ValuesNode).Rows = [][]expr.Expr{{}, {}}
			n.Left.Expressions = nil
			n.LeftExpressions = nil
			n.Grouping = nil
			n.Right.QuerySchema = plan.QuerySchema{}
			n.Right.Expressions = nil
			n.RightExpressions = nil
			n.Right.Child = &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{CTE: node.CTE, QuerySchema: plan.QuerySchema{}}}
			node.QuerySchema = plan.QuerySchema{}
			node.Query.Root.(*plan.ProjectNode).QuerySchema = plan.QuerySchema{}
			node.Query.Root.(*plan.ProjectNode).Expressions = nil
		} else {
			typ := n.Schema()[0].Usage.Type
			n.Left.Child.(*plan.ValuesNode).Rows = [][]expr.Expr{{plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}}, {plan.LiteralExpr{Info: expr.Info{Usage: typ.Usage()}, Value: sqltype.Null(typ)}}}
			n.Right.Child = &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{CTE: node.CTE, QuerySchema: n.Schema()}}
			n.Right.Expressions = []expr.Expr{plan.ColumnExpr{Info: expr.Info{Usage: typ.Usage()}, Scope: 1, Frame: 3, Slot: 0}}
			n.RightExpressions = n.Right.Expressions
		}
		stage, err := OpenCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		if zero {
			for range 3 {
				if row := cteUnitNext(t, &stage); len(row) != 0 {
					t.Fatal(row)
				}
			}
			if owner.Producers[node.CTE].Recursion.Seen != nil {
				t.Fatal("zero-key recursion allocated seen set")
			}
		} else {
			if row := cteUnitNext(t, &stage); !row[0].IsNull() {
				t.Fatal(row)
			}
			if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
				t.Fatal(present, err)
			}
		}
		if err := owner.Close(); err != nil {
			t.Fatal(err)
		}
	}
}

func recursiveUnitBind(t *testing.T, text string) (*Engine, *plan.Query, error) {
	t.Helper()
	statements, err := parser.Parse(text)
	if err != nil {
		return nil, nil, err
	}
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	e.mu.Lock()
	auth, err := e.authorizationLocked(binderOperation(b))
	e.mu.Unlock()
	if err != nil {
		return e, nil, err
	}
	b.query.auth = &auth
	query, err := b.bindSelect(statements[0].(*parser.Select), false)
	return e, query, err
}

func TestQueryRecursiveCTEBindingUsageAndIdentity(t *testing.T) {
	for _, tc := range []struct{ query, code string }{
		{"WITH RECURSIVE r(n) AS (SELECT 1 UNION ALL SELECT n::bigint FROM r) SELECT n FROM r", "42804"},
		{"WITH RECURSIVE r(n) AS (SELECT '7' UNION ALL SELECT n+1 FROM r) SELECT n FROM r", "42883"},
		{"WITH RECURSIVE r(v) AS (SELECT 'a'::varchar(3) UNION ALL SELECT v::varchar FROM r) SELECT v FROM r", "42804"},
	} {
		_, _, err := recursiveUnitBind(t, tc.query)
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) || diagnostic.Code != tc.code {
			t.Fatal(err, tc.code)
		}
	}
	_, query, err := recursiveUnitBind(t, "WITH RECURSIVE r(n) AS NOT MATERIALIZED (SELECT 1 UNION ALL SELECT n+1 FROM r WHERE n<3) SELECT n FROM r")
	if err != nil {
		t.Fatal(err)
	}
	var union *plan.RecursiveUnionNode
	var self *plan.WorktableScanNode
	plan.VisitCTEQuery(query.Root, func(n plan.Node) {
		switch n := n.(type) {
		case *plan.RecursiveUnionNode:
			union = n
		case *plan.WorktableScanNode:
			self = n
		}
	}, func(expr.Expr) {}, true)
	if union == nil || self == nil || self.Query != nil || self.CTE != union.Definition || self.CTE.Folded() {
		t.Fatal("self edge/folding contract", union, self)
	}
	if left, right := plan.QueryInputs(self); left != nil || right != nil {
		t.Fatal("self edge became structural cycle")
	}
	if len(union.Schema()) != 1 || union.Schema()[0].Usage.Type.ID() != sqltype.Int4 {
		t.Fatal(union.Schema())
	}
}

func TestQueryRecursiveCTERewriteAndForeignRemap(t *testing.T) {
	_, bound, err := recursiveUnitBind(t, "WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT n+1 FROM r WHERE n<3) SELECT n FROM r")
	if err != nil {
		t.Fatal(err)
	}
	var n *plan.RecursiveUnionNode
	plan.VisitCTEQuery(bound.Root, func(node plan.Node) {
		if recursive, ok := node.(*plan.RecursiveUnionNode); ok {
			n = recursive
		}
	}, func(expr.Expr) {}, true)
	if n == nil {
		t.Fatal("missing recursive node")
	}
	project := n.Definition.Query.Root.(*plan.ProjectNode)
	original := n.LeftExpressions[0]
	replacement := queryProbe(original.ResultUsage(), func(c *EvalContext) (sqltype.Value, error) { return original.Eval(c) })
	rewritten, err := plan.RewriteQueryExpressions(project, func(e expr.Expr) (expr.Expr, bool, error) {
		if e == original {
			return replacement, true, nil
		}
		return e, false, nil
	})
	if err != nil || rewritten == project || n.LeftExpressions[0] != original {
		t.Fatal("immutable recursive rewrite", err)
	}
	destination := New()
	mapped, err := remapViewQuery(destination.CatalogRead(), project, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		typ, ok := destination.CatalogRead().LookupType(old.ID())
		if !ok {
			t.Fatal(old.ID())
		}
		return typ
	})
	if err != nil {
		t.Fatal(err)
	}
	copy := mapped.(*plan.ProjectNode).Child.(*plan.RecursiveUnionNode)
	var self *plan.WorktableScanNode
	plan.VisitCTEQuery(copy.Right, func(node plan.Node) {
		if scan, ok := node.(*plan.WorktableScanNode); ok {
			self = scan
		}
	}, func(expr.Expr) {}, true)
	if copy.Definition == n.Definition || self.CTE != copy.Definition || self.Query != nil || copy.Definition.Query.Root != mapped {
		t.Fatal("remapped recursive identities disagree")
	}
	if copy.Schema()[0].Usage.Type == n.Schema()[0].Usage.Type || n.Definition.Query.Root != project {
		t.Fatal("foreign remap retained/mutated source owner")
	}
}

func TestQueryRecursiveCTEStoredNamespaceIdentity(t *testing.T) {
	typ := cteUnitType(t)
	table := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "r"}}}}
	definition := &plan.CTEDefinition{Recursive: true}
	self := &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{QuerySchema: plan.QuerySchema{{Name: "n", Usage: typ.Usage()}}, Table: table, CTE: definition, Ordinal: -1}}
	project := &plan.ProjectNode{Child: &plan.FilterNode{Child: self}}
	if viewCTETable(project, table) != self || viewCTEOrdinal(project, -1) != self {
		t.Fatal("stored self scan lost opaque node identity")
	}
	renderer := &viewRenderer{slots: map[plan.Node]map[int]string{self: {0: "r.n"}}}
	text, err := renderer.slotSQL(context.Background(), catalog.Read{}, viewCTETable(project, table), 0)
	if err != nil || text != "r.n" {
		t.Fatal("worktable namespace slot", text, err)
	}
}
