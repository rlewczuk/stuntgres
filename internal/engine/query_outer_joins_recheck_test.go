package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

// Nullable outer graphs cannot acquire an inherited enclosing rowmark. These
// package-function tests exercise the absent-frame reconstruction invariant
// independently of that public legality boundary.
func TestQueryOuterJoinsAbsentRecheck(t *testing.T) {
	for _, missingLeft := range []bool{false, true} {
		t.Run(map[bool]string{false: "right", true: "left"}[missingLeft], func(t *testing.T) {
			text := "SELECT * FROM a FULL JOIN (b FULL JOIN wide USING(k)) j USING(k)"
			if missingLeft {
				text = "SELECT * FROM (b FULL JOIN wide USING(k)) j FULL JOIN a USING(k)"
			}
			e, query, inputs := queryJoinsFixture(t, text)
			outer := query.Root.(*projectNode).child.(*joinNode)
			missing, present := outer.right, outer.left
			if missingLeft {
				missing, present = outer.left, outer.right
			}
			inner := missing.(*joinNode)
			boolType, _ := e.CatalogRead().LookupType(sqltype.Bool)
			forbidden := 0
			fail := func(*EvalContext) (sqltype.Value, error) {
				forbidden++
				return sqltype.Value{}, errors.New("missing subtree evaluated")
			}
			inner.predicate = queryProbe(boolType.Usage(), fail)
			for i := range inner.merges {
				inner.merges[i] = queryProbe(inner.merges[i].ResultUsage(), fail)
			}
			// Projection and order recipes of the missing subtree must also be
			// bypassed, including slots that do not name a physical input column.
			project := &projectNode{querySchema: inner.Schema(), child: inner}
			for _, column := range inner.Schema() {
				project.expressions = append(project.expressions, queryProbe(column.Usage, fail))
			}
			wrapped := &evaluationNode{querySchema: project.Schema(), child: project, expressions: []BoundExpr{queryProbe(boolType.Usage(), fail)}}
			if missingLeft {
				outer.left = wrapped
			} else {
				outer.right = wrapped
			}
			outer.predicate = queryProbe(boolType.Usage(), fail)
			locks := &queryLockContext{leaves: make([]queryLockLeaf, len(inputs.sources))}
			rows := make([][]sqltype.Value, len(inputs.sources))
			parentMerges := 0
			for i := range outer.merges {
				usage := outer.merges[i].ResultUsage()
				outer.merges[i] = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
					parentMerges++
					return sqltype.Null(usage.Type), nil
				})
			}
			for i, source := range inputs.sources {
				locks.leaves[i].absent = true
				rows[i] = source.row(0)
			}
			physical := present.(*sourceNode).occurrence
			locks.leaves[physical] = queryLockLeaf{changed: true}
			rows[physical] = append([]sqltype.Value(nil), rows[physical]...)
			// This is replacement data, not the captured original candidate.
			key := rows[physical][1]
			rows[physical][1] = sqltype.Null(key.Type())
			evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, statementScope: &StatementScope{queryLocks: locks}, orderValues: []sqltype.Value{sqltype.Boolean(boolType, true)}}
			scratch := make([]sqltype.Value, queryLockRecheckWidth(outer, inputs))
			offset := 0
			got, ok, err := recheckLockedQuery(outer, inputs, rows, &evaluation, scratch, &offset, make([]sqltype.Value, len(outer.Schema())))
			if err != nil || !ok || forbidden != 0 || len(got) != len(outer.Schema()) {
				t.Fatal(got, ok, err, forbidden)
			}
			start, end := outer.leftWidth, outer.leftWidth+outer.rightWidth
			if missingLeft {
				start, end = 0, outer.leftWidth
			}
			for i := start; i < end; i++ {
				if !reflect.DeepEqual(got[i], sqltype.Null(outer.Schema()[i].Usage.Type)) {
					t.Fatalf("missing slot %d: %#v, want typed NULL", i, got[i])
				}
			}
			if parentMerges != len(outer.merges) || len(outer.merges) == 0 {
				t.Fatal("parent merge recipes were not evaluated", parentMerges)
			}
			if missingLeft && !reflect.DeepEqual(got[end:end+len(rows[physical])], rows[physical]) || !missingLeft && !reflect.DeepEqual(got[:start], rows[physical]) {
				t.Fatal("replacement preserved row lost", got, rows[physical])
			}
		})
	}

	t.Run("real_null_and_zero_width", func(t *testing.T) {
		e := New()
		intType, _ := e.CatalogRead().LookupType(sqltype.Int4)
		boolType, _ := e.CatalogRead().LookupType(sqltype.Bool)
		for _, width := range []int{0, 1} {
			schema := make(querySchema, width)
			row := make([]sqltype.Value, width)
			if width != 0 {
				schema[0] = ColumnDescriptor{Usage: intType.Usage()}
				row[0] = sqltype.Null(intType)
			}
			inputs := &querySource{sources: []*querySource{{occurrence: 0}, {occurrence: 1}}}
			locks := &queryLockContext{leaves: []queryLockLeaf{{changed: true}, {changed: true}}}
			calls := 0
			join := &joinNode{querySchema: schema, left: &sourceNode{querySchema: schema, occurrence: 0}, right: &sourceNode{occurrence: 1}, leftWidth: width, kind: parser.JoinLeft, predicate: queryProbe(boolType.Usage(), func(*EvalContext) (sqltype.Value, error) {
				calls++
				return sqltype.Boolean(boolType, false), nil
			})}
			evaluation := EvalContext{statementScope: &StatementScope{queryLocks: locks}}
			offset := 0
			got, ok, err := recheckLockedQuery(join, inputs, [][]sqltype.Value{row, {}}, &evaluation, make([]sqltype.Value, width), &offset, make([]sqltype.Value, width))
			if err != nil || ok || got != nil || calls != 1 {
				t.Fatal("real occurrence mistaken for absent extension", width, got, ok, err, calls)
			}
		}
	})

	t.Run("stale_order_lineage", func(t *testing.T) {
		e := New()
		boolType, _ := e.CatalogRead().LookupType(sqltype.Bool)
		calls := 0
		recipe := queryProbe(boolType.Usage(), func(*EvalContext) (sqltype.Value, error) {
			calls++
			return sqltype.Value{}, errors.New("absent lineage recipe evaluated")
		})
		stage := &querySource{materialized: true}
		inner := &querySource{lineage: &queryLockLineage{stage: stage, currentIndex: 0, currentOrder: []sqltype.Value{sqltype.Boolean(boolType, true)}, root: &projectNode{child: &singletonNode{}, expressions: []BoundExpr{recipe}}}}
		bridge := &querySource{lineage: &queryLockLineage{stage: inner, currentIndex: 0, currentOrder: []sqltype.Value{sqltype.Boolean(boolType, true)}}}
		setQueryLineage(bridge, absentQueryIndex)
		if bridge.lineage.currentOrder != nil || inner.lineage.currentOrder != nil || inner.lineage.currentIndex != absentQueryIndex {
			t.Fatal("stale nested order lineage", bridge.lineage, inner.lineage)
		}
		inputs := &querySource{sources: []*querySource{bridge}}
		evaluation := EvalContext{statementScope: &StatementScope{queryLocks: &queryLockContext{}}}
		offset := 0
		got, ok, err := recheckLockedQuery(&sourceNode{occurrence: 0}, inputs, nil, &evaluation, nil, &offset, nil)
		if err != nil || !ok || got != nil || calls != 0 || offset != 0 {
			t.Fatal(got, ok, err, calls, offset)
		}
		got, ok, err = recheckLockedSource(bridge, nil, &evaluation, nil, &offset)
		if err != nil || !ok || got != nil || calls != 0 || offset != 0 {
			t.Fatal("absent source lineage dereferenced", got, ok, err, calls, offset)
		}
	})
}

func TestQueryOuterJoinsChangedPairRecheck(t *testing.T) {
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		_, query, inputs := queryJoinsFixture(t, "SELECT * FROM a LEFT JOIN b ON a.k=b.k")
		join := query.Root.(*projectNode).child.(*joinNode)
		join.kind = kind
		rows := [][]sqltype.Value{inputs.sources[0].row(0), inputs.sources[1].row(2)}
		locks := &queryLockContext{leaves: []queryLockLeaf{{changed: true}, {}}}
		evaluation := EvalContext{Context: context.Background(), Scope: 1, Frame: 1, statementScope: &StatementScope{queryLocks: locks}}
		offset := 0
		got, ok, err := recheckLockedQuery(join, inputs, rows, &evaluation, make([]sqltype.Value, queryLockRecheckWidth(join, inputs)), &offset, make([]sqltype.Value, len(join.Schema())))
		if err != nil || ok || got != nil {
			t.Fatal("changed real pair fabricated extension or searched another matching captured row", kind, got, ok, err)
		}
	}
}
