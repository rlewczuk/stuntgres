package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

func (s *StatementScope) executePLLoop(d *catalog.Procedure, f *proc.PLFrame, loop *proc.PLBlock, read catalog.Read) (sqltype.Value, proc.PLFlow, error) {
	f.Enter(loop)
	defer f.Leave(loop)
	for {
		// An empty body and a continue-only body must both remain cancellable.
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		value, flow, err := s.executePLStatements(d, f, loop.Statements, read)
		if err != nil || flow.Kind == proc.PLReturn {
			return value, flow, err
		}
		if flow.Kind == proc.PLNormal {
			continue
		}
		if flow.Target != loop.Control {
			return value, flow, nil
		}
		if flow.Kind == proc.PLExit {
			return sqltype.Value{}, proc.PLFlow{}, nil
		}
		if flow.Kind != proc.PLContinue {
			return sqltype.Value{}, proc.PLFlow{}, fmt.Errorf("stuntgres: invalid PL loop flow")
		}
	}
}

func (s *StatementScope) executePLTransfer(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (sqltype.Value, proc.PLFlow, error) {
	raw := in.Syntax
	action, kind := "at EXIT", proc.PLExit
	if raw.Transfer.Continue {
		action, kind = "at CONTINUE", proc.PLContinue
	}
	if in.Query != nil {
		value, _, _, err := s.evaluatePLExpression(f, in.Query, read, nil)
		expressionError := err != nil
		if err == nil {
			boolean, ok := read.LookupType(sqltype.Bool)
			if !ok {
				return sqltype.Value{}, proc.PLFlow{}, fmt.Errorf("stuntgres: missing PL condition Boolean type")
			}
			value, err = s.convertPLValue(value, boolean.Usage())
		}
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, plRuntimeError(err, read, d, raw.ExpressionStart, raw.ExpressionEnd, raw.Pos, action, expressionError)
		}
		if value.IsNull() {
			return sqltype.Value{}, proc.PLFlow{}, nil
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, proc.PLFlow{}, err
		}
		if !truth {
			return sqltype.Value{}, proc.PLFlow{}, nil
		}
	}
	return sqltype.Value{}, proc.PLFlow{Kind: kind, Target: in.Transfer}, nil
}
