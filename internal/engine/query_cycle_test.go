package engine

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func cycleUnitRecipe(t *testing.T, usages ...sqltype.Usage) *plan.CycleRecipe {
	t.Helper()
	c := &plan.CycleRecipe{MarkSlot: len(usages), Keys: make([]int, len(usages)), Grouping: make([]sqltype.Grouping, len(usages))}
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	c.Mark = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, true)}
	c.Fallback = plan.LiteralExpr{Info: expr.Info{Usage: boolean.Usage()}, Value: sqltype.Boolean(boolean, false)}
	members := make([]sqltype.CompositeMember, len(usages))
	for i, u := range usages {
		c.Keys[i] = i
		members[i] = sqltype.CompositeMember{Attribute: int32(i + 1), Name: "key", Usage: u}
		var err error
		c.Grouping[i], err = sqltype.BindGrouping(u)
		if err != nil {
			t.Fatal(err)
		}
	}
	var err error
	c.Record, err = sqltype.RecordShape(members)
	if err != nil {
		t.Fatal(err)
	}
	c.Path, err = sqltype.Array(2287, "_record", c.Record)
	if err != nil {
		t.Fatal(err)
	}
	return c
}
func cycleUnitAugment(t *testing.T, c *plan.CycleRecipe, parent sqltype.Value, fields ...sqltype.Value) []sqltype.Value {
	t.Helper()
	boolean := c.Mark.ResultUsage().Type
	row, err := c.Augment(context.Background(), fields, parent, sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false))
	if err != nil {
		t.Fatal(err)
	}
	return row
}
func TestQueryCycleEquality(t *testing.T) {
	view := New().CatalogRead()
	ctx := context.Background()
	for _, tc := range []struct {
		id   sqltype.ID
		a, b string
	}{
		{sqltype.Int4, "7", "7"}, {sqltype.Int8, "7", "7"}, {sqltype.Numeric, "1.0", "1.00"}, {sqltype.Float8, "NaN", "NaN"}, {sqltype.BPChar, "a", "a "}, {sqltype.Text, "a", "a"}, {sqltype.Bool, "true", "true"}, {1007, "{1,NULL}", "{1,NULL}"},
	} {
		typ, ok := view.LookupType(tc.id)
		if !ok {
			t.Fatal(tc.id)
		}
		c := cycleUnitRecipe(t, typ.Usage())
		a, err := typ.Input(ctx, tc.a, -1)
		if err != nil {
			t.Fatal(err)
		}
		b, err := typ.Input(ctx, tc.b, -1)
		if err != nil {
			t.Fatal(err)
		}
		pa := cycleUnitAugment(t, c, sqltype.Value{}, a)[c.MarkSlot+1]
		pb := cycleUnitAugment(t, c, sqltype.Value{}, b)[c.MarkSlot+1]
		equal, err := c.EqualPath(ctx, pa, pb)
		if err != nil || !equal {
			t.Fatal(tc, equal, err)
		}
		ha, err := c.HashPath(ctx, pa)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := c.HashPath(ctx, pb)
		if err != nil || ha != hb {
			t.Fatal(tc, ha, hb, err)
		}
		null := cycleUnitAugment(t, c, sqltype.Value{}, sqltype.Null(typ))[c.MarkSlot+1]
		equal, err = c.EqualPath(ctx, null, null)
		if err != nil || !equal || null.ArrayElement(0).IsNull() {
			t.Fatal("NULL field must remain a non-NULL record", err)
		}
	}
	integer, _ := view.LookupType(sqltype.Int4)
	c := cycleUnitRecipe(t, integer.Usage(), integer.Usage())
	one, two := cteUnitValue(t, integer, 1), cteUnitValue(t, integer, 2)
	a := cycleUnitAugment(t, c, sqltype.Value{}, one, two)
	b := cycleUnitAugment(t, c, sqltype.Value{}, two, one)
	if equal, err := c.EqualPath(ctx, a[3], b[3]); err != nil || equal {
		t.Fatal("ordered keys collapsed", equal, err)
	}
	grouping := make([]plan.Grouping, 3)
	for i := range 2 {
		grouping[i].Grouping, _ = sqltype.BindGrouping(integer.Usage())
	}
	// Keep the mark values and grouping bound to one descriptor generation.
	boolean := c.Mark.ResultUsage().Type
	grouping[2].Grouping, _ = sqltype.BindGrouping(boolean.Usage())
	// Force a hash bucket whose first tuple has the same user fields and mark
	// but different ancestry. Only full path equality may reject that tuple.
	a = cycleUnitAugment(t, c, a[3], two, one)
	stage := exec.RecursiveUnionStage{Node: &plan.RecursiveUnionNode{SetNode: plan.SetNode{QuerySchema: make(plan.QuerySchema, 4), Grouping: grouping}, Definition: &plan.CTEDefinition{UserWidth: 2}, Cycle: c}, Source: &exec.Source{Flat: append(append([]sqltype.Value(nil), a...), b...)}}
	if index, err := stage.FindTuple(ctx, b, []int{0, 1}); err != nil || index != 1 {
		t.Fatal("full collision equality", index, err)
	}
}

