package exec

import (
	"context"
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/parser"
	"testing"
)

// The correlated-right execution contract reopens its subtree for each left
// occurrence. Even a conservatively marked subtree must borrow the enclosing
// capture rather than completing it between those invocations.
func TestCorrelatedJoinSourceOwnership(t *testing.T) {
	for _, mode := range []struct {
		name  string
		value SourceMode
	}{{"one_shot", SourceOwned}, {"rowstream", SourceReusable}, {"borrowed", SourceBorrowed}} {
		t.Run(mode.name, func(t *testing.T) {
			source, state, _ := viewProducerFixture(t, false)
			defer source.Close()
			columns := source.NestedRoot.Schema()
			reader, err := OpenSourceStage(context.Background(), state, source, columns)
			if err != nil {
				t.Fatal(err)
			}
			viewProducerPull(t, &reader, 1)
			if err := reader.Close(); err != nil {
				t.Fatal(err)
			}
			producer := source.NestedStage
			cleanup := errors.New("required correlated helper close")
			closes := 0
			finish := producer.Finish
			producer.Finish = func() error {
				closes++
				if finish != nil {
					if err := finish(); err != nil {
						return err
					}
				}
				return cleanup
			}
			values := make([][]expr.Expr, 2)
			for i, row := range source.NestedInputs.Anonymous {
				values[i] = []expr.Expr{plan.LiteralExpr{Info: expr.Info{Usage: columns[0].Usage}, Value: row[0]}}
			}
			state.SourceMode = mode.value
			node := &plan.JoinNode{QuerySchema: []catalog.Column{columns[0], columns[0]}, Kind: parser.JoinInner, Left: &plan.ValuesNode{QuerySchema: columns, Rows: values}, Right: &plan.SourceNode{QuerySchema: columns}, LeftWidth: 1, RightWidth: 1, CorrelatedRight: true, Frame: 1}
			joined, err := OpenJoinNode(context.Background(), state, node)
			if err != nil {
				t.Fatal(err)
			}
			want := [][2]int64{{1, 1}, {1, 3}, {3, 1}, {3, 3}}
			count := 0
			for {
				i, present, failure := joined.NextOccurrence(context.Background())
				if failure != nil {
					err = failure
					break
				}
				if !present {
					break
				}
				if count >= len(want) {
					t.Fatal("extra joined occurrence")
				}
				row := joined.Source.Row(joined.Index(i))
				for slot := range 2 {
					got, failure := row[slot].Integer()
					if failure != nil || got != want[count][slot] {
						t.Fatal("joined row/frontier", count, slot, got, failure)
					}
				}
				count++
			}
			closeErr := joined.Close()
			if mode.value == SourceOwned {
				if !errors.Is(err, cleanup) || closes != 1 || !producer.Closed {
					t.Fatal("one-shot close veto", err, closes)
				}
			} else {
				if err != nil || closeErr != nil || count != 4 || closes != 0 || producer.Closed || source.NestedStage != producer || source.FlatCount != 2 {
					t.Fatal("correlated reader completed capture", err, closeErr, count, closes, source.FlatCount)
				}
				owner := State{Source: source, SourceMode: SourceReusable}
				if err := owner.FinishSources(nil); !errors.Is(err, cleanup) || closes != 1 {
					t.Fatal("owner close veto", err, closes)
				}
			}
			source.Close()
			source.Close()
			if closes != 1 {
				t.Fatal("duplicate correlated producer disposal", closes)
			}
		})
	}
}
