package enginetests_test

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	"stuntgres/internal/sqltype"
)

func TestPGRegexMatcherOwnership(t *testing.T) {
	program, err := sqltype.CompilePGRegex(context.Background(), `^(a+)(b)$`, sqltype.PGRegexOptions{}, sqltype.CollationRef{ID: 950})
	if err != nil {
		t.Fatal(err)
	}
	// Populate all source scratch before cloning. New instances must share no
	// populated spans, capture histories, state vectors or generation counters.
	original, err := program.Match(context.Background(), "aaab", true)
	if err != nil || !original.Matched || len(original.Captures) != 2 {
		t.Fatal(original, err)
	}
	instances := []*sqltype.PGRegexProgram{program}
	for range 8 {
		clone := program.NewMatcher()
		if clone == program {
			t.Fatal("shared mutable matcher")
		}
		instances = append(instances, clone)
	}
	var wg sync.WaitGroup
	failures := make(chan error, len(instances))
	for i, instance := range instances {
		wg.Go(func() {
			source := "ab"
			for range i {
				source = "a" + source
			}
			for range 20 {
				got, err := instance.Match(context.Background(), source, true)
				if err != nil || !got.Matched || len(got.Captures) != 2 || got.Captures[0] != (sqltype.PGRegexSpan{Start: 0, End: len(source) - 1}) {
					failures <- fmt.Errorf("matcher %d result=%+v err=%v", i, got, err)
					return
				}
			}
		})
	}
	wg.Wait()
	close(failures)
	for err := range failures {
		t.Error(err)
	}
	original, err = program.Match(context.Background(), "aaaab", true)
	if err != nil || !original.Matched {
		t.Fatal(original, err)
	}
	localBacking := program.NewMatcher()
	if _, err := localBacking.Match(context.Background(), "ab", true); err != nil {
		t.Fatal(err)
	}
	if original.Captures[0] != (sqltype.PGRegexSpan{Start: 0, End: 4}) {
		t.Fatal("clone mutated source capture backing", original)
	}
	canceled, cancel := context.WithCancel(context.Background())
	cancel()
	local := program.NewMatcher()
	if _, err := local.Match(canceled, "ab", true); !errors.Is(err, context.Canceled) {
		t.Fatal("matcher cancellation", err)
	}
	got, err := local.Match(context.Background(), "aab", true)
	if err != nil || !got.Matched || got.Captures[0] != (sqltype.PGRegexSpan{Start: 0, End: 2}) {
		t.Fatal("canceled instance poisoned reuse", got, err)
	}
}
