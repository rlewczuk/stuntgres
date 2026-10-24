package exec

import (
	"fmt"
	"stuntgres/internal/engine/plan"
	"stuntgres/internal/sqltype"
)

// This is the scalar-subquery zero/one/second-row protocol with a tuple output.
// The statement's captured source, frames, CTEs, locks and hash owner are borrowed
// exactly as for scalar subqueries; no output is admitted before cardinality.
func (c *Context) subqueryUpdateOutputs(e plan.SubqueryExpr) (value sqltype.Value, err error) {
	if len(c.UpdateOutputs) != e.UpdateWidth {
		return sqltype.Value{}, fmt.Errorf("stuntgres: missing UPDATE tuple output owner")
	}
	state := State{Source: c.Query.Source, SourceMode: c.Query.subquerySourceMode(), Parameters: c.Parameters, Input: c.Input, Output: c.Output, Locking: c.Query.Locking, Outer: c, Ctes: c.Query.Ctes, hashOwner: c.Query.membershipOwner()}
	state.inheritAsyncAdmission(c.Query)
	stage, err := OpenQueryProjectStage(c.Context, &state, e.QueryPlan.Root.(*plan.ProjectNode))
	if err != nil {
		return sqltype.Value{}, err
	}
	defer func() {
		err = state.finishInvocation(&stage, err)
		if err != nil {
			c.UpdateOutputsReady = false
		}
	}()
	present, err := stage.Next(c.Context, c.UpdateOutputs)
	if err != nil {
		return sqltype.Value{}, err
	}
	if !present {
		for i, column := range stage.Schema() {
			c.UpdateOutputs[i] = sqltype.ResultNull(column.Usage)
		}
	} else {
		extra, failure := stage.NextProjected(c.Context, nil)
		if failure != nil {
			return sqltype.Value{}, failure
		}
		if extra {
			return sqltype.Value{}, sqlError("21000", "more than one row returned by a subquery used as an expression")
		}
	}
	c.UpdateOutputsReady = true
	return c.UpdateOutputs[0], nil
}
