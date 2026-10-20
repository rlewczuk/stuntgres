package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/lock"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryTiesPresenceOnly(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id+0 FETCH FIRST ROW WITH TIES")
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
	limit.Count = queryProbe(limit.Count.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
	probe := queryProbe(ordered.Keys[0].Expression.ResultUsage(), func(*exec.Context) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
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

func TestQueryTiesReplacementCache(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	for _, properties := range []struct {
		name       string
		properties expr.Properties
		refresh    bool
	}{
		{"row", expr.Properties{RowDependent: true}, true},
		{"stage", expr.Properties{StageDependent: true}, true},
		{"volatile", expr.Properties{Volatility: expr.Volatile}, true},
		{"stable independent", expr.Properties{Volatility: expr.Stable}, false},
	} {
		for _, mode := range []string{"ties hidden", "only hidden", "only visible"} {
			t.Run(properties.name+"/"+mode, func(t *testing.T) {
				calls := 0
				expr := queryProbeExpression{Info: expr.Info{Usage: typ.Usage(), Property: properties.properties}, eval: func(c *exec.Context) (sqltype.Value, error) { calls++; return c.Row[0], nil }}
				ordered := &plan.SortNode{Keys: []plan.SortKey{{Cache: 0, Expression: expr, Output: mode == "only visible"}, {Cache: 0, Expression: expr, Output: mode == "only visible"}}, CacheWidth: 1}
				if mode == "ties hidden" {
					limit := &plan.LimitNode{WithTies: true}
					limit.FinishTies(ordered)
				}
				evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}, EvalFrame: exec.EvalFrame{OrderValues: []sqltype.Value{one}}}
				if err := exec.RefreshLockedOrder(ordered, []sqltype.Value{two}, &evaluation); err != nil {
					t.Fatal(err)
				}
				wantCalls := 0
				wantValue := int64(1)
				if properties.refresh && mode != "only hidden" {
					wantCalls = 1
					wantValue = 2
				}
				value, err := evaluation.OrderValues[0].Integer()
				if err != nil || calls != wantCalls || value != wantValue {
					t.Fatal("replacement cache identity", calls, wantCalls, value, wantValue, err)
				}
			})
		}
	}
}

func TestQueryTiesQueuedReplacement(t *testing.T) {
	for _, key := range []string{"v+0", "v"} {
		t.Run(key, func(t *testing.T) {
			e, a, b, c := rowLockEngineFixture(t)
			sessionSQL(t, c, "INSERT INTO t VALUES(3,30),(4,40)")
			ar := transactionBegin(t, a)
			sessionSQL(t, a, "UPDATE t SET v=20 WHERE id=3")
			br := transactionBegin(t, b)
			done := queryLimitAsync(t, e, b, "SELECT id FROM t ORDER BY "+key+" FETCH FIRST 2 ROWS WITH TIES FOR UPDATE", false)
			rowLockQueued(t, e, a.Transactions.ticket, lock.ForUpdate)
			transactionEnd(t, a, ar, true)
			rowLockAwait(t, done, 1, 2, 3)
			rowLockViewGrants(t, e, b.Transactions.ticket, "t", 1, 2, 3, 4)
			transactionEnd(t, b, br, false)
			lockEmpty(t, e)
		})
	}
}

func TestQueryTiesReplacementFailure(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	boolean, _ := e.CatalogRead().LookupType(sqltype.Bool)
	one, _ := sqltype.Integer(typ, 1)
	for _, phase := range []string{"error", "type", "cancel", "physical slot"} {
		t.Run(phase, func(t *testing.T) {
			failure := errors.New("replacement key failure")
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			calls := 0
			expr := queryProbeExpression{Info: expr.Info{Usage: typ.Usage(), Property: expr.Properties{RowDependent: true}}, eval: func(c *exec.Context) (sqltype.Value, error) {
				calls++
				switch phase {
				case "type":
					return sqltype.Boolean(boolean, true), nil
				case "cancel":
					cancel()
					return sqltype.Value{}, c.Context.Err()
				default:
					return sqltype.Value{}, failure
				}
			}}
			slot := 0
			if phase == "physical slot" {
				slot = -1
			}
			ordered := &plan.SortNode{Keys: []plan.SortKey{{Cache: slot, Expression: expr}}, CacheWidth: 1}
			limit := &plan.LimitNode{WithTies: true}
			limit.FinishTies(ordered)
			evaluation := exec.Context{EvalStatement: exec.EvalStatement{Context: ctx}, EvalFrame: exec.EvalFrame{OrderValues: []sqltype.Value{one}}}
			err := exec.RefreshLockedOrder(ordered, []sqltype.Value{one}, &evaluation)
			if evaluation.OrderValues[0] != one {
				t.Fatal("failed key refresh published value")
			}
			if phase == "physical slot" {
				if err != nil || calls != 0 {
					t.Fatal("physical key reevaluated", err, calls)
				}
				return
			}
			if calls != 1 || err == nil {
				t.Fatal(err, calls)
			}
			switch phase {
			case "error":
				if !errors.Is(err, failure) {
					t.Fatal(err)
				}
			case "cancel":
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestQueryTiesPresenceBoundEligibility(t *testing.T) {
	for _, count := range []string{"0", "$1"} {
		t.Run(count, func(t *testing.T) {
			_, query, _ := queryCoreFixture(t, "SELECT id FROM t ORDER BY id+0 FETCH FIRST "+count+" ROWS WITH TIES")
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
