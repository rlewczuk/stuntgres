package proc

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

// PLProgram contains no evaluator, catalog handle or invocation values. Slots
// identify lexical variables; aliases always identify the original slot.
type PLProgram struct {
	Root        *PLBlock
	Syntax      *parser.PLBlock
	Variables   []PLVariable
	FormalCount int
	Function    string
	Procedure   bool
}
type PLVariable struct {
	Name                      string
	Usage                     sqltype.Usage
	Constant, NotNull, Record bool
	InheritInputCollation     bool
}
type PLBlock struct {
	Syntax       *parser.PLBlock
	Label        string
	Bindings     map[string]int
	Declarations []PLInitializer
	Statements   []PLInstruction
	HandlerScope *PLBlock
	Handlers     []PLHandler
}
type PLCondition struct {
	Codes  []string
	Others bool
}
type PLHandler struct {
	Syntax     *parser.PLHandler
	Conditions []PLCondition
	Statements []PLInstruction
}
type PLInitializer struct {
	Slot       int
	Expression parser.Expr
	Query      *parser.Select
	Scope      *PLBlock
}
type PLTarget struct {
	Slot   int
	Fields []string
}
type PLInstruction struct {
	Syntax *parser.PLStatement
	Query  *parser.Select
	Block  *PLBlock
	Target PLTarget
}
type PLTypeResolver func(parser.PLType) (sqltype.Usage, bool, error)

