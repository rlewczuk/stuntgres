package proc

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
)

// FOREACH resolves existing destinations, unlike cursor-FOR's implicit record.
func (p *PLProgram) foreachTargets(raw *parser.PLForeach, scopes []*PLBlock) ([]PLTarget, error) {
	if !parser.ValidPLForeachSlice(int64(raw.Slice)) {
		return nil, fmt.Errorf("stuntgres: invalid FOREACH SLICE level")
	}
	if len(raw.Targets) == 0 || len(raw.TargetCommas) != len(raw.Targets)-1 {
		return nil, fmt.Errorf("stuntgres: invalid FOREACH target list")
	}
	targets := make([]PLTarget, len(raw.Targets))
	for i, syntax := range raw.Targets {
		if syntax == nil {
			return nil, fmt.Errorf("stuntgres: missing FOREACH target syntax")
		}
		if i > 0 {
			comma := raw.TargetCommas[i-1]
			if comma.Character <= 0 || comma.Offset <= raw.Targets[i-1].SourcePosition().Offset || comma.Offset >= syntax.SourcePosition().Offset || comma.Offset >= len(p.Source) || p.Source[comma.Offset] != ',' {
				return nil, fmt.Errorf("stuntgres: invalid FOREACH target separator")
			}
		}
		target, err := p.ResolveTarget(syntax, scopes)
		if err != nil {
			return nil, parser.PLPositionError("42601", "loop variable of FOREACH must be a known variable or list of variables", syntax.SourcePosition())
		}
		for _, step := range target.Steps {
			if step.Subscript != nil {
				return nil, parser.PLPositionError("42601", `syntax error at or near "["`, step.Subscript.Pos)
			}
		}
		variable := p.Variables[target.Slot]
		wholeRow := len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite())
		if len(raw.Targets) > 1 && wholeRow {
			if i == 0 {
				return nil, parser.PLPositionError("42601", `syntax error at or near ","`, raw.TargetCommas[0])
			}
			return nil, parser.PLPositionError("42601", `"`+variable.Name+`" is not a scalar variable`, syntax.SourcePosition())
		}
		if variable.Constant {
			return nil, parser.PLPositionError("22005", "variable \""+variable.Name+"\" is declared CONSTANT", syntax.SourcePosition())
		}
		targets[i] = target
	}
	if raw.SliceSpecified {
		pos := raw.SlicePos
		if pos.Character <= 0 || pos.Offset < 0 || pos.Offset+len("slice") > len(p.Source) || !strings.EqualFold(p.Source[pos.Offset:pos.Offset+len("slice")], "slice") {
			return nil, fmt.Errorf("stuntgres: invalid FOREACH SLICE keyword")
		}
		// A lexical datum shadows an unreserved PL keyword. Labels and
		// record fields alone do not enter this unqualified datum namespace.
		if _, found := p.Lookup([]string{"slice"}, scopes); found {
			return nil, parser.PLPositionError("42601", `syntax error at or near "`+p.Source[pos.Offset:pos.Offset+len("slice")]+`"`, pos)
		}
	}
	return targets, nil
}

func (d *plDiagnosticDatums) foreach(program *PLProgram, raw *parser.PLForeach, scopes []*PLBlock) error {
	if len(raw.Targets) == 0 {
		return nil
	}
	if err := d.expression(program, program.Source, raw.Targets[0].SourcePosition(), raw.Source.Start, scopes); err != nil {
		return err
	}
	// PostgreSQL allocates a private ROW for a scalar destination list, but
	// not for a singleton ordinary variable (unlike FETCH).
	if len(raw.Targets) > 1 {
		d.extra++
	}
	return d.expression(program, program.Source, raw.Source.Start, raw.Source.End, scopes)
}
