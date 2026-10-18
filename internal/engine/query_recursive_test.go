package engine

import (
	"context"
	"errors"
	"reflect"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"testing"
)

func recursiveUnitFixture(t *testing.T, all bool, seeds []int64, terminal int64) (*cteInvocation, *queryState, *derivedNode) {
	t.Helper()
	typ := cteUnitType(t)
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	schema := querySchema{{Name: "n", Usage: typ.Usage()}}
	d := &cteDefinition{owner: 1, recursive: true, name: "r", schema: schema}
	column := func(frame uint32) columnExpression {
		return columnExpression{expressionInfo: expressionInfo{usage: typ.Usage(), properties: ExprProperties{RowDependent: true}}, scope: 1, frame: frame, slot: 0}
	}
	rows := make([][]BoundExpr, len(seeds))
	for i, n := range seeds {
		rows[i] = []BoundExpr{literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: cteUnitValue(t, typ, n)}}
	}
	left := &projectNode{querySchema: schema, child: &valuesNode{querySchema: schema, rows: rows}, expressions: []BoundExpr{column(2)}, frame: 2}
	self := &worktableScanNode{derivedNode: derivedNode{querySchema: schema, cte: d, scanFrame: 3}}
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
	right := &projectNode{querySchema: schema, child: &filterNode{querySchema: schema, child: self, predicate: predicate}, expressions: []BoundExpr{successor}, frame: 3}
	n := &recursiveUnionNode{setNode: setNode{querySchema: schema, left: left, right: right, operation: "union", all: all, leftExpressions: left.expressions, rightExpressions: right.expressions}, definition: d}
	if !all {
		grouping, err := sqltype.BindGrouping(typ.Usage())
		if err != nil {
			t.Fatal(err)
		}
		n.grouping = []queryGrouping{{Grouping: grouping}}
	}
	project := &projectNode{querySchema: schema, child: n, expressions: []BoundExpr{column(4)}, frame: 4}
	d.query = &BoundQuery{Root: project}
	state := &queryState{source: &querySource{materialized: true}}
	owner := &cteInvocation{frame: 1, state: *state}
	state.ctes = owner
	return owner, state, &derivedNode{querySchema: schema, query: d.query, cte: d}
}

func recursiveUnitGeneration(t *testing.T, s *recursiveUnionStage, indexes []int) []int64 {
	t.Helper()
	var values []int64
	for _, index := range indexes {
		n, err := s.source.row(index)[0].Integer()
		if err != nil {
			t.Fatal(err)
		}
		values = append(values, n)
	}
	return values
}

func TestQueryRecursiveCTECurrentGenerationAndSharing(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1, 2}, 3)
	defer owner.close()
	first, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	second, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if owner.producers[node.cte].opened {
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
		stage := owner.producers[node.cte].recursion
		if stage.seen != nil {
			t.Fatal("ALL allocated deduplication state")
		}
		if i == 3 && !reflect.DeepEqual(recursiveUnitGeneration(t, stage, stage.current), []int64{1, 2}) {
			t.Fatal("RHS replayed accumulated output")
		}
		if i == 4 && !reflect.DeepEqual(recursiveUnitGeneration(t, stage, stage.current), []int64{2, 3}) {
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
	if _, present, err := first.nextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	if !reflect.DeepEqual(got, []int64{1, 2, 2, 3, 3}) {
		t.Fatal(got)
	}
	if node.query.Root != node.cte.query.Root {
		t.Fatal("mutated canonical query")
	}
}

func TestQueryRecursiveCTEDistinctAnchorPassAndCollision(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, false, []int64{1, 1, 2}, 3)
	defer owner.close()
	stage, err := openCTEScan(context.Background(), state, node)
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
	if _, present, err := stage.nextOccurrence(context.Background()); present || err != nil {
		t.Fatal(present, err)
	}
	n := node.query.Root.(*projectNode).child.(*recursiveUnionNode)
	one, two := cteUnitValue(t, n.Schema()[0].Usage.Type, 1), cteUnitValue(t, n.Schema()[0].Usage.Type, 2)
	if index, err := findDistinctTuple(context.Background(), n.grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0}); index != -1 || err != nil {
		t.Fatal("unequal forced collision", index, err)
	}
	if index, err := findDistinctTuple(context.Background(), n.grouping, []sqltype.Value{two}, []sqltype.Value{one, two}, []int{0, 1}); index != 1 || err != nil {
		t.Fatal("equal forced collision", index, err)
	}
}