func CompilePL(definition *Definition, syntax *parser.PLBlock, procedure bool, resolve PLTypeResolver) (*PLProgram, error) {
	p := &PLProgram{Syntax: syntax, FormalCount: len(definition.Formals), Function: definition.Name, Procedure: procedure}
	outer := &PLBlock{Label: definition.Name, Bindings: make(map[string]int)}
	for i, f := range definition.Formals {
		p.Variables = append(p.Variables, PLVariable{Name: f.Name, Usage: f.Usage, InheritInputCollation: f.Usage.Collation.ID != 0})
		for _, name := range [2]string{"$" + strconv.Itoa(i+1), f.Name} {
			if name == "" {
				continue
			}
			if _, exists := outer.Bindings[name]; exists {
				return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42P13", Message: fmt.Sprintf("parameter name %q used more than once", name), Context: fmt.Sprintf("compilation of PL/pgSQL function %q near line 1", definition.Name)}
			}
			outer.Bindings[name] = i
		}
	}
	var compile func(*parser.PLBlock, []*PLBlock) (*PLBlock, error)
	compileStatements := func(raw []parser.PLStatement, scopes []*PLBlock) ([]PLInstruction, error) {
		var instructions []PLInstruction
		for i := range raw {
			statement := &raw[i]
			instruction := PLInstruction{Syntax: statement, Query: plExpressionQuery(statement.Expression)}
			switch {
			case statement.Block != nil:
				child, err := compile(statement.Block, scopes)
				if err != nil {
					return nil, err
				}
				instruction.Block = child
			case statement.Rethrow:
			case statement.Return:
				message, code := "", "42804"
				switch {
				case procedure && statement.Expression != nil:
					message, code = "RETURN cannot have a parameter in a procedure", "42601"
				case len(definition.OutputFormals) > 0 && statement.Expression != nil:
					message = "RETURN cannot have a parameter in function with OUT parameters"
				case definition.Void() && statement.Expression != nil:
					message = "RETURN cannot have a parameter in function returning void"
				case !procedure && !definition.Void() && len(definition.OutputFormals) == 0 && statement.Expression == nil:
					message, code = "missing expression at or near \";\"", "42601"
				}
				if message != "" {
					pos := statement.Pos
					if statement.Expression != nil {
						pos = statement.Expression.SourcePosition()
					} else {
						pos.Offset += 6
						pos.Character += 6
					}
					return nil, parser.PLPositionError(code, message, pos)
				}
			default:
				target, err := p.ResolveTarget(statement.Target, scopes)
				if err != nil {
					return nil, err
				}
				if p.Variables[target.Slot].Constant {
					return nil, parser.PLPositionError("22005", fmt.Sprintf("variable %q is declared CONSTANT", p.Variables[target.Slot].Name), statement.Pos)
				}
				instruction.Target = target
			}
			instructions = append(instructions, instruction)
		}
		return instructions, nil
	}
	compile = func(raw *parser.PLBlock, scopes []*PLBlock) (*PLBlock, error) {
		block := &PLBlock{Syntax: raw, Label: raw.Label.Text, Bindings: make(map[string]int)}
		scopes = append(scopes, block)
		for _, decl := range raw.Declarations {
			if _, exists := block.Bindings[decl.Name.Text]; exists {
				return nil, parser.PLPositionError("42601", fmt.Sprintf("duplicate declaration at or near %q", decl.Name.Text), decl.Name.Pos)
			}
			if decl.Alias != nil {
				target, err := p.ResolveTarget(decl.Alias, scopes)
				if err != nil {
					return nil, err
				}
				if len(target.Fields) != 0 {
					return nil, parser.PLPositionError("42601", "alias must identify a variable", decl.Name.Pos)
				}
				block.Bindings[decl.Name.Text] = target.Slot
				continue
			}
			var usage sqltype.Usage
			var record bool
			var err error
			inherit := false
			if len(decl.Type.Copy.Parts) > 0 && !decl.Type.Row {
				names := plNames(decl.Type.Copy)
				if slot, found := p.Lookup(names, scopes); found {
					usage = p.Variables[slot].Usage
					record = p.Variables[slot].Record
					inherit = p.Variables[slot].InheritInputCollation
				} else if len(names) == 1 {
					return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf("variable %q does not exist", names[0]), Context: fmt.Sprintf("compilation of PL/pgSQL function %q near line %d", definition.Name, strings.Count(definition.Source[:decl.Name.Pos.Offset], "\n")+1)}
				} else {
					usage, record, err = resolve(decl.Type)
				}
			} else {
				usage, record, err = resolve(decl.Type)
				inherit = !record && usage.Collation.ID != 0 && len(decl.Type.Copy.Parts) == 0
			}
			if err != nil {
				return nil, err
			}
			if decl.NotNull && decl.Default == nil {
				return nil, parser.PLPositionError("22004", fmt.Sprintf("variable %q must have a default value, since it's declared NOT NULL", decl.Name.Text), decl.NotNullPos)
			}
			slot := len(p.Variables)
			p.Variables = append(p.Variables, PLVariable{Name: decl.Name.Text, Usage: usage, Constant: decl.Constant, NotNull: decl.NotNull, Record: record, InheritInputCollation: inherit})
			var initializerScope *PLBlock
			if decl.Default != nil {
				initializerScope = &PLBlock{Label: block.Label, Bindings: maps.Clone(block.Bindings)}
			}
			block.Bindings[decl.Name.Text] = slot
			block.Declarations = append(block.Declarations, PLInitializer{Slot: slot, Expression: decl.Default, Query: plExpressionQuery(decl.Default), Scope: initializerScope})
		}
		var err error
		block.Statements, err = compileStatements(raw.Statements, scopes)
		if err != nil {
			return nil, err
		}
		if len(raw.Handlers) != 0 {
			usage, record, err := resolve(parser.PLType{Type: parser.TypeName{Name: "text"}})
			if err != nil {
				return nil, err
			}
			if record {
				return nil, fmt.Errorf("stuntgres: exception intrinsic requires TEXT")
			}
			block.HandlerScope = &PLBlock{Bindings: make(map[string]int, 2)}
			for _, name := range []string{"sqlstate", "sqlerrm"} {
				block.HandlerScope.Bindings[name] = len(p.Variables)
				p.Variables = append(p.Variables, PLVariable{Name: name, Usage: usage, Constant: true})
			}
			handlerScopes := append(scopes, block.HandlerScope)
			for i := range raw.Handlers {
				rawHandler := &raw.Handlers[i]
				handler := PLHandler{Syntax: rawHandler}
				for _, rawCondition := range rawHandler.Conditions {
					condition, err := compilePLCondition(rawCondition)
					if err != nil {
						return nil, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf("unrecognized exception condition %q", rawCondition.Name.Text), Context: fmt.Sprintf("compilation of PL/pgSQL function %q near line %d", definition.Name, strings.Count(definition.Source[:rawCondition.Pos.Offset], "\n")+1)}
					}
					handler.Conditions = append(handler.Conditions, condition)
				}
				handler.Statements, err = compileStatements(rawHandler.Statements, handlerScopes)
				if err != nil {
					return nil, err
				}
				block.Handlers = append(block.Handlers, handler)
			}
		}
		return block, nil
	}
	root, err := compile(syntax, []*PLBlock{outer})
	if err != nil {
		return nil, err
	}
	// Function formals retain their implicit outer label for expression lookup.
	outer.Statements = []PLInstruction{{Block: root}}
	p.Root = outer
	return p, nil
}
func plNames(name parser.ObjectName) []string {
	parts := make([]string, len(name.Parts))
	for i, p := range name.Parts {
		parts[i] = p.Text
	}
	return parts
}
func (p *PLProgram) Lookup(names []string, scopes []*PLBlock) (int, bool) {
	if len(names) == 0 {
		return 0, false
	}
	if len(names) == 1 {
		for i := len(scopes) - 1; i >= 0; i-- {
			if slot, ok := scopes[i].Bindings[names[0]]; ok {
				return slot, true
			}
		}
	}
	if len(names) == 2 {
		for i := len(scopes) - 1; i >= 0; i-- {
			if scopes[i].Label == names[0] {
				slot, ok := scopes[i].Bindings[names[1]]
				return slot, ok
			}
		}
	}
	return 0, false
}
func (p *PLProgram) ResolveTarget(raw parser.Expr, scopes []*PLBlock) (PLTarget, error) {
	var names []string
	switch raw := raw.(type) {
	case parser.ParameterExpr:
		names = []string{"$" + strconv.Itoa(raw.Number)}
	case parser.ColumnExpr:
		names = plNames(raw.Name)
	default:
		return PLTarget{}, parser.PLPositionError("42601", "invalid assignment target", raw.SourcePosition())
	}
	for n := len(names); n > 0; n-- {
		if slot, ok := p.Lookup(names[:n], scopes); ok {
			return PLTarget{Slot: slot, Fields: names[n:]}, nil
		}
	}
	return PLTarget{}, parser.PLPositionError("42704", fmt.Sprintf("variable %q does not exist", names[0]), raw.SourcePosition())
}

