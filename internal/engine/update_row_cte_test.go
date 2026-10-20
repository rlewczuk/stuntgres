package engine

import (
	"context"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestUpdateRowCorrelatedCTEFrames(t *testing.T) {
	e := New()
	owner := sessionStart(t, e, nil)
	sessionSQL(t, owner, "CREATE TABLE t(id INT,a INT,b INT)")
	sessionSQL(t, owner, "INSERT INTO t VALUES(1,10,20),(2,30,40)")
	ctx := context.Background()
	op, scope, b, _, sources := updateFromBoundSources(t, e, owner, ctx, "UPDATE t SET (a,b)=(WITH v AS MATERIALIZED (SELECT t.id AS x) SELECT x,x+100 FROM v)")
	defer op.Done()
	defer scope.Finish(context.Canceled)
	if err := selectQuerySources(ctx, sources); err != nil {
		t.Fatal(err)
	}
	source := b.update.assignments[0].(subqueryExpression)
	project := source.QueryPlan.Root.(*projectNode)
	var producer *projectNode
	for _, analysis := range project.AnalysisExpressions {
		if edge, ok := analysis.(subqueryExpression); ok && edge.cteDefinition {
			producer = edge.QueryPlan.Root.(*projectNode)
			break
		}
	}
	if producer == nil || project.Frame != 2 || producer.Frame != 3 {
		t.Fatal("missing nested query frames", project.Frame, producer)
	}
	var target columnExpression
	found := false
	visitCTEQuery(producer, func(LogicalNode) {}, func(node expr.Expr) {
		if column, ok := node.(columnExpression); ok && column.Origin.Address.ObjectID == b.ref.Address.ObjectID {
			if column.Frame != 1 {
				t.Fatal("CTE correlation impersonated an empty intermediate SELECT frame", column.Frame)
			}
			target, found = column, true
		}
	}, true)
	if !found {
		t.Fatal("missing actual outer target recipe")
	}
	draws := 0
	subqueryTestTarget(t, producer, queryProbe(producer.Schema()[0].Usage, func(c *EvalContext) (sqltype.Value, error) {
		value, err := target.Eval(c)
		if err != nil {
			return sqltype.Value{}, err
		}
		id, err := value.Integer()
		if err != nil {
			return sqltype.Value{}, err
		}
		draws++
		return sqltype.Integer(value.Type(), id+int64(draws*10))
	}))
	execution := queryState{source: sources, input: scope.input, output: scope.output}
	defer execution.releaseMemberships()
	eval := execution.evaluation(ctx)
	typ := b.relation.Columns[0].Usage.Type
	for i, id := range []int64{1, 2, 1} {
		old := []sqltype.Value{subqueryTestInteger(t, typ, id), subqueryTestInteger(t, typ, 7), subqueryTestInteger(t, typ, 8)}
		eval.Row = old
		row, err := evaluateUpdateAssignments(&eval, b.update, b.targets, old)
		want := id + int64((i+1)*10)
		if err != nil || testExport(row[0]) != id || testExport(row[1]) != want || testExport(row[2]) != want+100 || draws != i+1 {
			t.Fatal("producer tuple was stale, rebound or duplicated", row, draws, err)
		}
		if eval.updateOutputs != nil || eval.updateOutputsReady {
			t.Fatal("correlated invocation leaked tuple owner")
		}
	}
}
