package exec

import (
	"context"
	"errors"
	"stuntgres/internal/engine/plan"
	"testing"
)

func TestSetArmSourceOwnership(t *testing.T) {
	for _, shape := range []string{"union_all", "recursive_seed"} {
		for _, mode := range []struct {
			name  string
			value SourceMode
		}{{"one_shot", SourceOwned}, {"rowstream", SourceReusable}, {"borrowed", SourceBorrowed}} {
			t.Run(shape+"/"+mode.name, func(t *testing.T) {
				source, state, _ := viewProducerFixture(t, false)
				defer source.Close()
				columns := source.NestedRoot.Schema()
				// Admit one real view occurrence, retaining its uncaptured second row.
				reader, err := OpenSourceStage(context.Background(), state, source, columns)
				if err != nil {
					t.Fatal(err)
				}
				viewProducerPull(t, &reader, 1)
				if err := reader.Close(); err != nil {
					t.Fatal(err)
				}
				producer := source.NestedStage
				cleanup := errors.New("required set helper close")
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
				state.SourceMode = mode.value
				left := source.NestedRoot.(*plan.ProjectNode)
				right := &plan.ProjectNode{QuerySchema: columns, Child: &plan.ValuesNode{QuerySchema: columns}, Expressions: left.Expressions, Frame: 1}
				node := plan.SetNode{QuerySchema: columns, Left: left, Right: right, Operation: "union", All: true, LeftExpressions: left.Expressions, RightExpressions: right.Expressions}
				open := func() (Stage, error) {
					if shape == "union_all" {
						return OpenSetNode(context.Background(), state, &node)
					}
					definition := &plan.CTEDefinition{Owner: 1, Recursive: true, UserWidth: len(columns)}
					output := &Source{FlatWidth: len(columns), Materialized: true}
					owner := &CTEInvocation{Frame: 1, Producers: map[*plan.CTEDefinition]*CTEProducer{definition: {Source: output}}}
					state.Ctes = owner
					return openRecursiveUnion(context.Background(), state, &plan.RecursiveUnionNode{SetNode: node, Definition: definition})
				}
				arm, err := open()
				if err != nil {
					t.Fatal(err)
				}
				viewProducerPull(t, &arm, 1)
				err = arm.Close()
				if mode.value == SourceOwned {
					if !errors.Is(err, cleanup) || !producer.Closed || closes != 1 {
						t.Fatal("one-shot lost required close", err, closes)
					}
				} else {
					if err != nil || producer.Closed || closes != 0 || source.FlatCount != 1 {
						t.Fatal("nested arm completed enclosing capture", err, closes, source.FlatCount)
					}
					next, err := open()
					if err != nil {
						t.Fatal(err)
					}
					viewProducerPull(t, &next, 1)
					viewProducerPull(t, &next, 3)
					if source.NestedStage != producer || source.FlatCount != 2 {
						t.Fatal("lost single producer frontier", source.FlatCount)
					}
					if err := next.Close(); err != nil || closes != 0 {
						t.Fatal("reentry reader close", err, closes)
					}
					owner := State{Source: source, SourceMode: SourceReusable}
					if err := owner.FinishSources(nil); !errors.Is(err, cleanup) || closes != 1 || !producer.Closed {
						t.Fatal("actual owner lost veto", err, closes)
					}
				}
				if err := arm.Close(); err != nil {
					t.Fatal("repeated arm close", err)
				}
				source.Close()
				source.Close()
				if closes != 1 {
					t.Fatal("duplicate producer disposal", closes)
				}
			})
		}
	}
}
