package proc

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"unicode/utf8"
)

func (p *PLProgram) compileInto(raw *parser.PLIntoStatement, scopes []*PLBlock) (*PLInto, error) {
	out := &PLInto{Query: raw.Query, Strict: raw.Strict}
	if len(raw.Targets) == 0 {
		return nil, fmt.Errorf("stuntgres: empty INTO target list")
	}
	for index, syntax := range raw.Targets {
		if syntax == nil {
			return nil, fmt.Errorf("stuntgres: missing INTO target")
		}
		target, err := p.ResolveTarget(syntax, scopes)
		if err != nil {
			return nil, plUnknownTargetError(syntax, err)
		}
		variable := p.Variables[target.Slot]
		if variable.Constant {
			return nil, parser.PLPositionError("22005", fmt.Sprintf("variable %q is declared CONSTANT", variable.Name), syntax.SourcePosition())
		}
		if len(raw.Targets) > 1 && p.wholeIntoTarget(target) {
			if index > 0 {
				return nil, parser.PLPositionError("42601", fmt.Sprintf("%q is not a scalar variable", variable.Name), syntax.SourcePosition())
			}
			position := syntax.SourcePosition()
			if index == 0 && len(raw.Separators) > 0 {
				position = raw.Separators[0]
			}
			return nil, parser.PLPositionError("42601", "record variable cannot be part of multiple-item INTO list", position)
		}
		out.Targets = append(out.Targets, target)
	}
	return out, nil
}

func (p *PLProgram) wholeIntoTarget(target PLTarget) bool {
	variable := p.Variables[target.Slot]
	return len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite())
}

func (d *plDiagnosticDatums) into(p *PLProgram, source string, raw *parser.PLIntoStatement, checked *PLInto, scopes []*PLBlock) error {
	if err := d.expression(p, source, raw.Start, raw.IntoStart, scopes); err != nil {
		return err
	}
	if err := d.expression(p, source, raw.TargetsStart, raw.TargetsEnd, scopes); err != nil {
		return err
	}
	if len(checked.Targets) != 1 || !p.wholeIntoTarget(checked.Targets[0]) {
		d.extra++
	}
	// Suffix INTO ends at the statement delimiter; there are no SQL tokens
	// after its targets. The compound-name visitor requires a nonempty span.
	if raw.TargetsEnd == raw.End {
		return nil
	}
	return d.expression(p, source, raw.TargetsEnd, raw.End, scopes)
}

func validPLIntoSource(source string, q *parser.PLIntoStatement, position parser.Position) bool {
	if q.Start.Offset < 0 || q.IntoStart.Offset < q.Start.Offset || q.TargetsStart.Offset <= q.IntoStart.Offset || q.TargetsEnd.Offset <= q.TargetsStart.Offset || q.End != q.Delimiter || q.End.Offset >= len(source) || q.TargetsEnd.Offset > q.End.Offset || source[q.End.Offset] != ';' || position.Offset < q.Start.Offset || position.Offset >= q.End.Offset {
		return false
	}
	for _, p := range []parser.Position{q.Start, q.IntoStart, q.TargetsStart, q.TargetsEnd, q.End} {
		if p.Character != utf8.RuneCountInString(source[:p.Offset])+1 {
			return false
		}
	}
	if len(q.Targets) == 0 || len(q.Separators) != len(q.Targets)-1 {
		return false
	}
	for i, target := range q.Targets {
		if target == nil {
			return false
		}
		at := target.SourcePosition()
		if at.Offset < q.TargetsStart.Offset || at.Offset >= q.TargetsEnd.Offset || at.Character != utf8.RuneCountInString(source[:at.Offset])+1 {
			return false
		}
		if i > 0 {
			comma := q.Separators[i-1]
			if comma.Offset <= q.Targets[i-1].SourcePosition().Offset || comma.Offset >= at.Offset || source[comma.Offset] != ',' || comma.Character != utf8.RuneCountInString(source[:comma.Offset])+1 {
				return false
			}
		}
	}
	if q.IntoStart.Offset+4 > len(source) || !strings.EqualFold(source[q.IntoStart.Offset:q.IntoStart.Offset+4], "into") {
		return false
	}
	switch query := q.Query.(type) {
	case *parser.Select:
		if query.Into != nil {
			return false
		}
	case *parser.Merge:
		return false
	}
	text := []byte(source[q.Start.Offset:q.End.Offset])
	for i := q.IntoStart.Offset - q.Start.Offset; i < q.TargetsEnd.Offset-q.Start.Offset; i++ {
		if text[i] != '\n' && text[i] != '\r' {
			text[i] = ' '
		}
	}
	return q.Text == string(text)
}