// PLFrame owns all invocation values; definitions and caller parameters remain
// immutable. Exiting a block erases only its declarations, not aliased slots.
type PLFrame struct {
	Program        *PLProgram
	Values         []sqltype.Value
	Scopes         []*PLBlock
	InputCollation sqltype.CollationRef
	handled        *sqlstate.Error
}

func NewPLFrame(p *PLProgram, d *Definition, input *Frame, collation sqltype.CollationRef) *PLFrame {
	f := &PLFrame{Program: p, Values: make([]sqltype.Value, len(p.Variables)), InputCollation: collation}
	for i, v := range p.Variables[:p.FormalCount] {
		f.Values[i] = sqltype.Null(v.Usage.Type)
	}
	for formal, index := range d.FormalInputs {
		if index >= 0 {
			f.Values[formal] = input.Parameters[index]
		}
	}
	return f
}
func (f *PLFrame) Enter(block *PLBlock) { f.Scopes = append(f.Scopes, block) }
func (f *PLFrame) Leave(block *PLBlock) {
	for _, decl := range block.Declarations {
		f.Values[decl.Slot] = sqltype.Value{}
	}
	f.Scopes = f.Scopes[:len(f.Scopes)-1]
}
func (f *PLFrame) Close() {
	clear(f.Values)
	f.Values = nil
	f.Scopes = nil
	f.handled = nil
	f.InputCollation = sqltype.CollationRef{}
}

// EnterHandler installs invocation-private intrinsic values and returns the
// diagnostic restored by LeaveHandler. Nested blocks never mutate the program.
func (f *PLFrame) EnterHandler(scope *PLBlock, diagnostic *sqlstate.Error) *sqlstate.Error {
	previous := f.handled
	f.handled = diagnostic
	f.Enter(scope)
	f.Values[scope.Bindings["sqlstate"]] = sqltype.String(f.Program.Variables[scope.Bindings["sqlstate"]].Usage.Type, diagnostic.Code)
	f.Values[scope.Bindings["sqlerrm"]] = sqltype.String(f.Program.Variables[scope.Bindings["sqlerrm"]].Usage.Type, diagnostic.Message)
	return previous
}
func (f *PLFrame) LeaveHandler(scope *PLBlock, previous *sqlstate.Error) {
	f.Values[scope.Bindings["sqlstate"]] = sqltype.Value{}
	f.Values[scope.Bindings["sqlerrm"]] = sqltype.Value{}
	f.Leave(scope)
	f.handled = previous
}
func (f *PLFrame) HandledError() *sqlstate.Error { return f.handled }

// Usage is invocation-private metadata; published declaration Usages stay immutable.
func (f *PLFrame) Usage(slot int) sqltype.Usage {
	variable := f.Program.Variables[slot]
	usage := variable.Usage
	if variable.InheritInputCollation && f.InputCollation.ID != 0 {
		usage.Collation = f.InputCollation
	}
	return usage
}

func plExpressionQuery(raw parser.Expr) *parser.Select {
	if raw == nil {
		return nil
	}
	return &parser.Select{Targets: []parser.Expr{raw}, Pos: raw.SourcePosition()}
}
