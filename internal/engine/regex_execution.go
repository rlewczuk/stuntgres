package engine

import "stuntgres/internal/sqltype"

// FunctionRegexExecution is the statement's shared regex presentation cache.
func (h *statementHost) FunctionRegexExecution() *sqltype.PGRegexExecution {
	return &h.scope().presentation().pgRegex
}
