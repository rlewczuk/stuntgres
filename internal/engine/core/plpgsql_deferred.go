package core

import (
	"errors"
	"fmt"
	"slices"
	"strconv"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/engine/session"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// PreparePLRoutineLocked returns a privately owned executable for a legitimate
// source-only declaration. It never publishes/caches compiled code or changes a
// catalog definition. Caller holds Engine.Mu and owns the operation's authority.
func (e *Engine) PreparePLRoutineLocked(op *session.Operation, auth AuthorizationView, d *catalog.Procedure) (*catalog.Procedure, error) {
	if err := op.Context().Err(); err != nil {
		return nil, err
	}
	if d.SQL == nil || d.SQL.Kind != proc.PLpgSQLBody {
		return nil, fmt.Errorf("stuntgres: expected PL source declaration")
	}
	if d.SQL.PL != nil {
		return d, nil
	}
	private := *d
	definition := *d.SQL
	definition.Formals = slices.Clone(d.SQL.Formals)
	definition.Position = parser.Position{}
	private.SQL = &definition
	if err := e.CompilePLRoutineLocked(op, auth, &private); err != nil {
		var diagnostic *sqlstate.Error
		if !errors.As(err, &diagnostic) {
			return nil, err
		}
		next := *diagnostic
		line := 1
		if position, failure := strconv.Atoi(next.Position); failure == nil {
			next.InternalPosition, next.InternalQuery = next.Position, definition.Source
			for _, r := range definition.Source {
				position--
				if position <= 0 {
					break
				}
				if r == '\n' {
					line++
				}
			}
			next.Position = ""
		}
		if next.Context == "" {
			next.Context = fmt.Sprintf("compilation of PL/pgSQL function %q near line %d", d.Name(), line)
		}
		return nil, &next
	}
	if err := op.Context().Err(); err != nil {
		return nil, err
	}
	// The executable retains source coordinates for subsequent runtime errors,
	definition.Position = d.SQL.Position
	return &private, nil
}
