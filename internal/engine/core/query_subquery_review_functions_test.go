package core_test

import (
	"context"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQuerySubquerySortedProjectionCacheOnce(t *testing.T) {
	_, q, source := queryJoinsFixture(t, "SELECT abs(k) FROM a ORDER BY a.k")
	project := q.Root.(*plan.ProjectNode)
	calls := 0
	subqueryTestTarget(t, project, queryProbe(project.Schema()[0].Usage, func(c *exec.Context) (sqltype.Value, error) {
		calls++
		return sqltype.Integer(project.Schema()[0].Usage.Type, int64(calls))
	}))
	state := exec.State{Source: source}
	stage, err := exec.OpenQueryProjectStage(context.Background(), &state, project)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	for range 2 {
		present, err := stage.NextProjected(context.Background(), nil)
		if err != nil || !present || calls != 4 {
			t.Fatal("sort input projection was skipped or reevaluated", present, err, calls)
		}
	}
}
