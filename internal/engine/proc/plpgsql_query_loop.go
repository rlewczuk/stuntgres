package proc

import (
	"stuntgres/internal/parser"
)

func (p *PLProgram) queryLoopTarget(raw parser.Expr, scopes []*PLBlock) (PLTarget, error) {
	target, err := p.ResolveTarget(raw, scopes)
	if err != nil || len(target.Steps) != 0 || target.Slot < 0 || target.Slot >= len(p.Variables) {
		return PLTarget{}, parser.PLPositionError("42601", "loop variable of loop over rows must be a record variable or list of scalar variables", raw.SourcePosition())
	}
	variable := p.Variables[target.Slot]
	if !variable.Record && (variable.Usage.Type == nil || !variable.Usage.Type.BaseType().IsComposite()) {
		return PLTarget{}, parser.PLPositionError("42601", "loop variable of loop over rows must be a record variable or list of scalar variables", raw.SourcePosition())
	}
	if variable.Constant {
		return PLTarget{}, parser.PLPositionError("22005", "variable \""+variable.Name+"\" is declared CONSTANT", raw.SourcePosition())
	}
	return target, nil
}

func plQueryLoopPosition(statement parser.Statement) (parser.Position, bool) {
	switch q := statement.(type) {
	case *parser.Select:
		if q != nil {
			return q.Pos, true
		}
	case *parser.Insert:
		if q != nil {
			return q.Pos, true
		}
	case *parser.Update:
		if q != nil {
			return q.Pos, true
		}
	case *parser.Delete:
		if q != nil {
			return q.Pos, true
		}
	case *parser.Merge:
		if q != nil {
			return q.Pos, true
		}
	}
	return parser.Position{}, false
}
