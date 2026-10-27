package proc

import (
	"fmt"
	"stuntgres/internal/parser"
	"unicode/utf8"
)

func validPLDiagnosticsSource(source string, position parser.Position, d *parser.PLDiagnostics) bool {
	if !parser.ValidPLDiagnostics(d) || position != d.Pos || d.Pos.Offset < 0 || d.Pos.Offset >= d.End.Offset || d.End != d.Delimiter || d.End.Offset >= len(source) || source[d.End.Offset] != ';' || d.Text != source[d.Pos.Offset:d.End.Offset] {
		return false
	}
	for _, at := range []parser.Position{d.Pos, d.End} {
		if at.Character != utf8.RuneCountInString(source[:at.Offset])+1 {
			return false
		}
	}
	previous := d.Pos.Offset
	for _, a := range d.Assignments {
		target := a.Target.SourcePosition()
		if target.Offset <= previous || target.Offset >= a.ItemPos.Offset || a.ItemPos.Offset >= d.End.Offset || target.Character != utf8.RuneCountInString(source[:target.Offset])+1 || a.ItemPos.Character != utf8.RuneCountInString(source[:a.ItemPos.Offset])+1 {
			return false
		}
		previous = a.ItemPos.Offset
	}
	return true
}

func (p *PLProgram) compileDiagnostics(raw *parser.PLDiagnostics, scopes []*PLBlock) (*PLDiagnostics, error) {
	if !parser.ValidPLDiagnostics(raw) {
		return nil, fmt.Errorf("stuntgres: invalid PL diagnostics syntax")
	}
	checked := &PLDiagnostics{Stacked: raw.Stacked, Assignments: make([]PLDiagnosticAssignment, len(raw.Assignments))}
	for i, a := range raw.Assignments {
		target, err := p.ResolveTarget(a.Target, scopes)
		if err != nil {
			return nil, plUnknownTargetError(a.Target, err)
		}
		variable := p.Variables[target.Slot]
		if variable.Constant {
			return nil, parser.PLPositionError("22005", fmt.Sprintf("variable %q is declared CONSTANT", variable.Name), a.Target.SourcePosition())
		}
		scalar := true
		for _, step := range target.Steps {
			if step.Subscript != nil {
				scalar = false
			}
		}
		if len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite()) {
			scalar = false
		}
		if !scalar {
			return nil, parser.PLPositionError("42601", fmt.Sprintf("%q is not a scalar variable", variable.Name), a.Target.SourcePosition())
		}
		checked.Assignments[i] = PLDiagnosticAssignment{Target: target, Item: a.Item}
	}
	return checked, nil
}
