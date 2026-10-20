package engine

import (
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// This is the scalar-subquery zero/one/second-row protocol with a tuple output.
// The statement's captured source, frames, CTEs, locks and hash owner are borrowed
// exactly as for scalar subqueries; no output is admitted before cardinality.
func (c *EvalContext) subqueryUpdateOutputs(e plan.SubqueryExpr) (value sqltype.Value, err error) {
	if len(c.updateOutputs) != e.UpdateWidth {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing UPDATE tuple output owner")
	}
	state := queryState{source: c.query.source, parameters: c.Parameters, input: c.Input, output: c.Output, locking: c.query.locking, outer: c, ctes: c.query.ctes, hashOwner: c.query.membershipOwner()}
	stage, err := openQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() {
		if closeErr := stage.Close(); err == nil {
			err = closeErr
		}
		if err != nil {
			c.updateOutputsReady = false
		}
	}()
	present, err := stage.Next(c.Context, c.updateOutputs)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !present {
		for i, column := range stage.Schema() {
			c.updateOutputs[i] = sqltype.ResultNull(column.Usage)
		}
	} else {
		extra, failure := stage.nextProjected(c.Context, nil)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if extra {
			return sqltype.Value{}, sqlError("21000", "more than one row returned by a subquery used as an expression")
		}
	}
	c.updateOutputsReady = true
	return c.updateOutputs[0], nil
}