func TestQueryCycleParent(t *testing.T) {
	integer := cteUnitType(t)
	c := cycleUnitRecipe(t, integer.Usage())
	one, two := cteUnitValue(t, integer, 1), cteUnitValue(t, integer, 2)
	a := cycleUnitAugment(t, c, sqltype.Value{}, one)
	b := cycleUnitAugment(t, c, sqltype.Value{}, two)
	childB := cycleUnitAugment(t, c, b[2], one)
	childA := cycleUnitAugment(t, c, a[2], one)
	if mark, _ := childB[1].Boolean(); mark {
		t.Fatal("used a different branch's parent")
	}
	if mark, _ := childA[1].Boolean(); !mark {
		t.Fatal("lost closing row")
	}
	if a[2].ArrayLen() != 1 || childA[2].ArrayLen() != 2 {
		t.Fatal("mutated parent path")
	}
	d := &plan.CTEDefinition{Cycle: c}
	self := &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{CTE: d}}
	physical := &plan.SingletonNode{QuerySchema: make(plan.QuerySchema, 4)}
	root := &plan.JoinNode{Left: physical, Right: self, LeftWidth: 4}
	if found, offset := recursiveParentSlot(root, d, 0); found != self || offset != 4 {
		t.Fatal(found, offset)
	}
	root = &plan.JoinNode{Left: self, Right: physical, LeftWidth: 3}
	if found, offset := recursiveParentSlot(root, d, 0); found != self || offset != 0 {
		t.Fatal(found, offset)
	}
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	predicate, err := sqltype.BindComparisonPredicate(sqltype.CompareNotEqual, integer, integer, boolean, sqltype.CollationRef{}, false)
	if err != nil {
		t.Fatal(err)
	}
	for _, pair := range [][2]sqltype.Value{{one, one}, {sqltype.Null(integer), one}, {one, sqltype.Null(integer)}} {
		value, err := predicate.Evaluate(context.Background(), pair[0], pair[1], sqltype.InputEnvironment{})
		if err != nil {
			t.Fatal(err)
		}
		if !value.IsNull() {
			if expand, _ := value.Boolean(); expand {
				t.Fatal("equal/NULL marks expanded")
			}
		}
	}
}

