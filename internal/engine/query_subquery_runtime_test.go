package engine

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/sqltype"
	"testing"
)

func TestQuerySubqueryCleanup(t *testing.T) {
	for _, phase := range []string{"producer", "projection", "close"} {
		t.Run(phase, func(t *testing.T) {
			primary := errors.New("primary")
			secondary := errors.New("close")
			closes := 0
			stage := exec.Stage{Source: &exec.Source{Singleton: true}, Finish: func() error { closes++; return secondary }}
			switch phase {
			case "producer":
				stage.Selection = []int{}
				stage.Produce = func(context.Context, *exec.Stage) (bool, error) { return false, primary }
			case "projection":
				stage.Columns = []catalog.Column{{}}
				stage.Expressions = []expr.Expr{queryProbe(sqltype.Usage{}, func(*exec.Context) (sqltype.Value, error) { return sqltype.Value{}, primary })}
				stage.Evaluation = exec.Context{EvalStatement: exec.EvalStatement{Context: context.Background()}}
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
