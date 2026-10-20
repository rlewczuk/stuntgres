package engine

import "stuntgres/internal/sqltype"

// FunctionRegexExecution is the expr.Context capability that lends regex
// nodes the statement's physical regex executor.
func (c *EvalContext) FunctionRegexExecution() *sqltype.PGRegexExecution {
	if host := c.liveHost(); host != nil {
		return host.FunctionRegexExecution()
	}
	return &c.pgRegex
}

// FunctionRegexExecution is the statement's shared regex presentation cache.
func (h *statementHost) FunctionRegexExecution() *sqltype.PGRegexExecution {
	return &h.scope().presentation().pgRegex
}
