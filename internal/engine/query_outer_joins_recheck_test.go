package engine

import (
	"context"
	"errors"
	"reflect"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
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
			outer := query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
			missing, present := outer.Right, outer.Left
			if missingLeft {
				missing, present = outer.Left, outer.Right
			}
			inner := missing.(*plan.JoinNode)
			boolType, _ := e.CatalogRead().LookupType(sqltype.Bool)
			forbidden := 0
			fail := func(*EvalContext) (sqltype.Value, error) {
				forbidden++
				return sqltype.Value{}, errors.New("missing subtree evaluated")
			}
			inner.Predicate = queryProbe(boolType.Usage(), fail)
			for i := range inner.Merges {
				inner.Merges[i] = queryProbe(inner.Merges[i].ResultUsage(), fail)
			}
			// Projection and order recipes of the missing subtree must also be
			// bypassed, including slots that do not name a physical input column.
			project := &plan.ProjectNode{QuerySchema: inner.Schema(), Child: inner}
			for _, column := range inner.Schema() {
				project.Expressions = append(project.Expressions, queryProbe(column.Usage, fail))
			}
			wrapped := &plan.EvaluationNode{QuerySchema: project.Schema(), Child: project, Expressions: []expr.Expr{queryProbe(boolType.Usage(), fail)}}
			if missingLeft {
				outer.Left = wrapped
			} else {
				outer.Right = wrapped
			}
			outer.Predicate = queryProbe(boolType.Usage(), fail)
			locks := &queryLockContext{Leaves: make([]queryLockLeaf, len(inputs.Sources))}
			rows := make([][]sqltype.Value, len(inputs.Sources))
			parentMerges := 0
			for i := range outer.Merges {
				usage := outer.Merges[i].ResultUsage()
				outer.Merges[i] = queryProbe(usage, func(*EvalContext) (sqltype.Value, error) {
					parentMerges++
					return sqltype.Null(usage.Type), nil
				})
			}
			for i, source := range inputs.Sources {
				locks.Leaves[i].Absent = true
				rows[i] = source.Row(0)
			}
			physical := present.(*plan.SourceNode).Occurrence
			locks.Leaves[physical] = queryLockLeaf{Changed: true}
			rows[physical] = append([]sqltype.Value(nil), rows[physical]...)
			// This is replacement data, not the captured original candidate.
			key := rows[physical][1]
			rows[physical][1] = sqltype.Null(key.Type())
			evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), Host: hostOf(&StatementScope{queryLocks: locks})}, evalFrame: evalFrame{Scope: 1, Frame: 1, OrderValues: []sqltype.Value{sqltype.Boolean(boolType, true)}}}
			scratch := make([]sqltype.Value, QueryLockRecheckWidth(outer, inputs))
			offset := 0
			got, ok, err := RecheckLockedQuery(outer, inputs, rows, &evaluation, scratch, &offset, make([]sqltype.Value, len(outer.Schema())))
			if err != nil || !ok || forbidden != 0 || len(got) != len(outer.Schema()) {
				t.Fatal(got, ok, err, forbidden)
			}
			start, end := outer.LeftWidth, outer.LeftWidth+outer.RightWidth
			if missingLeft {
				start, end = 0, outer.LeftWidth
			}
			for i := start; i < end; i++ {
				if !reflect.DeepEqual(got[i], sqltype.Null(outer.Schema()[i].Usage.Type)) {
					t.Fatalf("missing slot %d: %#v, want typed NULL", i, got[i])
				}
			}
			if parentMerges != len(outer.Merges) || len(outer.Merges) == 0 {
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
			schema := make(plan.QuerySchema, width)
			row := make([]sqltype.Value, width)
			if width != 0 {
				schema[0] = catalog.Column{Usage: intType.Usage()}
				row[0] = sqltype.Null(intType)
			}
			inputs := &querySource{Sources: []*querySource{{Occurrence: 0}, {Occurrence: 1}}}
			locks := &queryLockContext{Leaves: []queryLockLeaf{{Changed: true}, {Changed: true}}}
			calls := 0
			join := &plan.JoinNode{QuerySchema: schema, Left: &plan.SourceNode{QuerySchema: schema, Occurrence: 0}, Right: &plan.SourceNode{Occurrence: 1}, LeftWidth: width, Kind: parser.JoinLeft, Predicate: queryProbe(boolType.Usage(), func(*EvalContext) (sqltype.Value, error) {
				calls++
				return sqltype.Boolean(boolType, false), nil
			})}
			evaluation := EvalContext{evalStatement: evalStatement{Host: hostOf(&StatementScope{queryLocks: locks})}}
			offset := 0
			got, ok, err := RecheckLockedQuery(join, inputs, [][]sqltype.Value{row, {}}, &evaluation, make([]sqltype.Value, width), &offset, make([]sqltype.Value, width))
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
		stage := &querySource{Materialized: true}
		inner := &querySource{Lineage: &queryLockLineage{Stage: stage, CurrentIndex: 0, CurrentOrder: []sqltype.Value{sqltype.Boolean(boolType, true)}, Root: &plan.ProjectNode{Child: &plan.SingletonNode{}, Expressions: []expr.Expr{recipe}}}}
		bridge := &querySource{Lineage: &queryLockLineage{Stage: inner, CurrentIndex: 0, CurrentOrder: []sqltype.Value{sqltype.Boolean(boolType, true)}}}
		SetQueryLineage(bridge, AbsentQueryIndex)
		if bridge.Lineage.CurrentOrder != nil || inner.Lineage.CurrentOrder != nil || inner.Lineage.CurrentIndex != AbsentQueryIndex {
			t.Fatal("stale nested order lineage", bridge.Lineage, inner.Lineage)
		}
		inputs := &querySource{Sources: []*querySource{bridge}}
		evaluation := EvalContext{evalStatement: evalStatement{Host: hostOf(&StatementScope{queryLocks: &queryLockContext{}})}}
		offset := 0
		got, ok, err := RecheckLockedQuery(&plan.SourceNode{Occurrence: 0}, inputs, nil, &evaluation, nil, &offset, nil)
		if err != nil || !ok || got != nil || calls != 0 || offset != 0 {
			t.Fatal(got, ok, err, calls, offset)
		}
		got, ok, err = RecheckLockedSource(bridge, nil, &evaluation, nil, &offset)
		if err != nil || !ok || got != nil || calls != 0 || offset != 0 {
			t.Fatal("absent source lineage dereferenced", got, ok, err, calls, offset)
		}
	})
}

func TestQueryOuterJoinsChangedPairRecheck(t *testing.T) {
	for _, kind := range []parser.JoinKind{parser.JoinLeft, parser.JoinRight, parser.JoinFull} {
		_, query, inputs := queryJoinsFixture(t, "SELECT * FROM a LEFT JOIN b ON a.k=b.k")
		join := query.Root.(*plan.ProjectNode).Child.(*plan.JoinNode)
		join.Kind = kind
		rows := [][]sqltype.Value{inputs.Sources[0].Row(0), inputs.Sources[1].Row(2)}
		locks := &queryLockContext{Leaves: []queryLockLeaf{{Changed: true}, {}}}
		evaluation := EvalContext{evalStatement: evalStatement{Context: context.Background(), Host: hostOf(&StatementScope{queryLocks: locks})}, evalFrame: evalFrame{Scope: 1, Frame: 1}}
		offset := 0
		got, ok, err := RecheckLockedQuery(join, inputs, rows, &evaluation, make([]sqltype.Value, QueryLockRecheckWidth(join, inputs)), &offset, make([]sqltype.Value, len(join.Schema())))
		if err != nil || ok || got != nil {
			t.Fatal("changed real pair fabricated extension or searched another matching captured row", kind, got, ok, err)
		}
	}
}
