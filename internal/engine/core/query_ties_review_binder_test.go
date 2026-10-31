package core_test

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/enginetest"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryTiesPresenceOnly(t *testing.T) {
	query, source := enginetest.QueryFixture(t, engine.New(), "SELECT id FROM t ORDER BY id+0 FETCH FIRST ROW WITH TIES")
	if !analyze.CanSimplifyExists(query) {
		t.Fatal("literal positive bound not eligible")
	}
	original := query.Root.(*plan.ProjectNode).Child.(*plan.LimitNode)
	limit := analyze.PresenceQueryInput(original).(*plan.LimitNode)
	ordered := limit.Child.(*plan.SortNode)
	if !limit.PresenceOnly || !ordered.PresenceOnly || limit.Count != original.Count || !limit.WithTies || len(limit.PeerKeys) != len(original.PeerKeys) {
		t.Fatal("presence transformation lost analyzed graph")
	}
	calls := 0
	failure := errors.New("ignored bounds or ORDER executed")
	limit.Count = enginetest.QueryProbe(limit.Count.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
	probe := enginetest.QueryProbe(ordered.Keys[0].Expression.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
	ordered.Keys = append([]plan.SortKey(nil), ordered.Keys...)
	ordered.Keys[0].Expression = probe
	limit.PeerKeys = ordered.Keys
	state := &exec.State{Source: source}
	stage, err := exec.OpenLimitNode(context.Background(), state, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	_, present, err := stage.NextOccurrence(context.Background())
	if err != nil || !present || calls != 0 || len(stage.OrderValues) != 0 {
		t.Fatal("presence demanded boundary/cache", present, err, calls, stage.OrderValues)
	}
}

func TestQueryTiesPresenceBoundEligibility(t *testing.T) {
	for _, count := range []string{"0", "$1"} {
		t.Run(count, func(t *testing.T) {
			query, _ := enginetest.QueryFixture(t, engine.New(), "SELECT id FROM t ORDER BY id+0 FETCH FIRST "+count+" ROWS WITH TIES")
			if analyze.CanSimplifyExists(query) {
				t.Fatal("ordinary bound incorrectly discarded")
			}
			limit := query.Root.(*plan.ProjectNode).Child.(*plan.LimitNode)
			if limit.PresenceOnly || limit.Child.(*plan.SortNode).PresenceOnly {
				t.Fatal("ordinary ORDER production bypassed")
			}
		})
	}
}
