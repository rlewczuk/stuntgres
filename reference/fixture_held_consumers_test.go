package reference_test

import (
	"database/sql"
	"fmt"
)

// Preserve the existing three-field held wrapper and shared assertion runner.
// Plain consumers require actual DB/Tx methods, never a Context stand-in.
func (s referenceHeldStatement) Prepare(query string) (*sql.Stmt, error) {
	source, ok := s.referenceExecutor.(referenceNonContextExecutor)
	if !ok {
		return nil, fmt.Errorf("non-Context held source requires DB or Tx methods, got %T", s.referenceExecutor)
	}
	return source.Prepare(query)
}
func (s referenceHeldStatement) Exec(query string, args ...any) (sql.Result, error) {
	source, ok := s.referenceExecutor.(referenceNonContextExecutor)
	if !ok {
		return nil, fmt.Errorf("non-Context held source requires DB or Tx methods, got %T", s.referenceExecutor)
	}
	if query == s.query {
		return s.statement.Exec(args...)
	}
	return source.Exec(query, args...)
}
func (s referenceHeldStatement) Query(query string, args ...any) (*sql.Rows, error) {
	source, ok := s.referenceExecutor.(referenceNonContextExecutor)
	if !ok {
		return nil, fmt.Errorf("non-Context held source requires DB or Tx methods, got %T", s.referenceExecutor)
	}
	if query == s.query {
		return s.statement.Query(args...)
	}
	return source.Query(query, args...)
}
