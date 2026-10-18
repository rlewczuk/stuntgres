package engine

import "stuntgres/internal/sqltype"

// FunctionRegexExecution is the expr.Context capability that lends regex
// nodes the statement's physical regex executor.
func (c *EvalContext) FunctionRegexExecution() *sqltype.PGRegexExecution {
	if scope := c.liveScope(); scope != nil {
		return &scope.pgRegex
	}
	return &c.pgRegex
}
