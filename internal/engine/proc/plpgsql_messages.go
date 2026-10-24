package proc

import (
	"fmt"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
)

// Message programs contain only immutable syntax and ordinary expression queries.
// No operand is bound or evaluated while a definition is compiled or captured.
type PLRaise struct {
	Level parser.PLRaiseLevel
	Form parser.PLRaiseForm
	Format string
	Code string
	Arguments []*parser.Select
	Options []PLRaiseOption
}
type PLRaiseOption struct {
	Kind parser.PLRaiseOptionKind
	Query *parser.Select
}
type PLAssert struct {
	Condition *parser.Select
	Message *parser.Select
}

// RaiseSQLState constructs a condition from the first released inventory match.
// Handler matching intentionally retains all aliases and has its own resolver.
func RaiseSQLState(name string) (string, error) {
	if validRaiseSQLState(name) { return name, nil }
	return raiseConditionSQLState(name)
}

func raiseConditionSQLState(name string) (string, error) {
	for _, entry := range plExceptionConditions {
		if entry.name == name { return entry.code, nil }
	}
	return "", &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:"42704", Message:fmt.Sprintf("unrecognized exception condition %q", name)}
}

func validRaiseSQLState(code string) bool {
	if len(code) != 5 { return false }
	for i := range code {
		c := code[i]
		if !(c >= 'A' && c <= 'Z' || c >= '0' && c <= '9') { return false }
	}
	return true
}

func raiseArity(format string) int {
	count := 0
	for i := 0; i < len(format); i++ {
		if format[i] != '%' { continue }
		if i+1 < len(format) && format[i+1] == '%' { i++; continue }
		count++
	}
	return count
}

func (p *PLProgram) compileRaise(raw *parser.PLRaise, pos parser.Position) (*PLRaise, error) {
	checked := &PLRaise{Level:raw.Level, Form:raw.Form, Format:raw.Format}
	compilationError := func(code, message string) error {
		return &sqlstate.Error{Severity:"ERROR", SeverityUnlocalized:"ERROR", Code:code, Message:message,
			Context:fmt.Sprintf("compilation of PL/pgSQL function %q near line %d", p.Function, strings.Count(p.Source[:pos.Offset], "\n")+1)}
	}
	switch raw.Form {
	case parser.PLRaiseFormat:
		arity := raiseArity(raw.Format)
		if arity > len(raw.Arguments) { return nil, compilationError("42601", "too few parameters specified for RAISE") }
		if arity < len(raw.Arguments) { return nil, compilationError("42601", "too many parameters specified for RAISE") }
	case parser.PLRaiseCondition:
		code, err := raiseConditionSQLState(raw.Condition.Name.Text)
		if err != nil { return nil, compilationError("42704", fmt.Sprintf("unrecognized exception condition %q", raw.Condition.Name.Text)) }
		checked.Code = code
	case parser.PLRaiseSQLState:
		if !validRaiseSQLState(raw.Condition.Code) { return nil, fmt.Errorf("stuntgres: invalid RAISE SQLSTATE") }
		checked.Code = raw.Condition.Code
	case parser.PLRaiseUsing:
	default:
		return nil, fmt.Errorf("stuntgres: invalid RAISE form")
	}
	checked.Arguments = make([]*parser.Select, len(raw.Arguments))
	for i, operand := range raw.Arguments { checked.Arguments[i] = plExpressionQuery(operand.Expression) }
	checked.Options = make([]PLRaiseOption, len(raw.Options))
	for i, option := range raw.Options { checked.Options[i] = PLRaiseOption{Kind:option.Kind, Query:plExpressionQuery(option.Value.Expression)} }
	return checked, nil
}

func (p *PLProgram) validRaise(raw *parser.PLRaise, checked *PLRaise, pos parser.Position) bool {
	if checked == nil || raw.Level > parser.PLRaiseWarning || checked.Level != raw.Level || checked.Form != raw.Form || checked.Format != raw.Format || len(checked.Arguments) != len(raw.Arguments) || len(checked.Options) != len(raw.Options) {
		return false
	}
	code := ""
	switch raw.Form {
	case parser.PLRaiseFormat:
		if raw.Condition != (parser.PLCondition{}) || raiseArity(raw.Format) != len(raw.Arguments) { return false }
	case parser.PLRaiseCondition:
		if raw.Format != "" || len(raw.Arguments) != 0 || raw.Condition.Code != "" || raw.Condition.Name.Text == "" { return false }
		var err error
		code, err = raiseConditionSQLState(raw.Condition.Name.Text)
		if err != nil { return false }
	case parser.PLRaiseSQLState:
		if raw.Format != "" || len(raw.Arguments) != 0 || raw.Condition.Name != (parser.Identifier{}) || !validRaiseSQLState(raw.Condition.Code) { return false }
		code = raw.Condition.Code
	case parser.PLRaiseUsing:
		if raw.Format != "" || len(raw.Arguments) != 0 || raw.Condition != (parser.PLCondition{}) || len(raw.Options) == 0 { return false }
	default:
		return false
	}
	if checked.Code != code { return false }
	prior := pos.Offset
	for _, value := range raw.Arguments {
		if value.Start.Offset <= prior { return false }
		prior = value.End.Offset
	}
	for i, option := range raw.Options {
		if option.Kind > parser.PLRaiseSchema || checked.Options[i].Kind != option.Kind || option.Pos.Offset <= prior || option.Pos.Offset >= option.Value.Start.Offset {
			return false
		}
		prior = option.Value.End.Offset
	}
	if raw.Form == parser.PLRaiseCondition || raw.Form == parser.PLRaiseSQLState {
		if raw.Condition.Pos.Offset <= pos.Offset || raw.Condition.Pos.Offset >= len(p.Source) || raw.Condition.Pos.Character <= pos.Character { return false }
	}
	return true
}
