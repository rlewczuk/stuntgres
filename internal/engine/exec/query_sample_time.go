package exec

import (
	"context"
	"math"
	"sort"
	"time"

	"stuntgres/internal/engine/ids"
)

type sampleBudgetKey struct{}

// This is cooperative scan control, not a context cancellation or SQL error.
// The pointer identifies its owning invocation across nested sample stages.
type sampleBudget struct {
	parent *sampleBudget
	start time.Time
	millis float64
	source *Source
	physical ids.ObjectRef
	cursor *Stage
}

func (*sampleBudget) Error() string { return "stuntgres: sample scan budget exhausted" }

func sampleCheckpoint(ctx context.Context) error {
	if ctx == nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	budget, _ := ctx.Value(sampleBudgetKey{}).(*sampleBudget)
	return checkSampleBudget(budget)
}

func checkSampleBudget(budget *sampleBudget) error {
	if budget == nil {
		return nil
	}
	if err := checkSampleBudget(budget.parent); err != nil {
		return err
	}
	if !budget.start.IsZero() && time.Since(budget.start).Seconds()*1000 >= budget.millis {
		return budget
	}
	return nil
}

// Record actual physical traversal, including rows rejected inside lazy RLS.
// Only this sampler's source may advance its physical group; nested producers
// inherit the enclosing clock, rather than restarting it for unrelated sources.
func sampleDemand(ctx context.Context, stage *Stage) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	budget, _ := ctx.Value(sampleBudgetKey{}).(*sampleBudget)
	for current := budget; current != nil; current = current.parent {
		if stage.Source != current.source || stage.Produce != nil || stage.Position >= stage.Count() {
			continue
		}
		physical := stage.Source.PhysicalRef(stage.Index(stage.Position))
		if current.physical != physical {
			if current.physical != (ids.ObjectRef{}) || current.start.IsZero() {
				current.start = time.Now()
			}
			current.physical = physical
		}
		current.cursor = stage
	}
	return checkSampleBudget(budget)
}

// Move past the remaining visible occurrences of an exhausted physical RTE.
// Source layout and visibility vectors are borrowed immutable inputs. A lazy
// security wrapper retains its accepted prefix and resumes at this raw frontier.
func (b *sampleBudget) nextPhysical() bool {
	child := b.cursor
	if child == nil || b.source.Inheritance == nil {
		return false
	}
	start := child.Position
	end := start + sort.Search(child.Count()-start, func(offset int) bool {
		return child.Source.PhysicalRef(child.Index(start+offset)) != b.physical
	})
	child.Position = end
	b.start, b.physical = time.Time{}, ids.ObjectRef{}
	return end < child.Count()
}

func selectSampleTime(ctx context.Context, out, child *Stage, millis float64) error {
	out.Selection, out.CacheOccurrences, out.OrderValues = []int{}, nil, nil
	out.Permutation = nil
	out.scanRange, out.scanStart, out.scanEnd = false, 0, 0
	if millis == 0 {
		return ctx.Err()
	}
	if math.IsInf(millis, 1) {
		// No local clock or context is needed for an unbounded finite scan.
		// Enclosing scan budgets and ordinary cancellation still apply.
		for {
			i, present, err := child.NextOccurrence(ctx)
			if err != nil {
				return err
			}
			if !present {
				return ctx.Err()
			}
			RetainOccurrence(out, child, i)
		}
	}
	parent, _ := ctx.Value(sampleBudgetKey{}).(*sampleBudget)
	physicalSource := child.Source
	for physicalSource.Input != nil {
		physicalSource = physicalSource.Input
	}
	budget := &sampleBudget{parent: parent, start: time.Now(), millis: millis, source: physicalSource}
	scan := context.WithValue(ctx, sampleBudgetKey{}, budget)
	for {
		i, present, err := child.NextOccurrence(scan)
		if err == budget {
			if err := ctx.Err(); err != nil {
				return err
			}
			if budget.nextPhysical() {
				continue
			}
			return nil
		}
		if err != nil {
			return err
		}
		if !present {
			return ctx.Err()
		}
		// A completed indivisible policy/codec can cross the clock boundary.
		// Its genuine failure above remains primary; cancellation wins over EOF.
		if err := sampleCheckpoint(scan); err != nil {
			if err != budget {
				return err
			}
			if budget.nextPhysical() {
				continue
			}
			return ctx.Err()
		}
		RetainOccurrence(out, child, i)
	}
}
