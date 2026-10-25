package exec

import (
	"context"
	"errors"
	"testing"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
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
