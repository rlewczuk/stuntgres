package engine

import (
	"fmt"

	"stuntgres/internal/engine/catalog"
	"stuntgres/internal/engine/proc"
	"stuntgres/internal/sqltype"
)

type plFlowKind uint8

const (
	plNormal plFlowKind = iota
	plReturn
	plExit
	plContinue
)

// Flow is not a SQL error: exception checkpoints commit normal transfers and
// lexical/cursor owners unwind before the addressed control token consumes it.
type plFlow struct {
	kind   plFlowKind
	target *proc.PLControl
}

func (s *StatementScope) executePLLoop(d *catalog.Procedure, f *proc.PLFrame, loop *proc.PLBlock, read catalog.Read) (sqltype.Value, plFlow, error) {
	f.Enter(loop)
	defer f.Leave(loop)
	for {
		// An empty body and a continue-only body must both remain cancellable.
		if err := s.op.Context().Err(); err != nil {
			return sqltype.Value{}, plFlow{}, err
		}
		value, flow, err := s.executePLStatements(d, f, loop.Statements, read)
		if err != nil || flow.kind == plReturn {
			return value, flow, err
		}
		if flow.kind == plNormal {
			continue
		}
		if flow.target != loop.Control {
			return value, flow, nil
		}
		if flow.kind == plExit {
			return sqltype.Value{}, plFlow{}, nil
		}
		if flow.kind != plContinue {
			return sqltype.Value{}, plFlow{}, fmt.Errorf("stuntgres: invalid PL loop flow")
		}
	}
}

func (s *StatementScope) executePLTransfer(d *catalog.Procedure, f *proc.PLFrame, in proc.PLInstruction, read catalog.Read) (sqltype.Value, plFlow, error) {
	raw := in.Syntax
	action, kind := "at EXIT", plExit
	if raw.Transfer.Continue {
		action, kind = "at CONTINUE", plContinue
	}
	if in.Query != nil {
		value, _, _, err := s.evaluatePLExpression(f, in.Query, read, nil)
		expressionError := err != nil
		if err == nil {
			boolean, ok := read.LookupType(sqltype.Bool)
			if !ok {
				return sqltype.Value{}, plFlow{}, fmt.Errorf("stuntgres: missing PL condition Boolean type")
			}
			value, err = s.convertPLValue(value, boolean.Usage())
		}
		if err != nil {
			return sqltype.Value{}, plFlow{}, plRuntimeError(err, read, d, raw.ExpressionStart, raw.ExpressionEnd, raw.Pos, action, expressionError)
		}
		if value.IsNull() {
			return sqltype.Value{}, plFlow{}, nil
		}
		truth, err := value.Boolean()
		if err != nil {
			return sqltype.Value{}, plFlow{}, err
		}
		if !truth {
			return sqltype.Value{}, plFlow{}, nil
		}
	}
	return sqltype.Value{}, plFlow{kind: kind, target: in.Transfer}, nil
}