func TestQueryRecursiveCTEDemandCancellationAndClose(t *testing.T) {
	owner, state, node := recursiveUnitFixture(t, true, []int64{1}, 100)
	defer owner.close()
	stage, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	cteUnitNext(t, &stage)
	producer := owner.producers[node.cte]
	if producer.recursion.phase != 0 || producer.recursion.source.flatCount != 1 {
		t.Fatal("seed demand opened a recursive pass")
	}
	cteUnitNext(t, &stage)
	if producer.recursion.phase != 1 {
		t.Fatal("did not enter recursive pass")
	}
	sibling, err := openCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	closes := 0
	finish := producer.recursion.arm.inner.finish
	producer.recursion.arm.inner.finish = func() error {
		closes++
		if finish != nil {
			return finish()
		}
		return nil
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, _, err := stage.nextOccurrence(ctx); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if _, _, err := sibling.nextOccurrence(context.Background()); !errors.Is(err, context.Canceled) {
		t.Fatal("sibling replay escaped producer failure", err)
	}
	if err := owner.close(); !errors.Is(err, context.Canceled) {
		t.Fatal("lost sticky cancellation", err)
	}
	if closes != 1 {
		t.Fatal("recursive child close count", closes)
	}
	if _, err := producer.ensure(context.Background(), 0); !errors.Is(err, context.Canceled) {
		t.Fatal("replay cleared primary", err)
	}
	if err := owner.close(); !errors.Is(err, context.Canceled) || closes != 1 {
		t.Fatal(err, closes)
	}
}

func TestQueryRecursiveCTENullAndZeroWidth(t *testing.T) {
	for _, zero := range []bool{false, true} {
		owner, state, node := recursiveUnitFixture(t, false, []int64{1, 1}, 2)
		n := node.query.Root.(*projectNode).child.(*recursiveUnionNode)
		if zero {
			n.querySchema = querySchema{}
			n.left.querySchema = querySchema{}
			n.left.child.(*valuesNode).querySchema = querySchema{}
			n.left.child.(*valuesNode).rows = [][]BoundExpr{{}, {}}
			n.left.expressions = nil
			n.leftExpressions = nil
			n.grouping = nil
			n.right.querySchema = querySchema{}
			n.right.expressions = nil
			n.rightExpressions = nil
			n.right.child = &worktableScanNode{derivedNode: derivedNode{cte: node.cte, querySchema: querySchema{}}}
			node.querySchema = querySchema{}
			node.query.Root.(*projectNode).querySchema = querySchema{}
			node.query.Root.(*projectNode).expressions = nil
		} else {
			typ := n.Schema()[0].Usage.Type
			n.left.child.(*valuesNode).rows = [][]BoundExpr{{literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.Null(typ)}}, {literalExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, value: sqltype.Null(typ)}}}
			n.right.child = &worktableScanNode{derivedNode: derivedNode{cte: node.cte, querySchema: n.Schema()}}
			n.right.expressions = []BoundExpr{columnExpression{expressionInfo: expressionInfo{usage: typ.Usage()}, scope: 1, frame: 3, slot: 0}}
			n.rightExpressions = n.right.expressions
		}
		stage, err := openCTEScan(context.Background(), state, node)
		if err != nil {
			t.Fatal(err)
		}
		if zero {
			for range 3 {
				if row := cteUnitNext(t, &stage); len(row) != 0 {
					t.Fatal(row)
				}
			}
			if owner.producers[node.cte].recursion.seen != nil {
				t.Fatal("zero-key recursion allocated seen set")
			}
		} else {
			if row := cteUnitNext(t, &stage); !row[0].IsNull() {
				t.Fatal(row)
			}
			if _, present, err := stage.nextOccurrence(context.Background()); present || err != nil {
				t.Fatal(present, err)
			}
		}
		if err := owner.close(); err != nil {
			t.Fatal(err)
		}
	}
}

