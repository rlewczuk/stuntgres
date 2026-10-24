package proc

import (
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"stuntgres/internal/parser"
)

func plOperandQuery(operand parser.PLExpression) *parser.Select {
	q := plExpressionQuery(operand.Expression)
	if q != nil && operand.Alias.Text != "" {
		q.Labels = []parser.Identifier{operand.Alias}
	}
	return q
}

// A single target supplies a new private name. A comma list instead enters
// PostgreSQL's existing-datum/assignability admission before range rejection.
func (p *PLProgram) integerForTarget(raw *parser.PLIntegerFor, scopes []*PLBlock) (string, error) {
	if len(raw.Targets) == 0 || len(raw.TargetCommas) != len(raw.Targets)-1 {
		return "", fmt.Errorf("stuntgres: invalid integer FOR targets")
	}
	for _, target := range raw.Targets {
		if target == nil {
			return "", fmt.Errorf("stuntgres: missing integer FOR target")
		}
	}
	if len(raw.Targets) == 1 {
		return p.integerForName(raw.Targets[0], raw.Lower.Start, scopes)
	}
	for i, source := range raw.Targets {
		target, err := p.ResolveTarget(source, scopes)
		if err != nil {
			return "", parser.PLPositionError("42601", fmt.Sprintf(`"%s" is not a known variable`, plIntegerForListName(source)), source.SourcePosition())
		}
		variable := p.Variables[target.Slot]
		wholeRow := len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite())
		if i == 0 && wholeRow {
			return "", parser.PLPositionError("42601", `syntax error at or near ","`, raw.TargetCommas[0])
		}
		if variable.Constant {
			return "", parser.PLPositionError("22005", fmt.Sprintf(`variable "%s" is declared CONSTANT`, variable.Name), source.SourcePosition())
		}
		for _, step := range target.Steps {
			if step.Subscript != nil {
				return "", parser.PLPositionError("42601", `syntax error at or near "["`, step.Subscript.Pos)
			}
		}
		if wholeRow {
			return "", parser.PLPositionError("42601", fmt.Sprintf(`"%s" is not a scalar variable`, plIntegerForListName(source)), source.SourcePosition())
		}
	}
	return "", parser.PLPositionError("42601", "integer FOR loop must have only one target variable", raw.Targets[0].SourcePosition())
}

func plIntegerForListName(raw parser.Expr) string {
	root, steps, _ := plTargetPath(raw)
	var names []string
	switch root := root.(type) {
	case parser.ColumnExpr:
		names = plNames(root.Name)
	case parser.ParameterExpr:
		names = []string{"$" + strconv.Itoa(root.Number)}
	}
	for _, step := range steps {
		if step.Field != nil {
			names = append(names, step.Field.Text)
		}
	}
	return strings.Join(names, ".")
}

// The target supplies a new name. Only compound names need an existing outer
// datum; neither assignment constraints nor its destination type are inherited.
func (p *PLProgram) integerForName(raw parser.Expr, end parser.Position, scopes []*PLBlock) (string, error) {
	if raw == nil {
		return "", fmt.Errorf("stuntgres: missing integer FOR target")
	}
	root, steps, ok := plTargetPath(raw)
	if !ok {
		return "", parser.PLPositionError("42601", "invalid integer FOR loop variable", raw.SourcePosition())
	}
	var parts []parser.Identifier
	switch root := root.(type) {
	case parser.ColumnExpr:
		parts = append(parts, root.Name.Parts...)
	case parser.ParameterExpr:
		parts = []parser.Identifier{{Text: "$" + strconv.Itoa(root.Number), Pos: root.Pos}}
	default:
		return "", fmt.Errorf("stuntgres: invalid integer FOR target")
	}
	for _, step := range steps {
		if step.Subscript != nil {
			return "", parser.PLPositionError("42601", `syntax error at or near "["`, step.Subscript.Pos)
		}
		if step.Field == nil {
			return "", fmt.Errorf("stuntgres: invalid integer FOR field")
		}
		parts = append(parts, *step.Field)
	}
	names := plNames(parser.ObjectName{Parts: parts})
	if len(parts) > 1 {
		_, used, found, err := plDiagnosticParent(p, parts, scopes)
		if err != nil {
			return "", err
		}
		if !found || used < len(parts)-1 {
			return "", parser.PLPositionError("42601", fmt.Sprintf("\"%s\" is not a known variable", strings.Join(names, ".")), raw.SourcePosition())
		}
	}
	name := strings.Join(names, ".")
	spelled, ok := parser.PLIntegerForTargetName(p.Source, raw.SourcePosition(), end)
	if !ok || len(spelled.Parts) != len(parts) {
		return "", fmt.Errorf("stuntgres: integer FOR target differs from source")
	}
	for i, part := range parts {
		if part != spelled.Parts[i] {
			return "", fmt.Errorf("stuntgres: integer FOR target differs from source")
		}
	}
	return name, nil
}

func (d *plDiagnosticDatums) integerFor(p *PLProgram, source string, raw *parser.PLIntegerFor, scopes []*PLBlock) error {
	if err := d.expression(p, source, raw.Targets[0].SourcePosition(), raw.Lower.Start, scopes); err != nil {
		return err
	}
	for _, operand := range []parser.PLExpression{raw.Lower, raw.Upper, raw.Step} {
		if err := d.expression(p, source, operand.Start, operand.End, scopes); err != nil {
			return err
		}
	}
	return nil
}

func plIntegerForDelimiters(source string, raw *parser.PLIntegerFor) bool {
	token := func(pos parser.Position, text string) bool {
		return pos.Offset >= 0 && pos.Offset+len(text) <= len(source) && pos.Character == utf8.RuneCountInString(source[:pos.Offset])+1 && strings.EqualFold(source[pos.Offset:pos.Offset+len(text)], text)
	}
	if !token(raw.Lower.Delimiter, "..") {
		return false
	}
	if raw.Reverse {
		if !token(raw.ReversePos, "reverse") || raw.ReversePos.Offset >= raw.Lower.Start.Offset {
			return false
		}
	} else if raw.ReversePos != (parser.Position{}) {
		return false
	}
	if raw.Lower.Delimiter.Offset >= raw.Upper.Start.Offset {
		return false
	}
	if raw.Step.Expression != nil {
		return raw.Upper.Delimiter.Offset < raw.Step.Start.Offset && raw.Body.Pos == raw.Step.Delimiter && token(raw.Upper.Delimiter, "by") && token(raw.Step.Delimiter, "loop")
	}
	return raw.Step == (parser.PLExpression{}) && raw.Body.Pos == raw.Upper.Delimiter && token(raw.Upper.Delimiter, "loop")
}
