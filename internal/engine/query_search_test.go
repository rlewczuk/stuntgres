package engine

import (
	"context"
	"errors"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
	"testing"
)

func searchUnitDefinition(t *testing.T, breadth bool, usage sqltype.Usage, distinct bool) (*expressionBinder, *cteDefinition) {
	t.Helper()
	b := &expressionBinder{engine: New(), ctx: context.Background()}
	d := &cteDefinition{userWidth: 1, schema: querySchema{{Name: "n", Usage: usage}}, search: &searchRecipe{syntax: &parser.SearchClause{Breadth: breadth, Keys: []parser.Identifier{{Text: "n"}}, Sequence: parser.Identifier{Text: "seq"}}}}
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
func searchUnitValue(t *testing.T, s *searchRecipe, parent sqltype.Value, key sqltype.Value) sqltype.Value {
	t.Helper()
	value, err := s.value(context.Background(), []sqltype.Value{key}, parent)
	if err != nil {
		t.Fatal(err)
	}
	return value
}
func TestQuerySearchShape(t *testing.T) {
	typ := cteUnitType(t)
	for _, breadth := range []bool{true, false} {
		_, d := searchUnitDefinition(t, breadth, typ.Usage(), false)
		s := d.search
		if s.slot != 1 || len(d.schema) != 2 || s.record.ID() != 2249 || s.record.Usage().Typmod != -1 || s.result.Usage().Collation.ID != 0 {
			t.Fatal("shape", s, d.schema)
		}
		offset := 0
		if breadth {
			offset = 1
			member := s.record.CompositeMember(0)
			if member.Name != "*DEPTH*" || member.Usage.Type.ID() != sqltype.Int8 || s.result != s.record {
				t.Fatal(member)
			}
		} else if s.result.ID() != 2287 || s.result.Element() != s.record {
			t.Fatal("not record array")
		}
		if member := s.record.CompositeMember(offset); member.Name != "n" || member.Usage != typ.Usage() {
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
	if len(d.search.grouping) != 0 {
		t.Fatal("ALL acquired equality")
	}
	searchUnitValue(t, d.search, sqltype.Value{}, sqltype.Null(json))
	varchar, _ := New().CatalogRead().LookupType(sqltype.VarChar)
	usage := varchar.Usage()
	usage.Typmod = 11
	for _, breadth := range []bool{true, false} {
		_, typed := searchUnitDefinition(t, breadth, usage, false)
		member := typed.search.record.CompositeMember(typed.search.record.CompositeLen() - 1)
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
		s := d.search
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
		self := &worktableScanNode{derivedNode: derivedNode{cte: d, demand: []bool{false, true}}}
		physical := &singletonNode{querySchema: make(querySchema, 4)}
		for _, tc := range []struct {
			node   LogicalNode
			offset int
		}{{&joinNode{left: physical, right: self, leftWidth: 4}, 4}, {&joinNode{left: self, right: physical, leftWidth: 2}, 0}} {
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
		s := d.search
		anchor := searchUnitValue(t, s, sqltype.Value{}, one)
		child := searchUnitValue(t, s, anchor, one)
		if same, err := s.equal(ctx, anchor, child); err != nil || same {
			t.Fatal("generated identity omitted", same, err)
		}
		copy := searchUnitValue(t, s, sqltype.Value{}, one)
		ha, err := s.hash(ctx, anchor)
		if err != nil {
			t.Fatal(err)
		}
		hb, err := s.hash(ctx, copy)
		if err != nil || ha != hb {
			t.Fatal("equal hashes", ha, hb, err)
		}
		grouping := make([]queryGrouping, 1)
		grouping[0].Grouping, _ = sqltype.BindGrouping(typ.Usage())
		stage := recursiveUnionStage{node: &recursiveUnionNode{setNode: setNode{querySchema: d.schema, grouping: grouping}, definition: d, search: s}, source: &querySource{flat: []sqltype.Value{one, child, one, anchor}}}
		if index, err := stage.findTuple(ctx, []sqltype.Value{one, anchor}, []int{0, 1}); err != nil || index != 1 {
			t.Fatal("collision ignored sequence", index, err)
		}
		cycle := cycleUnitRecipe(t, typ.Usage())
		cycle.markSlot = 2
		first := cycleUnitAugment(t, cycle, sqltype.Value{}, one, anchor)
		second := cycleUnitAugment(t, cycle, first[3], one, anchor)
		grouping = append(grouping, queryGrouping{})
		grouping[1].Grouping, _ = sqltype.BindGrouping(cycle.mark.ResultUsage())
		d.cycle = cycle
		d.schema = append(d.schema, ColumnDescriptor{Usage: cycle.mark.ResultUsage()}, ColumnDescriptor{Usage: cycle.path.Usage()})
		stage.node = &recursiveUnionNode{setNode: setNode{querySchema: d.schema, grouping: grouping}, definition: d, search: s, cycle: cycle}
		stage.source.flat = append(append([]sqltype.Value(nil), second...), first...)
		if index, err := stage.findTuple(ctx, first, []int{0, 1}); err != nil || index != 1 {
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
		if copy == d || copy.search == d.search || copy.search.record == d.search.record || copy.schema[1].Usage.Type != copy.search.result || copy.search.record.CompositeMember(copy.search.record.CompositeLen()-1).Usage.Type != mapper(small) {
			t.Fatal("foreign authority retained")
		}
		if d.schema[1].Usage.Type != d.search.result || d.search.record.CompositeMember(d.search.record.CompositeLen()-1).Usage.Type != small {
			t.Fatal("source mutated")
		}
		if sameRecordUsage(d.search.result.Usage(), copy.search.result.Usage()) {
			t.Fatal("same-OID shapes merged")
		}
		expr := literalExpression{expressionInfo: expressionInfo{usage: small.Usage()}, value: sqltype.Null(small)}
		left := &projectNode{querySchema: d.schema[:1]}
		right := &projectNode{querySchema: d.schema}
		seq := columnExpression{expressionInfo: expressionInfo{usage: d.search.result.Usage()}}
		n := setNode{querySchema: d.schema, left: left, right: right, operation: "union", leftExpressions: []BoundExpr{expr}, rightExpressions: []BoundExpr{expr, seq}, grouping: make([]queryGrouping, 1)}
		if err := validateStoredSetNode(&n, nil, d.search); err != nil {
			t.Fatal(err)
		}
		bad := n
		bad.leftExpressions = []BoundExpr{expr, seq}
		if err := validateStoredSetNode(&bad, nil, d.search); err == nil {
			t.Fatal("accepted generated anchor target")
		}
		bad = n
		bad.rightExpressions = []BoundExpr{expr, expr}
		if err := validateStoredSetNode(&bad, nil, d.search); err == nil {
			t.Fatal("accepted malformed parent")
		}
	}
}
func TestQuerySearchCancellation(t *testing.T) {
	typ := cteUnitType(t)
	one := cteUnitValue(t, typ, 1)
	_, d := searchUnitDefinition(t, false, typ.Usage(), true)
	s := d.search
	value := searchUnitValue(t, s, sqltype.Value{}, one)
	for range 12 {
		value = searchUnitValue(t, s, value, one)
	}
	for _, operation := range []func(context.Context) error{
		func(ctx context.Context) error { _, err := s.value(ctx, []sqltype.Value{one}, value); return err },
		func(ctx context.Context) error { _, err := s.hash(ctx, value); return err },
		func(ctx context.Context) error { _, err := s.equal(ctx, value, value); return err },
	} {
		ctx := &cycleUnitCancelContext{Context: context.Background(), remaining: 4}
		if err := operation(ctx); !errors.Is(err, context.Canceled) {
			t.Fatal("loop ignored cancellation", err)
		}
	}
}
func TestQuerySearchSignature(t *testing.T) {
	binder := &expressionBinder{engine: New(), ctx: context.Background()}
	small := binder.typ(sqltype.Int4)
	big := binder.typ(sqltype.Int8)
	for _, breadth := range []bool{true, false} {
		a := &cteDefinition{userWidth: 1, schema: querySchema{{Name: "n", Usage: small.Usage()}}, search: &searchRecipe{syntax: &parser.SearchClause{Breadth: breadth, Keys: []parser.Identifier{{Text: "n"}}, Sequence: parser.Identifier{Text: "seq"}}}}
		b := &cteDefinition{userWidth: 1, schema: querySchema{{Name: "n", Usage: big.Usage()}}, search: &searchRecipe{syntax: a.search.syntax}}
		for _, definition := range []*cteDefinition{a, b} {
			if err := binder.bindSearchShape(definition); err != nil {
				t.Fatal(err)
			}
		}
		left, right := a.search.result.Usage(), b.search.result.Usage()
		if !sameStatementResultUsage(left, right) || sameStatementUsage(left, right) || sameRecordUsage(left, right) {
			t.Fatal("public/internal/parameter signatures conflated")
		}
		expected := StatementDescription{Results: []ColumnDescriptor{{Name: "seq", Usage: left}}}
		actual := StatementDescription{Results: []ColumnDescriptor{{Name: "seq", Usage: right}}}
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
		expected := StatementDescription{catalogOwner: New(), Results: []ColumnDescriptor{{Name: "seq", Usage: d.search.result.Usage()}}}
		actual := expected
		actual.catalogOwner = New()
		if err := checkStatementSignature(expected, actual); err == nil {
			t.Fatal("catalog leaf owner fence relaxed")
		}
	}
}
