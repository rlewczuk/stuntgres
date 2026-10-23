package engine

import (
	"errors"
	"strconv"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) executePLConditional(d *catalog.Procedure, f *proc.PLFrame, instruction proc.PLInstruction, read catalog.Read) (sqltype.Value, bool, error) {
	checked, raw, action := instruction.If, instruction.Syntax.If, "at IF"
	isCase := instruction.Case != nil
	if isCase {
		checked, raw, action = instruction.Case, instruction.Syntax.Case, "at CASE"
	}
	if checked.Search != nil {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, false, err
		}
		value, usage, _, err := s.evaluatePLExpression(f, checked.Search, read, nil)
		if err != nil {
			return sqltype.Value{}, false, plRuntimeError(err, read, d, raw.Search.Start, raw.Search.End, instruction.Syntax.Pos, action, true)
		}
		f.Searches[checked.SearchSlot] = proc.PLSearch{Value: value, Usage: usage}
		defer func() { f.Searches[checked.SearchSlot] = proc.PLSearch{} }()
	}
	boolean, ok := read.LookupType(sqltype.Bool)
	if !ok {
		return sqltype.Value{}, false, errors.New("stuntgres: missing PL condition Boolean type")
	}
	for i, branch := range checked.Arms {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, false, err
		}
		value, _, _, err := s.evaluatePLExpression(f, branch.Query, read, nil)
		expressionError := err != nil
		if err == nil {
			value, err = s.convertPLValue(value, boolean.Usage())
		}
		if err != nil {
			arm := raw.Arms[i]
			first := arm.Conditions[0]
			if checked.Search != nil && expressionError {
				err = plCaseExpressionError(err, branch, first.Start)
			}
			return sqltype.Value{}, false, plRuntimeError(err, read, d, first.Start, arm.Conditions[len(arm.Conditions)-1].End, instruction.Syntax.Pos, action, expressionError)
		}
		if value.IsNull() {
			continue
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, false, err
		}
		if truth {
			return s.executePLStatements(d, f, branch.Statements, read)
		}
	}
	if isCase && !checked.HasElse {
		err := sqlError("20000", "case not found").(*sqlstate.Error)
		err.Hint = "CASE statement is missing ELSE part."
		return sqltype.Value{}, false, plRuntimeError(err, read, d, parser.Position{}, parser.Position{}, instruction.Syntax.Pos, action, false)
	}
	return s.executePLStatements(d, f, checked.Else, read)
}

// The operator has its own disjoint coordinate; original WHEN children retain
// their original byte/character coordinates. Neither source is reparsed.
func plCaseExpressionError(err error, branch proc.PLBranch, start parser.Position) error {
	var original *sqlstate.Error
	if !errors.As(err, &original) {
		return err
	}
	next := *original
	if next.Position != "" {
		position, failure := strconv.Atoi(next.Position)
		if failure == nil {
			if position == branch.OperatorPosition.Character {
				position = branch.DiagnosticOperator
			} else {
				position = position - start.Character + branch.DiagnosticItems
			}
			next.InternalPosition = strconv.Itoa(position)
			next.InternalQuery = branch.DiagnosticQuery
			next.Position = ""
		}
	}
	if plExpressionContext(err, &next) {
		next.Context = "PL/pgSQL expression \"" + branch.DiagnosticQuery + "\""
	}
	return sqlstate.WithDiagnostic(err, &next)
}
