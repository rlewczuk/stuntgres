package enginetests

import (
	"context"
	"errors"
	"fmt"
	"testing"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

func TestQueryCTEModifierLifetime(t *testing.T) {
	for _, empty := range []bool{false, true} {
		t.Run(map[bool]string{false: "returning", true: "empty"}[empty], func(t *testing.T) {
			ctx := context.Background()
			definition := &plan.CTEDefinition{Owner: 1}
			producer := &exec.CTEProducer{Source: &exec.Source{Materialized: true}, MutationPending: true}
			starts, completions := 0, 0
			producer.StartMutation = func() error {
				starts++
				producer.MutationPending, producer.Done, producer.Closed = false, true, true
				if !empty { producer.Source.FlatCount = 1 }
				producer.CompleteMutation = func() error { completions++; return nil }
				return nil
			}
			owner := &exec.CTEInvocation{Frame: 1, Producers: map[*plan.CTEDefinition]*exec.CTEProducer{definition: producer}, Order: []*exec.CTEProducer{producer}}
			state := &exec.State{Ctes: owner, Source: &exec.Source{Materialized: true}}
			node := &plan.DerivedNode{CTE: definition}
			reader, err := exec.OpenCTEScan(ctx, state, node)
			if err != nil || starts != 0 || completions != 0 { t.Fatal(err, starts, completions) }
			_, present, err := reader.NextOccurrence(ctx)
			if err != nil || present == empty || starts != 1 { t.Fatal(present, err, starts) }
			if !empty && completions != 0 { t.Fatal("RETURNING row exhausted producer") }
			if err := reader.Close(); err != nil { t.Fatal(err) }
			if !empty && completions != 0 { t.Fatal("reader close exhausted shared producer") }
			// Another reader owns the same immutable materialization, not another
			// activation or another completion decision.
			second, err := exec.OpenCTEScan(ctx, state, node)
			if err != nil { t.Fatal(err) }
			for { _, more, err := second.NextOccurrence(ctx); if err != nil { t.Fatal(err) }; if !more { break } }
			if err := second.Close(); err != nil { t.Fatal(err) }
			if starts != 1 || completions != 1 { t.Fatal(starts, completions) }
			if err := owner.Close(); err != nil { t.Fatal(err) }
			if producer.StartMutation != nil || producer.CompleteMutation != nil { t.Fatal("closed owner retained executable callbacks") }
		})
	}
}

func TestQueryCTEModifierFailureLifetime(t *testing.T) {
	failure := errors.New("modifier completion failure")
	producer := &exec.CTEProducer{Source: &exec.Source{Materialized: true}, Done: true, Closed: true}
	completions := 0
	producer.CompleteMutation = func() error { completions++; return failure }
	for range 2 {
		if present, err := producer.Ensure(context.Background(), 0); present || err != failure { t.Fatal(present, err) }
	}
	if completions != 1 || producer.CompleteMutation != nil { t.Fatal(completions) }
	ctx, cancel := context.WithCancel(context.Background()); cancel()
	starts := 0
	unstarted := &exec.CTEProducer{Source: &exec.Source{Materialized: true}, StartMutation: func() error { starts++; return nil }}
	if _, err := unstarted.Ensure(ctx, 0); err != context.Canceled { t.Fatal(err) }
	if starts != 0 || unstarted.StartMutation != nil { t.Fatal("cancelled demand activated or retained modifier") }
}

func TestQueryCTEModifierTransferredLifetime(t *testing.T) {
	for _, failed := range []bool{false, true} {
		t.Run(map[bool]string{false: "complete", true: "discard"}[failed], func(t *testing.T) {
			producer := &exec.CTEProducer{Source: &exec.Source{Materialized: true}}
			owner := &exec.CTEInvocation{Order: []*exec.CTEProducer{producer}}
			completions := 0
			pending := func() error { completions++; return nil }
			producer.CompleteMutation = pending
			var rows exec.RowResult = exec.NewFinalRows(nil, 0)
			if failed { rows = &cteFailedRows{RowResult: rows} }
			retained := &exec.CTEOwnedRows{RowResult: rows, Owner: owner, Source: &exec.Source{}, Complete: pending}
			_, err := retained.Next(context.Background(), nil)
			if failed != (err != nil) { t.Fatal(err) }
			if completions != 0 { t.Fatal("auxiliary completed before main demand finished") }
			if err := retained.Close(); err != nil { t.Fatal(err) }
			want := 1
			if failed { want = 0 }
			if completions != want || !owner.Closed || retained.Complete != nil || producer.CompleteMutation != nil { t.Fatal(completions, owner.Closed) }
			if err := retained.Close(); err != nil || completions != want { t.Fatal(err, completions) }
		})
	}
}

type cteFailedRows struct { exec.RowResult }

func (*cteFailedRows) Next(context.Context, []sqltype.Value) (bool, error) {
	return false, errors.New("primary demand failure")
}

func TestQueryCTEModifierCompletionCauses(t *testing.T) {
	for _, abort := range []bool{false, true} {
		for _, closeFails := range []bool{false, true} {
			t.Run(fmt.Sprintf("abort_%t/cleanup_%t", abort, closeFails), func(t *testing.T) {
				executionFailure := errors.New("ordinary modifier execution")
				cleanupFailure := errors.New("resource close")
				producer := &exec.CTEProducer{Source: &exec.Source{Materialized: true}, Done: true, Opened: true}
				closes, completions := 0, 0
				producer.Inner.Finish = func() error {
					closes++
					if closeFails { return cleanupFailure }
					return nil
				}
				producer.CompleteMutation = func() error { completions++; return executionFailure }
				owner := &exec.CTEInvocation{Order: []*exec.CTEProducer{producer}}
				retained := &exec.CTEOwnedRows{
					RowResult: exec.NewFinalRows(nil, 0), Owner: owner, Source: &exec.Source{},
					Complete: func() error { _, err := producer.Ensure(context.Background(), 0); return err },
				}
				var primary error
				if abort { primary = context.Canceled }
				execution, cleanup := retained.Finish(primary)
				if abort {
					if execution != nil || completions != 0 { t.Fatal("aborted auxiliary executed", execution, completions) }
				} else if !errors.Is(execution, executionFailure) || completions != 1 {
					t.Fatal("lost ordinary completion cause", execution, completions)
				}
				if errors.Is(cleanup, executionFailure) || closeFails != errors.Is(cleanup, cleanupFailure) {
					t.Fatal("execution and resource errors conflated", execution, cleanup)
				}
				if closes != 1 || !owner.Closed || retained.Owner != nil || retained.Source != nil || retained.Complete != nil || producer.CompleteMutation != nil {
					t.Fatal("retained or repeated owner resources", closes, owner.Closed)
				}
				if execution, cleanup := retained.Finish(nil); execution != nil || cleanup != nil || closes != 1 {
					t.Fatal("completion replay", execution, cleanup, closes)
				}
			})
		}
	}
}
