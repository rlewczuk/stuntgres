package engine

import (
	"context"
	"errors"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQueryTiesPresenceOnly(t *testing.T) {
	_, query, source := queryCoreFixture(t, "SELECT id FROM t ORDER BY id+0 FETCH FIRST ROW WITH TIES")
	if !canSimplifyExists(query) {
		t.Fatal("literal positive bound not eligible")
	}
	original := query.Root.(*projectNode).child.(*limitNode)
	limit := presenceQueryInput(original).(*limitNode)
	ordered := limit.child.(*sortNode)
	if !limit.presenceOnly || !ordered.presenceOnly || limit.count != original.count || !limit.withTies || len(limit.peerKeys) != len(original.peerKeys) {
		t.Fatal("presence transformation lost analyzed graph")
	}
	calls := 0
	failure := errors.New("ignored bounds or ORDER executed")
	limit.count = queryProbe(limit.count.ResultUsage(), func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
	probe := queryProbe(ordered.keys[0].expression.ResultUsage(), func(*EvalContext) (sqltype.Value, error) { calls++; return sqltype.Value{}, failure })
	ordered.keys = append([]sortKey(nil), ordered.keys...)
	ordered.keys[0].expression = probe
	limit.peerKeys = ordered.keys
	state := &queryState{source: source}
	stage, err := openLimitNode(context.Background(), state, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer stage.Close()
	_, present, err := stage.nextOccurrence(context.Background())
	if err != nil || !present || calls != 0 || len(stage.orderValues) != 0 {
		t.Fatal("presence demanded boundary/cache", present, err, calls, stage.orderValues)
	}
}

func TestQueryTiesReplacementCache(t *testing.T) {
	e := New()
	typ, _ := e.CatalogRead().LookupType(sqltype.Int4)
	one, _ := sqltype.Integer(typ, 1)
	two, _ := sqltype.Integer(typ, 2)
	for _, properties := range []struct {
		name       string
		properties ExprProperties
		refresh    bool
	}{
		{"row", ExprProperties{RowDependent: true}, true},
		{"stage", ExprProperties{StageDependent: true}, true},
		{"volatile", ExprProperties{Volatility: Volatile}, true},
		{"stable independent", ExprProperties{Volatility: Stable}, false},
	} {
		for _, mode := range []string{"ties hidden", "only hidden", "only visible"} {
			t.Run(properties.name+"/"+mode, func(t *testing.T) {
				calls := 0
				expr := queryProbeExpression{Info: expressionInfo{Usage: typ.Usage(), Property: properties.properties}, eval: func(c *EvalContext) (sqltype.Value, error) { calls++; return c.Row[0], nil }}
				ordered := &sortNode{keys: []sortKey{{cache: 0, expression: expr, output: mode == "only visible"}, {cache: 0, expression: expr, output: mode == "only visible"}}, cacheWidth: 1}
				if mode == "ties hidden" {
					limit := &limitNode{withTies: true}
					limit.finishTies(ordered)
				}
				evaluation := EvalContext{Context: context.Background(), orderValues: []sqltype.Value{one}}
				if err := refreshLockedOrder(ordered, []sqltype.Value{two}, &evaluation); err != nil {
					t.Fatal(err)
				}
				wantCalls := 0
				wantValue := int64(1)
				if properties.refresh && mode != "only hidden" {
					wantCalls = 1
					wantValue = 2
				}
				value, err := evaluation.orderValues[0].Integer()
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
			rowLockQueued(t, e, a.transactions.ticket, rowUpdate)
			transactionEnd(t, a, ar, true)
			rowLockAwait(t, done, 1, 2, 3)
			rowLockViewGrants(t, e, b.transactions.ticket, "t", 1, 2, 3, 4)
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
			expr := queryProbeExpression{Info: expressionInfo{Usage: typ.Usage(), Property: ExprProperties{RowDependent: true}}, eval: func(c *EvalContext) (sqltype.Value, error) {
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
			ordered := &sortNode{keys: []sortKey{{cache: slot, expression: expr}}, cacheWidth: 1}
			limit := &limitNode{withTies: true}
			limit.finishTies(ordered)
			evaluation := EvalContext{Context: ctx, orderValues: []sqltype.Value{one}}
			err := refreshLockedOrder(ordered, []sqltype.Value{one}, &evaluation)
			if evaluation.orderValues[0] != one {
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
			if canSimplifyExists(query) {
				t.Fatal("ordinary bound incorrectly discarded")
			}
			limit := query.Root.(*projectNode).child.(*limitNode)
			if limit.presenceOnly || limit.child.(*sortNode).presenceOnly {
				t.Fatal("ordinary ORDER production bypassed")
			}
		})
	}
}
