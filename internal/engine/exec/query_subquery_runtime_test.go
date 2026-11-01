package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqltype"
)

func TestQuerySubqueryCleanup(t *testing.T) {
	for _, phase := range []string{"producer", "projection", "close"} {
		t.Run(phase, func(t *testing.T) {
			primary := errors.New("primary")
			secondary := errors.New("close")
			closes := 0
			stage := Stage{Source: &Source{Singleton: true}, Finish: func() error { closes++; return secondary }}
			switch phase {
			case "producer":
				stage.Selection = []int{}
				stage.Produce = func(context.Context, *Stage) (bool, error) { return false, primary }
			case "projection":
				stage.Columns = []catalog.Column{{}}
				stage.Expressions = []expr.Expr{queryProbe(sqltype.Usage{}, func(*Context) (sqltype.Value, error) { return sqltype.Value{}, primary })}
				stage.Evaluation = Context{EvalStatement: EvalStatement{Context: context.Background()}}
			case "close":
				if err := stage.Close(); err != secondary {
					t.Fatal(err)
				}
			}
			if phase != "close" {
				var err error
				if phase == "producer" {
					_, _, err = stage.NextOccurrence(context.Background())
				} else {
					_, err = stage.Next(context.Background(), make([]sqltype.Value, 1))
				}
				if err != primary {
					t.Fatal("cleanup replaced primary failure", err)
				}
				if _, _, err = stage.NextOccurrence(context.Background()); err != primary {
					t.Fatal("terminal error not retained", err)
				}
			}
			if closes != 1 {
				t.Fatal(closes)
			}
			if err := stage.Close(); err != nil || closes != 1 {
				t.Fatal("non-idempotent close", err, closes)
			}
			if _, present, err := stage.NextOccurrence(context.Background()); present || err != nil {
				t.Fatal("closed stage reopened", present, err)
			}
		})
	}
}

func queryProbe(usage sqltype.Usage, eval func(*Context) (sqltype.Value, error)) expr.Expr {
	return queryProbeExpression{Info: expr.Info{Usage: usage, Pos: parser.Position{Offset: -1}}, eval: eval}
}

func TestNamedRelationDeferredPresence(t *testing.T) {
	if got := QueryTreeOccurrences(&plan.NamedRelationScanNode{PresenceSlot: 3}, nil, []int{7}); len(got) != 1 || got[0] != 7 {
		t.Fatal("named leaf invented a physical occurrence", got)
	}
	root := &plan.JoinNode{
		Left: &plan.ValuesNode{PresenceSlot: 1},
		Right: &plan.JoinNode{
			Left:  &plan.NamedRelationScanNode{PresenceSlot: 3},
			Right: &plan.FunctionFromNode{PresenceSlot: 5},
		},
	}
	var deferred Source
	establishDeferredPresence(&deferred, &plan.ProjectNode{Child: &plan.FilterNode{Child: root}})
	if deferred.PresenceCount != 3 || len(deferred.PresenceSlots) != 5 {
		t.Fatal("missing deferred named leaf", deferred.PresenceSlots, deferred.PresenceCount)
	}
	for i, want := range []int{1, 0, 2, 0, 3} {
		if deferred.PresenceSlots[i] != want {
			t.Fatal("incorrect packed topology", deferred.PresenceSlots)
		}
	}
	var left, named, function, right, actual Source
	EstablishSourcePresence(&left, 1)
	EstablishSourcePresence(&named, 3)
	EstablishSourcePresence(&function, 5)
	joinPresence(&right, &named, &function, 0)
	joinPresence(&actual, &left, &right, 0)
	if actual.PresenceCount != deferred.PresenceCount {
		t.Fatal("actual opening disagrees with deferred topology", actual.PresenceCount)
	}
	for i, slot := range actual.PresenceSlots {
		if slot != deferred.PresenceSlots[i] {
			t.Fatal("actual and borrowed deferred layouts disagree", actual.PresenceSlots, deferred.PresenceSlots)
		}
	}
	frame := Context{EvalFrame: EvalFrame{Scope: 1, Frame: 1, PresenceSlots: deferred.PresenceSlots, Presence: []bool{true, false, false}}}
	row := plan.RowExpr{PresenceFrame: 1, PresenceSlot: 3}
	if absent, err := frame.RowAbsent(row); err != nil || !absent {
		t.Fatal("null-extended named whole row was present", absent, err)
	}
	frame.Presence[1] = true
	if absent, err := frame.RowAbsent(row); err != nil || absent {
		t.Fatal("present all-null named whole row was absent", absent, err)
	}
	var undemanded Source
	establishDeferredPresence(&undemanded, &plan.NamedRelationScanNode{})
	if undemanded.PresenceCount != 0 || undemanded.PresenceSlots != nil {
		t.Fatal("undemanded named scan allocated presence slots")
	}
}
