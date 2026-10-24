package engine

import (
	"errors"
	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) executePLConditional(d *catalog.Procedure, f *proc.PLFrame, instruction proc.PLInstruction, read catalog.Read) (sqltype.Value, proc.PLFlow, error) {
	checked, raw, action := instruction.If, instruction.Syntax.If, "at IF"
	isCase := instruction.Case != nil
	if isCase {
		checked, raw, action = instruction.Case, instruction.Syntax.Case, "at CASE"
	}
	if checked.Search != nil {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		value, usage, _, err := s.evaluatePLExpression(f, checked.Search, read, nil)
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, plRuntimeError(err, read, d, raw.Search.Start, raw.Search.End, instruction.Syntax.Pos, action, true)
		}
		f.Searches[checked.SearchSlot] = proc.PLSearch{Value: value, Usage: usage}
		defer func() { f.Searches[checked.SearchSlot] = proc.PLSearch{} }()
	}
	boolean, ok := read.LookupType(sqltype.Bool)
	if !ok {
		return sqltype.Value{}, proc.PLFlow{}, errors.New("stuntgres: missing PL condition Boolean type")
	}
	for i, branch := range checked.Arms {
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
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
				err = proc.PLCaseExpressionError(err, branch, first.Start)
			}
			return sqltype.Value{}, proc.PLFlow{}, plRuntimeError(err, read, d, first.Start, arm.Conditions[len(arm.Conditions)-1].End, instruction.Syntax.Pos, action, expressionError && checked.Search == nil)
		}
		if value.IsNull() {
			continue
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		if truth {
			return s.executePLStatements(d, f, branch.Statements, read)
		}
	}
	if isCase && !checked.HasElse {
		err := sqlError("20000", "case not found").(*sqlstate.Error)
		err.Hint = "CASE statement is missing ELSE part."
		return sqltype.Value{}, proc.PLFlow{}, plRuntimeError(err, read, d, parser.Position{}, parser.Position{}, instruction.Syntax.Pos, action, false)
	}
	return s.executePLStatements(d, f, checked.Else, read)
}