func TestQueryCycleOwnership(t *testing.T) {
	view := New().CatalogRead()
	integer, _ := view.LookupType(sqltype.Int4)
	array, _ := view.LookupType(1007)
	c := cycleUnitRecipe(t, array.Usage())
	other := cycleUnitRecipe(t, array.Usage())
	if c.Path.Element() != c.Record || c.Path == other.Path || c.Record == other.Record {
		t.Fatal("merged occurrence authorities")
	}
	foreign, err := sqltype.Array(1007, "_int4", integer)
	if err != nil {
		t.Fatal(err)
	}
	mapper := analyze.RecordTypeMapper(func(old *sqltype.Descriptor) *sqltype.Descriptor {
		if old == array {
			return foreign
		}
		return old
	})
	mapped := mapper(c.Path)
	mappedOther := mapper(other.Path)
	if mapped == c.Path || mapped.Element() != mapper(c.Record) || mapped == mappedOther || mapped.Element() == mappedOther.Element() {
		t.Fatal("incoherent descriptor memo")
	}
	if c.Path.Element() != c.Record || c.Record.CompositeMember(0).Usage.Type != array {
		t.Fatal("changed source")
	}
	if !validExpressionUsage(view, c.Path.Usage()) || validExpressionUsage(view, mapped.Usage()) {
		t.Fatal("foreign leaf accepted")
	}
	if sqltype.SameRecordUsage(c.Path.Usage(), mapped.Usage()) {
		t.Fatal("foreign same-OID internal leaf accepted")
	}
	engine, bound, err := recursiveUnitBind(t, "WITH RECURSIVE r(n) AS (SELECT 1 UNION SELECT r.n FROM (VALUES(7),(8)) e(x) CROSS JOIN r) CYCLE n SET cyc USING path SELECT * FROM r")
	if err != nil {
		t.Fatal(err)
	}
	var original *plan.RecursiveUnionNode
	plan.VisitCTEQuery(bound.Root, func(node plan.Node) {
		if n, ok := node.(*plan.RecursiveUnionNode); ok {
			original = n
		}
	}, func(expr.Expr) {}, true)
	if original == nil {
		t.Fatal("missing cycle recursive node")
	}
	catalogRecord, recordOK := engine.CatalogRead().LookupType(2249)
	catalogPath, pathOK := engine.CatalogRead().LookupType(2287)
	if !recordOK || !pathOK || catalogRecord == original.Cycle.Record || catalogPath == original.Cycle.Path ||
		catalogPath.Concrete() || catalogPath.Usage().Valid() || catalogPath.IsUserDefined() {
		t.Fatal("occurrence shape replaced catalog record authority")
	}
	if err := validateStoredSetNode(&original.SetNode, original.Cycle, nil); err != nil {
		t.Fatal("original cycle arm layout", err)
	}
	badWidth := original.SetNode
	badWidth.LeftExpressions = append(append([]expr.Expr(nil), original.LeftExpressions...), original.RightExpressions[original.Cycle.MarkSlot])
	if err := validateStoredSetNode(&badWidth, original.Cycle, nil); err == nil {
		t.Fatal("accepted generated anchor slot as an original user target")
	}
	destination := New()
	remapped, err := remapViewQuery(destination.CatalogRead(), original.Definition.Query.Root, func(ref ids.ObjectRef) ids.ObjectRef { return ref }, func(old *sqltype.Descriptor) *sqltype.Descriptor {
		typ, ok := destination.CatalogRead().LookupType(old.ID())
		if !ok {
			t.Fatal(old.ID())
		}
		return typ
	})
	if err != nil {
		t.Fatal(err)
	}
	union := remapped.(*plan.ProjectNode).Child.(*plan.RecursiveUnionNode)
	if err := validateStoredSetNode(&union.SetNode, union.Cycle, nil); err != nil {
		t.Fatal("remapped cycle arm layout", err)
	}
	badParent := union.SetNode
	badParent.RightExpressions = append([]expr.Expr(nil), union.RightExpressions...)
	badParent.RightExpressions[union.Cycle.MarkSlot+1] = union.RightExpressions[union.Cycle.MarkSlot]
	if err := validateStoredSetNode(&badParent, union.Cycle, nil); err == nil {
		t.Fatal("accepted parent mark in the hidden path slot")
	}
	scan, offset := recursiveParentSlot(union.Right.Child, union.Definition, 0)
	if scan == nil || offset != 1 || scan.CTE != union.Definition || union.Definition.Query.Root != remapped {
		t.Fatal("remapped self identity/parent", scan, offset)
	}
	if union.Cycle.Path.Element() != union.Cycle.Record || union.Schema()[2].Usage.Type != union.Cycle.Path || scan.Schema()[2].Usage.Type != union.Cycle.Path {
		t.Fatal("remapped schema/recipe descriptor disagreement")
	}
	if union.Cycle.Path == original.Cycle.Path || original.Cycle.Path.Element() != original.Cycle.Record {
		t.Fatal("foreign remap retained/mutated source")
	}
	for i := range 2 {
		hidden, ok := union.RightExpressions[union.Cycle.MarkSlot+i].(plan.ColumnExpr)
		if !ok || hidden.Slot != offset+union.Cycle.MarkSlot+i || !scan.Demand[union.Cycle.MarkSlot+i] {
			t.Fatal("hidden projection lost joined parent authority", hidden)
		}
	}
}

