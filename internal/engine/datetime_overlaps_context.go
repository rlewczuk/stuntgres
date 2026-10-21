package engine

import (
	"stuntgres/internal/engine/analyze"
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/functions"
	"stuntgres/internal/engine/plan"
)

// Simplification of caller children precedes SQL wrapper evaluation/inlining.
// A partial constant body addition can fail during inlining even when another
// pair uses table columns. Stable zoned addition is never ordinarily folded.
func simplifyOverlapsRoute(c *exec.Context, e functions.DatetimeOverlaps) (functions.DatetimeOverlaps, bool, error) {
	old := e.WrapperContext
	oldFolded := e.Folded
	e.WrapperContext = !analyze.OverlapsInlineSafe(e)
	all := true
	for _, arg := range e.Args {
		if _, ok := arg.(plan.LiteralExpr); !ok {
			all = false
			break
		}
	}
	if all && !e.Plan.SessionDependent() {
		e.WrapperContext = true
		return e, e.WrapperContext != old, nil
	}
	if !e.WrapperContext && !e.Plan.SessionDependent() {
		for pair, duration := range e.Plan.Duration() {
			if !duration {
				continue
			}
			start, a := e.Args[pair*2].(plan.LiteralExpr)
			interval, b := e.Args[pair*2+1].(plan.LiteralExpr)
			if !a || !b {
				continue
			}
			if e.Folded[pair].Type() != nil {
				continue
			}
			if err := exec.AdmitOverlapsPair(c, e, pair); err != nil {
				return e, false, err
			}
			end, err := e.Plan.DurationEnd(c.Context, pair, start.Value, interval.Value, c.Input)
			if err != nil {
				return e, false, functions.OverlapsErrorContext(err, `SQL function "overlaps" during inlining`)
			}
			e.Folded[pair] = end
		}
	}
	return e, e.WrapperContext != old || e.Folded != oldFolded, nil
}
