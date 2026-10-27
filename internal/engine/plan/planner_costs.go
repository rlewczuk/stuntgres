package plan

import (
	"math"

	"stuntgres/internal/engine/ids"
)

// PlannerCosts is a command-admitted value, never a live settings provider.
// Only work implemented by the memory executor contributes to these models.
type PlannerCosts struct {
	Tuple, IndexTuple, Operator  float64
	ParallelSetup, ParallelTuple float64
	MinParallelTableBlocks       float64
}

func DefaultPlannerCosts() PlannerCosts {
	return PlannerCosts{Tuple: .01, IndexTuple: .005, Operator: .0025, ParallelSetup: 1000, ParallelTuple: .1, MinParallelTableBlocks: 1024}
}

type ScanMethod uint8

const (
	FullScan ScanMethod = iota
	HashCandidates
)

// ScanDecision owns only detached facts. Estimates are display-saturated only
// after comparing normalized work, so enormous valid settings do not cause ties.
type ScanDecision struct {
	Method                   ScanMethod
	Index                    ids.ObjectAddress
	IndexName                string
	FullCost, HashCost       float64
	FullStartup, HashStartup float64
	FullScore, HashScore     float64
}

func (d ScanDecision) Cost() float64 {
	if d.Method == HashCandidates {
		return d.HashCost
	}
	return d.FullCost
}

func (c PlannerCosts) Scan(rows, fraction float64, index ids.ObjectAddress, name string) ScanDecision {
	rows = max(0, rows)
	// Zero asks for very few rows, not an empty cursor.
	if fraction == 0 {
		fraction = 1e-10
	}
	scale := max(c.Tuple, c.IndexTuple, c.Operator)
	d := ScanDecision{Index: index, IndexName: name}
	if scale == 0 {
		return d
	}
	tuple, entry, operator := c.Tuple/scale, c.IndexTuple/scale, c.Operator/scale
	// One predicate per scanned row; one lookup, then entry equality and
	// original predicate recheck for each conservatively estimated candidate.
	full := rows * (tuple + operator)
	hash := operator + rows/2*(entry+tuple+2*operator)
	fullScore := fraction * full
	hashScore := operator + fraction*(hash-operator)
	if name != "" && hashScore < fullScore {
		d.Method = HashCandidates
	}
	d.FullCost = displayPlannerCost(full, scale)
	d.HashCost = displayPlannerCost(hash, scale)
	d.HashStartup = displayPlannerCost(operator, scale)
	d.FullScore = displayPlannerCost(fullScore, scale)
	d.HashScore = displayPlannerCost(hashScore, scale)
	return d
}

func displayPlannerCost(normalized, scale float64) float64 {
	if normalized == 0 || scale == 0 {
		return 0
	}
	if normalized >= ExplainEstimateLimit/scale {
		return ExplainEstimateLimit
	}
	return normalized * scale
}

// LocalParallel uses a logical header plus value slots, not physical disk size
// or measured allocations. The worker owner supplies its existing safety count.
func (c PlannerCosts) LocalParallel(rows float64, columns, workers int) bool {
	rows = max(0, rows)
	blocks := math.Ceil(rows * (24 + 8*float64(columns)) / 8192)
	if workers < 2 || blocks < c.MinParallelTableBlocks {
		return false
	}
	scale := max(c.Tuple, c.Operator, c.ParallelSetup, c.ParallelTuple)
	if scale == 0 {
		return false
	}
	work := rows * (c.Tuple/scale + c.Operator/scale)
	parallel := c.ParallelSetup/scale + work/float64(workers) + rows*(c.ParallelTuple/scale)
	return parallel < work
}
