package proc

import (
	"fmt"
	"reflect"
	"stuntgres/internal/parser"
)

func (p *PLProgram) compileDynamic(d *Definition, statement *parser.PLStatement, scopes []*PLBlock) (*PLDynamic, error) {
	raw := statement.Dynamic
	if !parser.ValidPLDynamicStatement(raw) {
		return nil, fmt.Errorf("stuntgres: invalid dynamic syntax admission")
	}
	if raw.ReturnQuery && !d.ReturnsSet {
		return nil, parser.PLPositionError("42804", "cannot use RETURN QUERY in a non-SETOF function", statement.Pos)
	}
	if raw.ReturnQuery && len(raw.Targets) != 0 {
		return nil, parser.PLPositionError("42601", "syntax error at or near \"INTO\"", raw.IntoStart)
	}
	out := &PLDynamic{ReturnQuery: raw.ReturnQuery, Command: plExpressionQuery(raw.Command.Expression), Strict: raw.Strict, Using: make([]*parser.Select, len(raw.Using))}
	for i, value := range raw.Using {
		out.Using[i] = plExpressionQuery(value.Expression)
	}
	if len(raw.Targets) != 0 {
		capture, err := p.compileInto(&parser.PLIntoStatement{Targets: raw.Targets, Separators: raw.Separators}, scopes)
		if err != nil {
			return nil, err
		}
		out.Targets = capture.Targets
	}
	return out, nil
}

func (d *plDiagnosticDatums) dynamic(p *PLProgram, source string, raw *parser.PLDynamicStatement, checked *PLDynamic, scopes []*PLBlock) error {
	// Visit operands and destinations in lexical source order. INTO's synthetic
	// row datum follows its targets, never the later USING operands.
	if err := d.expression(p, source, raw.Command.Start, raw.Command.End, scopes); err != nil {
		return err
	}
	into := func() error {
		if len(raw.Targets) == 0 {
			return nil
		}
		if err := d.expression(p, source, raw.TargetsStart, raw.TargetsEnd, scopes); err != nil {
			return err
		}
		if len(checked.Targets) != 1 || !p.wholeIntoTarget(checked.Targets[0]) {
			d.extra++
		}
		return nil
	}
	before := len(raw.Targets) != 0 && (len(raw.Using) == 0 || raw.IntoStart.Offset < raw.Using[0].Start.Offset)
	if before {
		if err := into(); err != nil {
			return err
		}
	}
	for _, value := range raw.Using {
		if err := d.expression(p, source, value.Start, value.End, scopes); err != nil {
			return err
		}
	}
	if !before {
		return into()
	}
	return nil
}

func (p *PLProgram) validDynamic(d *Definition, statement *parser.PLStatement, in *PLDynamic, scopes []*PLBlock, diagnostics *plDiagnosticDatums) bool {
	raw := statement.Dynamic
	if !parser.ValidPLDynamicStatement(raw) || raw.Command.Expression == nil || raw.End.Offset >= len(d.Source) || raw.End.Offset < 0 || d.Source[raw.End.Offset] != ';' || raw.Strict && len(raw.Targets) == 0 {
		return false
	}
	if !plSourceSpan(d.Source, raw.Command.Expression, raw.Command.Start, raw.Command.End, raw.Command.Delimiter) {
		return false
	}
	previous := raw.Command.End.Offset
	for _, value := range raw.Using {
		if value.Expression == nil || value.Start.Offset < previous || !plSourceSpan(d.Source, value.Expression, value.Start, value.End, value.Delimiter) {
			return false
		}
		previous = value.End.Offset
	}
	expected, err := p.compileDynamic(d, statement, scopes)
	return err == nil && reflect.DeepEqual(expected, in) && diagnostics.dynamic(p, d.Source, raw, in, scopes) == nil
}
