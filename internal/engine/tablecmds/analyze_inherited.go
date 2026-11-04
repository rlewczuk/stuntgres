package tablecmds

import (
	"fmt"
	"math"
	"math/bits"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/storage"
)

// A retained visit is command-local work, not stored hierarchy authority. Even a
// zero-weight/zero-quota visit remains in this ordered list without acquisition.
type analyzeSegment struct {
	relation *catalog.Relation
	store    *storage.Relation
	source   *exec.Source
	foreign  *analyzeForeignSource
	sample   *analyzeForeignSample
	layout   []int
	dead     int64
	weight   float64
	accepted int
}

func (r *analyzeRun) inheritedSegments() ([]*analyzeSegment, bool, error) {
	visits := make([]*analyzeSegment, 0, len(r.closure))
	var native []*catalog.Relation
	mixed := false
	for _, relation := range r.closure {
		if relation.Partition != nil {
			continue
		}
		if relation.Foreign != nil {
			foreign, err := captureAnalyzeForeign(r.e, r.s, r.auth, relation)
			if err != nil {
				return nil, false, err
			}
			if foreign == nil {
				continue
			}
			layout, err := catalog.InheritanceQueryLayout(r.relation, relation)
			if err != nil {
				return nil, false, err
			}
			visits = append(visits, &analyzeSegment{relation: relation, foreign: foreign, layout: layout, weight: foreign.info.TotalPages})
			mixed = true
			continue
		}
		var segment *analyzeSegment
		for _, captured := range r.segments {
			if captured.relation.Address() == relation.Address() {
				segment = captured
				break
			}
		}
		if segment == nil {
			native = append(native, relation)
		}
		visits = append(visits, segment)
	}
	captured := len(r.segments)
	if err := r.captureNative(native); err != nil {
		return nil, false, err
	}
	// Fill the uncaptured native visits in structural order, not capture order.
	next := 0
	for i := range visits {
		if visits[i] == nil {
			visits[i] = r.segments[captured+next]
			next++
		}
		if source := visits[i].source; source != nil {
			n := source.Count()
			// Deterministic logical allocation weight, not physical heap pages.
			pages := n / 128
			if n%128 != 0 {
				pages++
			}
			visits[i].weight = float64(pages)
		}
	}
	return visits, mixed, nil
}

func (r *analyzeRun) sampleTarget() int {
	target := 100
	for _, column := range r.columns {
		target = max(target, 300*column.Target)
	}
	for _, input := range r.extended {
		// Only eligible captured objects contribute. Missing/disabled simple
		// keys retain old payload without increasing this pass's sample budget.
		target = max(target, 300*input.MCVTarget)
		for _, column := range input.Columns {
			target = max(target, 300*column.Target)
		}
		if input.Definition.Expressions.Len() != 0 {
			target = max(target, 30000)
		}
	}
	return target
}

func (r *analyzeRun) allocate(visits []*analyzeSegment) (float64, error) {
	maximum := float64(0)
	for _, visit := range visits {
		maximum = max(maximum, visit.weight)
	}
	if maximum == 0 {
		return 0, nil
	}
	// Normalization prevents a finite collection of finite weights overflowing
	// the total. At least one normalized weight is exactly one.
	total := float64(0)
	for _, visit := range visits {
		total += visit.weight / maximum
	}
	target, accepted := r.sampleTarget(), 0
	population, dead := float64(0), float64(0)
	for _, visit := range visits {
		if err := r.s.Op.Context().Err(); err != nil {
			return 0, err
		}
		quota := int(min(float64(target-accepted), math.RoundToEven(float64(target)*(visit.weight/maximum)/total)))
		if quota == 0 {
			continue
		}
		var live, removed float64
		if visit.foreign != nil {
			sample, err := visit.foreign.acquire(r.e, r.s, r.auth, quota, false)
			if err != nil {
				return 0, err
			}
			visit.sample = sample
			visit.accepted = len(sample.Rows)
			live, removed = sample.TotalRows, sample.TotalDeadRows
		} else {
			visit.accepted = min(quota, visit.source.Count())
			live, removed = float64(visit.source.Count()), float64(visit.dead)
		}
		accepted += visit.accepted
		population += live
		dead += removed
		if !validForeignEstimate(population) || !validForeignEstimate(dead) {
			return 0, fmt.Errorf("stuntgres: invalid inherited ANALYZE population")
		}
	}
	return population, nil
}

// floor((j+1/2)*n/k), using a double-width product so neither large source
// cardinalities nor intermediate multiplication can overflow a machine int.
func analyzeMidpoint(j, n, k int) int {
	hi, lo := bits.Mul64(uint64(2*j+1), uint64(n))
	rank, _ := bits.Div64(hi, lo, uint64(2*k))
	return int(rank)
}
