package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func searchUnitDefinition(t *testing.T, breadth bool, usage sqltype.Usage, distinct bool) (*expressionBinder, *plan.CTEDefinition) {
	t.Helper()
	b := &expressionBinder{host: New().analysisHost(nil), ctx: context.Background()}
	d := &plan.CTEDefinition{UserWidth: 1, Schema: plan.QuerySchema{{Name: "n", Usage: usage}}, Search: &plan.SearchRecipe{Syntax: &parser.SearchClause{Breadth: breadth, Keys: []parser.Identifier{{Text: "n"}}, Sequence: parser.Identifier{Text: "seq"}}}}
	if err := b.bindSearchShape(d); err != nil {
		t.Fatal(err)
	}
	if err := searchNameError(d); err != nil {
		t.Fatal(err)
	}
	if distinct {
		if err := b.bindSearchIdentity(d); err != nil {
			t.Fatal(err)
		}
	}
	return b, d
}
func searchUnitValue(t *testing.T, s *plan.SearchRecipe, parent sqltype.Value, key sqltype.Value) sqltype.Value {
	t.Helper()
	value, err := s.Value(context.Background(), []sqltype.Value{key}, parent)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestQuerySearchShape(t *testing.T) {
	typ := cteUnitType(t)
	for _, breadth := range []bool{true, false} {
		_, d := searchUnitDefinition(t, breadth, typ.Usage(), false)
		s := d.Search
		if s.Slot != 1 || len(d.Schema) != 2 || s.Record.ID() != 2249 || s.Record.Usage().Typmod != -1 || s.Result.Usage().Collation.ID != 0 {
			t.Fatal("shape", s, d.Schema)
		}
		offset := 0
		if breadth {
			offset = 1
			member := s.Record.CompositeMember(0)
			if member.Name != "*DEPTH*" || member.Usage.Type.ID() != sqltype.Int8 || s.Result != s.Record {
				t.Fatal(member)
			}
		} else if s.Result.ID() != 2287 || s.Result.Element() != s.Record {
			t.Fatal("not record array")
		}
		if member := s.Record.CompositeMember(offset); member.Name != "n" || member.Usage != typ.Usage() {
			t.Fatal(member)
		}
		value := searchUnitValue(t, s, sqltype.Value{}, sqltype.Null(typ))
		if value.IsNull() {
			t.Fatal("whole NULL sequence")
		}
		record := value
		if !breadth {
			if value.ArrayRank() != 1 || value.ArrayDimension(0) != (sqltype.ArrayDimension{Lower: 1, Length: 1}) {
				t.Fatal("dimensions")
			}
			record = value.ArrayElement(0)
		}
		if record.IsNull() || !record.CompositeField(offset).IsNull() {
			t.Fatal("NULL field lost")
		}
	}
	// JSON shape construction must never demand grouping or comparison.
	json, _ := New().CatalogRead().LookupType(sqltype.JSON)
	_, d := searchUnitDefinition(t, true, json.Usage(), false)
	if len(d.Search.Grouping) != 0 {
		t.Fatal("ALL acquired equality")
	}
	searchUnitValue(t, d.Search, sqltype.Value{}, sqltype.Null(json))
	varchar, _ := New().CatalogRead().LookupType(sqltype.VarChar)
	usage := varchar.Usage()
	usage.Typmod = 11
	for _, breadth := range []bool{true, false} {
		_, typed := searchUnitDefinition(t, breadth, usage, false)
		member := typed.Search.Record.CompositeMember(typed.Search.Record.CompositeLen() - 1)
		if member.Usage != usage {
			t.Fatal("key typmod/collation erased", member)
		}
	}
}
func TestQuerySearchParent(t *testing.T) {
	typ := cteUnitType(t)
	one, two := cteUnitValue(t, typ, 1), cteUnitValue(t, typ, 2)
	for _, breadth := range []bool{true, false} {
		_, d := searchUnitDefinition(t, breadth, typ.Usage(), false)
		s := d.Search
		a := searchUnitValue(t, s, sqltype.Value{}, one)
		b := searchUnitValue(t, s, sqltype.Value{}, two)
		child := searchUnitValue(t, s, b, one)
		if breadth {
			depth, _ := child.CompositeField(0).Integer()
			key, _ := child.CompositeField(1).Integer()
			old, _ := a.CompositeField(0).Integer()
			if depth != 1 || key != 1 || old != 0 {
				t.Fatal(depth, key, old)
			}
		} else {
			key, _ := child.ArrayElement(0).CompositeField(0).Integer()
			if key != 2 || a.ArrayLen() != 1 || b.ArrayLen() != 1 || child.ArrayLen() != 2 {
				t.Fatal("wrong/mutated parent")
			}
		}
		self := &plan.WorktableScanNode{DerivedNode: plan.DerivedNode{CTE: d, Demand: []bool{false, true}}}
		physical := &plan.SingletonNode{QuerySchema: make(plan.QuerySchema, 4)}
		for _, tc := range []struct {
			node   plan.Node
			offset int
		}{{&plan.JoinNode{Left: physical, Right: self, LeftWidth: 4}, 4}, {&plan.JoinNode{Left: self, Right: physical, LeftWidth: 2}, 0}} {
			if scan, offset := recursiveParentSlot(tc.node, d, 0); scan != self || offset != tc.offset {
				t.Fatal(scan, offset)
			}
		}
	}
}
func TestQuerySearchIdentity(t *testing.T) {
	typ := cteUnitType(t)
	one := cteUnitValue(t, typ, 1)
	ctx := context.Background()
	for _, breadth := range []bool{true, false} {
		_, d := searchUnitDefinition(t, breadth, typ.Usage(), true)
		s := d.Search
		anchor := searchUnitValue(t, s, sqltype.Value{}, one)
		child := searchUnitValue(t, s, anchor, one)
		if same, err := s.Equal(ctx, anchor, child); err != nil || same {
			t.Fatal("generated identity omitted", same, err)
		}
		copy := searchUnitValue(t, s, sqltype.Value{}, one)
		ha, err := s.Hash(ctx, anchor)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := s.Hash(ctx, copy)
		if err != nil || ha != hb {
			t.Fatal("equal hashes", ha, hb, err)
		}
		grouping := make([]plan.Grouping, 1)
		grouping[0].Grouping, _ = sqltype.BindGrouping(typ.Usage())
		stage := exec.RecursiveUnionStage{Node: &plan.RecursiveUnionNode{SetNode: plan.SetNode{QuerySchema: d.Schema, Grouping: grouping}, Definition: d, Search: s}, Source: &exec.Source{Flat: []sqltype.Value{one, child, one, anchor}}}
		if index, err := stage.FindTuple(ctx, []sqltype.Value{one, anchor}, []int{0, 1}); err != nil || index != 1 {
			t.Fatal("collision ignored sequence", index, err)
		}
		cycle := cycleUnitRecipe(t, typ.Usage())
		cycle.MarkSlot = 2
		first := cycleUnitAugment(t, cycle, sqltype.Value{}, one, anchor)
		second := cycleUnitAugment(t, cycle, first[3], one, anchor)
		grouping = append(grouping, plan.Grouping{})
		grouping[1].Grouping, _ = sqltype.BindGrouping(cycle.Mark.ResultUsage())
		d.Cycle = cycle
		d.Schema = append(d.Schema, catalog.Column{Usage: cycle.Mark.ResultUsage()}, catalog.Column{Usage: cycle.Path.Usage()})
		stage.Node = &plan.RecursiveUnionNode{SetNode: plan.SetNode{QuerySchema: d.Schema, Grouping: grouping}, Definition: d, Search: s, Cycle: cycle}
		stage.Source.Flat = append(append([]sqltype.Value(nil), second...), first...)
		if index, err := stage.FindTuple(ctx, first, []int{0, 1}); err != nil || index != 1 {
			t.Fatal("collision ignored CYCLE", index, err)
		}
	}
}
func TestQuerySearchRemap(t *testing.T) {
	source, destination := New(), New()
	small, _ := source.CatalogRead().LookupType(sqltype.Int4)
	small, err := sqltype.Array(1007, "_int4", small)
	if err != nil {
		t.Fatal(err)
	}
	for _, breadth := range []bool{true, false} {
		_, d := searchUnitDefinition(t, breadth, small.Usage(), true)
		mapper := recordTypeMapper(func(old *sqltype.Descriptor) *sqltype.Descriptor {
			typ, ok := destination.CatalogRead().LookupType(old.ID())
			if !ok {
				t.Fatal(old.ID())
			}
			return typ
		})
		m := viewQueryRemapper{catalog: destination.CatalogRead(), typ: mapper}
		copy, err := m.cte(d)
		if err != nil {
			t.Fatal(err)
		}
		again, err := m.cte(d)
		if err != nil || again != copy {
			t.Fatal("CTE memo", err)
		}
		if copy == d || copy.Search == d.Search || copy.Search.Record == d.Search.Record || copy.Schema[1].Usage.Type != copy.Search.Result || copy.Search.Record.CompositeMember(copy.Search.Record.CompositeLen()-1).Usage.Type != mapper(small) {
			t.Fatal("foreign authority retained")
		}
		if d.Schema[1].Usage.Type != d.Search.Result || d.Search.Record.CompositeMember(d.Search.Record.CompositeLen()-1).Usage.Type != small {
			t.Fatal("source mutated")
		}
		if sqltype.SameRecordUsage(d.Search.Result.Usage(), copy.Search.Result.Usage()) {
			t.Fatal("same-OID shapes merged")
		}
		bound := plan.LiteralExpr{Info: expr.Info{Usage: small.Usage()}, Value: sqltype.Null(small)}
		left := &plan.ProjectNode{QuerySchema: d.Schema[:1]}
		right := &plan.ProjectNode{QuerySchema: d.Schema}
		seq := plan.ColumnExpr{Info: expr.Info{Usage: d.Search.Result.Usage()}}
		n := plan.SetNode{QuerySchema: d.Schema, Left: left, Right: right, Operation: "union", LeftExpressions: []expr.Expr{bound}, RightExpressions: []expr.Expr{bound, seq}, Grouping: make([]plan.Grouping, 1)}
		if err := validateStoredSetNode(&n, nil, d.Search); err != nil {
			t.Fatal(err)
		}
		bad := n
		bad.LeftExpressions = []expr.Expr{bound, seq}
		if err := validateStoredSetNode(&bad, nil, d.Search); err == nil {
			t.Fatal("accepted generated anchor target")
		}
		bad = n
		bad.RightExpressions = []expr.Expr{bound, bound}
		if err := validateStoredSetNode(&bad, nil, d.Search); err == nil {
			t.Fatal("accepted malformed parent")
		}
	}
}
func TestQuerySearchCancellation(t *testing.T) {
	typ := cteUnitType(t)
	one := cteUnitValue(t, typ, 1)
	_, d := searchUnitDefinition(t, false, typ.Usage(), true)
	s := d.Search
	value := searchUnitValue(t, s, sqltype.Value{}, one)
	for range 12 {
		value = searchUnitValue(t, s, value, one)
	}
	for _, operation := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := s.Value(ctx, []sqltype.Value{one}, value); return err },
		func(ctx context.Context) error { _, err := s.Hash(ctx, value); return err },
		func(ctx context.Context) error { _, err := s.Equal(ctx, value, value); return err },
	} {
		ctx := &cycleUnitCancelContext{Context: context.Background(), remaining: 4}
		if err := operation(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("loop ignored cancellation", err)
		}
	}
}
func TestQuerySearchSignature(t *testing.T) {
	binder := &expressionBinder{host: New().analysisHost(nil), ctx: context.Background()}
	small := binder.typ(sqltype.Int4)
	big := binder.typ(sqltype.Int8)
	for _, breadth := range []bool{true, false} {
		a := &plan.CTEDefinition{UserWidth: 1, Schema: plan.QuerySchema{{Name: "n", Usage: small.Usage()}}, Search: &plan.SearchRecipe{Syntax: &parser.SearchClause{Breadth: breadth, Keys: []parser.Identifier{{Text: "n"}}, Sequence: parser.Identifier{Text: "seq"}}}}
		b := &plan.CTEDefinition{UserWidth: 1, Schema: plan.QuerySchema{{Name: "n", Usage: big.Usage()}}, Search: &plan.SearchRecipe{Syntax: a.Search.Syntax}}
		for _, definition := range []*plan.CTEDefinition{a, b} {
			if err := binder.bindSearchShape(definition); err != nil {
				t.Fatal(err)
			}
		}
		left, right := a.Search.Result.Usage(), b.Search.Result.Usage()
		if !sameStatementResultUsage(left, right) || sameStatementUsage(left, right) || sqltype.SameRecordUsage(left, right) {
			t.Fatal("public/internal/parameter signatures conflated")
		}
		expected := StatementDescription{Results: []catalog.Column{{Name: "seq", Usage: left}}}
		actual := StatementDescription{Results: []catalog.Column{{Name: "seq", Usage: right}}}
		if err := checkStatementSignature(expected, actual); err != nil {
			t.Fatal(err)
		}
		expected.Parameters.Count = 1
		actual.Parameters.Count = 1
		expected.Parameters.Types = []sqltype.Usage{left}
		actual.Parameters.Types = []sqltype.Usage{right}
		if err := checkStatementSignature(expected, actual); err == nil {
			t.Fatal("parameter shape relaxed")
		}
	}
	if sameStatementResultUsage(small.Usage(), big.Usage()) {
		t.Fatal("scalar result widened")
	}
	named, err := sqltype.Composite(49000, "keytype", "public.keytype", 49001, []sqltype.CompositeMember{{Attribute: 1, Name: "n", Usage: small.Usage()}})
	if err != nil {
		t.Fatal(err)
	}
	for _, breadth := range []bool{true, false} {
		_, d := searchUnitDefinition(t, breadth, named.Usage(), false)
		expected := StatementDescription{catalogOwner: New().catalog.owner(), Results: []catalog.Column{{Name: "seq", Usage: d.Search.Result.Usage()}}}
		actual := expected
		actual.catalogOwner = New().catalog.owner()
		if err := checkStatementSignature(expected, actual); err == nil {
			t.Fatal("catalog leaf owner fence relaxed")
		}
	}
}