func recursiveUnitBind(t *testing.T, text string) (*Engine, *BoundQuery, error) {
	t.Helper()
	statements, err := parser.Parse(text)
	if err != nil {
		return nil, nil, err
	}
	e := New()
	b := newTestExpressionBinder(t, context.Background(), e, sqltype.InputEnvironment{}, nil)
	e.mu.Lock()
	auth, err := e.authorizationLocked(b.op)
	e.mu.Unlock()
	if err != nil {
		return e, nil, err
	}
	b.queryAuth = &auth
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
	var union *recursiveUnionNode
	var self *worktableScanNode
	visitCTEQuery(query.Root, func(n LogicalNode) {
		switch n := n.(type) {
		case *recursiveUnionNode:
			union = n
		case *worktableScanNode:
			self = n
		}
	}, func(BoundExpr) {}, true)
	if union == nil || self == nil || self.query != nil || self.cte != union.definition || self.cte.folded() {
		t.Fatal("self edge/folding contract", union, self)
	}
	if left, right := queryInputs(self); left != nil || right != nil {
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
	var n *recursiveUnionNode
	visitCTEQuery(bound.Root, func(node LogicalNode) {
		if recursive, ok := node.(*recursiveUnionNode); ok {
			n = recursive
		}
	}, func(BoundExpr) {}, true)
	if n == nil {
		t.Fatal("missing recursive node")
	}
	project := n.definition.query.Root.(*projectNode)
	original := n.leftExpressions[0]
	replacement := queryProbe(original.ResultUsage(), original.Eval)
	rewritten, err := rewriteQueryExpressions(project, func(e BoundExpr) (BoundExpr, bool, error) {
		if e == original {
			return replacement, true, nil
		}
		return e, false, nil
	})
	if err != nil || rewritten == project || n.leftExpressions[0] != original {
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
	copy := mapped.(*projectNode).child.(*recursiveUnionNode)
	var self *worktableScanNode
	visitCTEQuery(copy.right, func(node LogicalNode) {
		if scan, ok := node.(*worktableScanNode); ok {
			self = scan
		}
	}, func(BoundExpr) {}, true)
	if copy.definition == n.definition || self.cte != copy.definition || self.query != nil || copy.definition.query.Root != mapped {
		t.Fatal("remapped recursive identities disagree")
	}
	if copy.Schema()[0].Usage.Type == n.Schema()[0].Usage.Type || n.definition.query.Root != project {
		t.Fatal("foreign remap retained/mutated source owner")
	}
}

func TestQueryRecursiveCTEStoredNamespaceIdentity(t *testing.T) {
	typ := cteUnitType(t)
	table := &parser.TableReference{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: "r"}}}}
	definition := &cteDefinition{recursive: true}
	self := &worktableScanNode{derivedNode: derivedNode{querySchema: querySchema{{Name: "n", Usage: typ.Usage()}}, table: table, cte: definition, ordinal: -1}}
	project := &projectNode{child: &filterNode{child: self}}
	if viewCTETable(project, table) != self || viewCTEOrdinal(project, -1) != self {
		t.Fatal("stored self scan lost opaque node identity")
	}
	renderer := &viewRenderer{slots: map[LogicalNode]map[int]string{self: {0: "r.n"}}}
	text, err := renderer.slotSQL(context.Background(), CatalogRead{}, viewCTETable(project, table), 0)
	if err != nil || text != "r.n" {
		t.Fatal("worktable namespace slot", text, err)
	}
}
