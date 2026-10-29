package core

import (
	"stuntgres/internal/engine/dml"
)

// These aliases expose existing activation backing to composed owner tests only.
// No production observer, counter or alternate trigger admission is installed.
type BeforeStatementTestKey = afterStatementKey

func ScopeBeforeTestMap(s *StatementScope) map[BeforeStatementTestKey]struct{} {
	if s.afterTriggers == nil || s.afterTriggers.frames[s.effectCommand] == nil {
		return nil
	}
	return s.afterTriggers.frames[s.effectCommand].before
}
func BeforeStatementNativeTest(result dml.BeforeResult, err error) error {
	return beforeStatementNativeResult(result, err)
}
