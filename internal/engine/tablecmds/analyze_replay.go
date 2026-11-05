package tablecmds

import (
	"context"

	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
	"stuntgres/internal/sqltype"
)

func (segment *analyzeSegment) project(values []sqltype.Value) []sqltype.Value {
	if segment.layout == nil {
		return values
	}
	for slot, physical := range segment.layout {
		segment.scratch[slot] = values[physical]
	}
	return segment.scratch
}

// Replay the captured visible source using exactly the scalar pass's ranks.
// Native rows remain borrowed immutable source values; no row graph is retained.
func (segment *analyzeSegment) eachNative(ctx context.Context, yield func([]sqltype.Value) error) error {
	n, k := segment.source.Count(), segment.accepted
	for j := range k {
		if err := ctx.Err(); err != nil {
			return err
		}
		ordinal := j
		if k < n {
			ordinal = analyzeMidpoint(j, n, k)
		}
		if err := yield(segment.source.VersionAt(segment.source.RawIndex(ordinal)).Values); err != nil {
			return err
		}
	}
	return ctx.Err()
}

func (r *analyzeRun) buildExtended(evaluation *exec.Context, input *capturedExtendedInput, inherited bool, visits []*analyzeSegment, sampled *float64) (*storage.ExtendedStatistics, error) {
	captured := input.ExtendedInput
	captured.Key.Inherited = inherited
	captured.SamplePopulation = sampled
	collector, err := storage.NewExtendedCollector(captured)
	if err != nil {
		return nil, err
	}
	for _, segment := range visits {
		if segment.foreign != nil {
			for _, row := range segment.rows {
				if err := evaluation.Context.Err(); err != nil {
					return nil, err
				}
				if err := input.add(evaluation, collector, row); err != nil {
					return nil, err
				}
			}
		} else {
			if err := segment.eachNative(evaluation.Context, func(row []sqltype.Value) error {
				return input.add(evaluation, collector, segment.project(row))
			}); err != nil {
				return nil, err
			}
		}
	}
	fact, err := collector.Finish(evaluation.Context)
	if fact != nil && inherited {
		fact.Hierarchy = r.hierarchy
	}
	return fact, err
}