type cycleUnitCancelContext struct {
	context.Context
	remaining int
}

func (c *cycleUnitCancelContext) Err() error {
	c.remaining--
	if c.remaining <= 0 {
		return context.Canceled
	}
	return nil
}
func TestQueryCycleLifecycle(t *testing.T) {
	integer := cteUnitType(t)
	c := cycleUnitRecipe(t, integer.Usage())
	one := cteUnitValue(t, integer, 1)
	row := cycleUnitAugment(t, c, sqltype.Value{}, one)
	for range 12 {
		row = cycleUnitAugment(t, c, row[2], one)
	}
	ctx := &cycleUnitCancelContext{Context: context.Background(), remaining: 4}
	if _, err := c.HashPath(ctx, row[2]); !errors.Is(err, context.Canceled) {
		t.Fatal("hash ignored cancellation", err)
	}
	ctx = &cycleUnitCancelContext{Context: context.Background(), remaining: 4}
	if _, err := c.EqualPath(ctx, row[2], row[2]); !errors.Is(err, context.Canceled) {
		t.Fatal("comparison ignored cancellation", err)
	}
	boolean, _ := New().CatalogRead().LookupType(sqltype.Bool)
	ctx = &cycleUnitCancelContext{Context: context.Background(), remaining: 4}
	if _, err := c.Augment(ctx, []sqltype.Value{one}, row[2], sqltype.Boolean(boolean, true), sqltype.Boolean(boolean, false)); !errors.Is(err, context.Canceled) {
		t.Fatal("append ignored cancellation", err)
	}
	owner, state, node := cteUnitScan(nil, nil, 1)
	stage, err := exec.OpenCTEScan(context.Background(), state, node)
	if err != nil {
		t.Fatal(err)
	}
	if owner.Producers[node.CTE].Opened {
		t.Fatal("metadata opened producer")
	}
	cteUnitNext(t, &stage)
	producer := owner.Producers[node.CTE]
	primary := errors.New("cycle primary")
	closeFailure := errors.New("cycle close")
	closes := 0
	producer.Inner.Finish = func() error { closes++; return closeFailure }
	if producer.Fail(primary) != primary {
		t.Fatal("lost primary")
	}
	if _, err := producer.Ensure(context.Background(), 0); err != primary {
		t.Fatal("replayed accepted prefix", err)
	}
	if err := owner.Close(); err != primary || closes != 1 {
		t.Fatal(err, closes)
	}
}

func TestQueryCycleSignature(t *testing.T) {
	view := New().CatalogRead()
	small, _ := view.LookupType(sqltype.Int4)
	big, _ := view.LookupType(sqltype.Int8)
	a, b := cycleUnitRecipe(t, small.Usage()), cycleUnitRecipe(t, big.Usage())
	if !sameStatementResultUsage(a.Path.Usage(), b.Path.Usage()) {
		t.Fatal("public RECORD[] OID froze private member shape")
	}
	if sqltype.SameRecordUsage(a.Path.Usage(), b.Path.Usage()) {
		t.Fatal("internal shape validation weakened")
	}
	if sameStatementUsage(small.Usage(), big.Usage()) {
		t.Fatal("ordinary result type widened")
	}
}
