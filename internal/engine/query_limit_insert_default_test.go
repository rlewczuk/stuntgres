package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitInsertDefaultDemand(t *testing.T) {
	for _, count := range []int64{0, 1} {
		t.Run(map[int64]string{0: "zero", 1: "one"}[count], func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t")
			run(t, e, "CREATE TABLE d(id INT,n INT DEFAULT 7)")
			ref := roleTableRef(t, e, "d")
			relation := e.CatalogRead().MustRelation(ref.Address)
			project := query.Root.(*plan.ProjectNode)
			project.Child = &plan.LimitNode{QuerySchema: project.Child.Schema(), Child: project.Child, Offset: queryLimitBound(t, e, 1), Count: queryLimitBound(t, e, count)}
			typ := relation.Columns[1].Usage.Type
			value, _ := sqltype.Integer(typ, 7)
			calls := 0
			probe := queryProbe(relation.Columns[1].Usage, func(*EvalContext) (sqltype.Value, error) {
				calls++
				return value, nil
			})
			bound := binding{relation: relation, query: query, targets: []int{0}, insert: insertBinding{defaults: []expr.Expr{nil, probe}}}
			rows, err := evaluateInsertQuery(context.Background(), bound, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
			if err != nil || len(rows.values) != int(count) || calls != int(count) || source.View != nil {
				t.Fatal(rows, err, calls, source.View)
			}
			if count != 0 {
				id, _ := rows.values[0][0].Integer()
				n, _ := rows.values[0][1].Integer()
				if id != 2 || n != 7 {
					t.Fatal(id, n)
				}
			}
		})
	}
}

func TestQueryLimitInsertDefaultFailure(t *testing.T) {
	e, query, source := queryCoreFixture(t, "SELECT id FROM t")
	run(t, e, "CREATE TABLE d(id INT,n INT DEFAULT 7)")
	ref := roleTableRef(t, e, "d")
	relation := e.CatalogRead().MustRelation(ref.Address)
	project := query.Root.(*plan.ProjectNode)
	project.Child = &plan.LimitNode{QuerySchema: project.Child.Schema(), Child: project.Child, Count: queryLimitBound(t, e, 2)}
	value, _ := sqltype.Integer(relation.Columns[1].Usage.Type, 7)
	failure := errors.New("retained runtime default failure")
	calls := 0
	probe := queryProbe(relation.Columns[1].Usage, func(*EvalContext) (sqltype.Value, error) {
		calls++
		if calls == 2 {
			return sqltype.Value{}, failure
		}
		return value, nil
	})
	bound := binding{relation: relation, query: query, targets: []int{0}, insert: insertBinding{defaults: []expr.Expr{nil, probe}}}
	rows, err := evaluateInsertQuery(context.Background(), bound, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{}, nil)
	// One destination row was assembled before the next retained default
	// failed. Neither that successful prefix nor the source pin may escape.
	if rows.values != nil || err != failure || calls != 2 || source.View != nil || source.Committed != nil || source.Own != nil {
		t.Fatal(rows, err, calls, source.View, source.Committed, source.Own)
	}
}
