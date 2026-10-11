package fixture

import (
	"database/sql"
	"fmt"
)

// Preserve the existing three-field held wrapper and shared assertion runner.
// Plain consumers require actual DB/Tx methods, never a Context stand-in.
func (s HeldStatement) Prepare(query string) (*sql.Stmt, error) {
	source, ok := s.Executor.(NonContextExecutor)
	if !ok {
		return nil, fmt.Errorf("non-Context held source requires DB or Tx methods, got %T", s.Executor)
	}
	return source.Prepare(query)
}
func (s HeldStatement) Exec(query string, args ...any) (sql.Result, error) {
	source, ok := s.Executor.(NonContextExecutor)
	if !ok {
		return nil, fmt.Errorf("non-Context held source requires DB or Tx methods, got %T", s.Executor)
	}
	if query == s.SQL {
		return s.Statement.Exec(args...)
	}
	return source.Exec(query, args...)
}
func (s HeldStatement) Query(query string, args ...any) (*sql.Rows, error) {
	source, ok := s.Executor.(NonContextExecutor)
	if !ok {
		return nil, fmt.Errorf("non-Context held source requires DB or Tx methods, got %T", s.Executor)
	}
	if query == s.SQL {
		return s.Statement.Query(args...)
	}
	return source.Query(query, args...)
}
