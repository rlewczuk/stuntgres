package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryLimitInsertDefaultDemand(t *testing.T) {
	for _, count := range []int64{0, 1} {
		t.Run(map[int64]string{0: "zero", 1: "one"}[count], func(t *testing.T) {
			e, query, source := queryCoreFixture(t, "SELECT id FROM t")
			run(t, e, "CREATE TABLE d(id INT,n INT DEFAULT 7)")
			ref := roleTableRef(t, e, "d")
			relation := e.CatalogRead().state.descriptors[ref.Address].(*relationDescriptor)
			project := query.Root.(*projectNode)
			project.child = &limitNode{querySchema: project.child.Schema(), child: project.child, offset: queryLimitBound(t, e, 1), count: queryLimitBound(t, e, count)}
			typ := relation.columns[1].Usage.Type
			value, _ := sqltype.Integer(typ, 7)
			calls := 0
			probe := queryProbe(relation.columns[1].Usage, func(*EvalContext) (sqltype.Value, error) {
				calls++
				if calls > 1 {
					return sqltype.Value{}, errors.New("unselected default invocation")
				}
				return value, nil
			})
			bound := binding{relation: relation, query: query, targets: []int{0}, defaults: []BoundExpr{nil, probe}}
			rows, err := evaluateInsertQuery(context.Background(), bound, source, nil, "public", sqltype.InputEnvironment{}, sqltype.OutputEnvironment{})
			if err != nil || len(rows) != int(count) || calls != int(count) || source.view != nil {
				t.Fatal(rows, err, calls, source.view)
			}
			if count != 0 {
				id, _ := rows[0][0].Integer()
				n, _ := rows[0][1].Integer()
				if id != 2 || n != 7 {
					t.Fatal(id, n)
				}
			}
		})
	}
}
