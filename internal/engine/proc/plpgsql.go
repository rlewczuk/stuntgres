package proc

import (
	"fmt"
	"maps"
	"strconv"
	"strings"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
	"unicode/utf8"
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
	SearchCount int
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
	Start      parser.Position
	End        parser.Position
	Delimiter  parser.Position
	Expression parser.Expr
	Query      *parser.Select
	Scope      *PLBlock
}
type PLTarget struct {
	Slot  int
	Steps []parser.TargetStep
}
type PLInstruction struct {
	Syntax *parser.PLStatement
	Query  *parser.Select
	Block  *PLBlock
	Target PLTarget
	If     *PLConditional
	Case   *PLConditional
}
type PLConditional struct {
	Search     *parser.Select
	HasElse    bool
	SearchSlot int
	SearchName string
	Arms       []PLBranch
	Else       []PLInstruction
}
type PLBranch struct {
	Query      *parser.Select
	Statements []PLInstruction
	// DiagnosticQuery describes PG's private IN expression, not executable SQL.
	DiagnosticQuery    string
	OperatorPosition   parser.Position
	DiagnosticOperator int
	DiagnosticItems    int
}

const plSearchPrefix = "\x00case:"

func PLSearchName(slot int) string { return plSearchPrefix + strconv.Itoa(slot) }

// Synthetic operator coordinates are strictly beyond body EOF. Only the CASE
// diagnostic mapper consumes them; query and source-span origins remain real.
func PLSearchOperatorPosition(source string, slot int) parser.Position {
	return parser.Position{Offset: len(source) + 2 + slot, Character: utf8.RuneCountInString(source) + 2 + slot}
}

func (p *PLProgram) SearchSlot(name string) (int, bool) {
	if !strings.HasPrefix(name, plSearchPrefix) {
		return 0, false
	}
	suffix := strings.TrimPrefix(name, plSearchPrefix)
	if suffix == "" || len(suffix) > 1 && suffix[0] == '0' {
		return 0, false
	}
	for _, digit := range suffix {
		if digit < '0' || digit > '9' {
			return 0, false
		}
	}
	slot, err := strconv.Atoi(suffix)
	return slot, err == nil && slot >= 0 && slot < p.SearchCount
}

func PLReturnPolicy(result sqltype.Usage, outputs int, procedure bool) parser.PLReturnPolicy {
	switch {
	case procedure:
		return parser.PLReturnPolicy{Code: "42601", Message: "RETURN cannot have a parameter in a procedure"}
	case outputs != 0:
		return parser.PLReturnPolicy{Code: "42804", Message: "RETURN cannot have a parameter in function with OUT parameters"}
	case result.Type != nil && result.Type.ID() == sqltype.Void:
		return parser.PLReturnPolicy{Code: "42804", Message: "RETURN cannot have a parameter in function returning void"}
	default:
		return parser.PLReturnPolicy{}
	}
}

type PLTypeResolver func(parser.PLType) (sqltype.Usage, bool, error)

func CompilePL(definition *Definition, syntax *parser.PLBlock, procedure bool, resolve PLTypeResolver) (*PLProgram, error) {
	p := &PLProgram{Syntax: syntax, FormalCount: len(definition.Formals), Function: definition.Name, Procedure: procedure}
	returnPolicy := PLReturnPolicy(definition.Result, len(definition.OutputFormals), procedure)
	var diagnostics plDiagnosticDatums
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
	var compileStatements func([]parser.PLStatement, []*PLBlock) ([]PLInstruction, error)
	var compileConditional func(*parser.PLConditional, bool, []*PLBlock) (*PLConditional, error)
	compileConditional = func(raw *parser.PLConditional, simple bool, scopes []*PLBlock) (*PLConditional, error) {
		checked := &PLConditional{SearchSlot: -1, Search: plExpressionQuery(raw.Search.Expression), HasElse: raw.HasElse, Arms: make([]PLBranch, len(raw.Arms))}
		if err := diagnostics.expression(p, definition.Source, raw.Search.Start, raw.Search.End, scopes); err != nil {
			return nil, err
		}
		var operatorPosition parser.Position
		if simple {
			checked.SearchSlot = p.SearchCount
			operatorPosition = PLSearchOperatorPosition(definition.Source, checked.SearchSlot)
			p.SearchCount++
		}
		for i, arm := range raw.Arms {
			if len(arm.Conditions) == 0 || !simple && len(arm.Conditions) != 1 {
				return nil, parser.PLPositionError("42601", "invalid CASE condition list", raw.Search.End)
			}
			for _, condition := range arm.Conditions {
				if err := diagnostics.expression(p, definition.Source, condition.Start, condition.End, scopes); err != nil {
					return nil, err
				}
			}
			branch := PLBranch{}
			expression := arm.Conditions[0].Expression
			if simple {
				items := make([]parser.Expr, len(arm.Conditions))
				for i, item := range arm.Conditions {
					items[i] = item.Expression
				}
				pos := expression.SourcePosition()
				left := parser.ColumnExpr{Name: parser.ObjectName{Parts: []parser.Identifier{{Text: PLSearchName(checked.SearchSlot), Pos: pos}}}, Pos: pos}
				branch.OperatorPosition = operatorPosition
				expression = parser.InListExpr{Left: left, Items: items, Pos: branch.OperatorPosition}
			}
			branch.Query = plExpressionQuery(expression)
			branch.Query.Pos = arm.Conditions[0].Expression.SourcePosition()
			var err error
			branch.Statements, err = compileStatements(arm.Statements, scopes)
			if err != nil {
				return nil, err
			}
			checked.Arms[i] = branch
		}
		var err error
		checked.Else, err = compileStatements(raw.Else, scopes)
		if err != nil {
			return nil, err
		}
		if simple {
			checked.SearchName = diagnostics.finishCase(len(p.Variables))
			prefix := "\"" + checked.SearchName + "\" IN ("
			for i, arm := range raw.Arms {
				branch := &checked.Arms[i]
				start := arm.Conditions[0].Start.Offset
				end := arm.Conditions[len(arm.Conditions)-1].End.Offset
				branch.DiagnosticQuery = prefix + definition.Source[start:end] + ")"
				branch.DiagnosticOperator = len(checked.SearchName) + 4
				branch.DiagnosticItems = len(prefix) + 1
			}
		}
		return checked, err
	}
	compileStatements = func(raw []parser.PLStatement, scopes []*PLBlock) ([]PLInstruction, error) {
		instructions := make([]PLInstruction, 0, len(raw))
		for i := range raw {
			statement := &raw[i]
			instruction := PLInstruction{Syntax: statement, Query: plExpressionQuery(statement.Expression)}
			if err := diagnostics.target(p, definition.Source, statement, scopes); err != nil {
				return nil, err
			}
			if err := diagnostics.expression(p, definition.Source, statement.ExpressionStart, statement.ExpressionEnd, scopes); err != nil {
				return nil, err
			}
			var err error
			switch {
			case statement.Block != nil:
				instruction.Block, err = compile(statement.Block, scopes)
			case statement.If != nil:
				instruction.If, err = compileConditional(statement.If, false, scopes)
			case statement.Case != nil:
				instruction.Case, err = compileConditional(statement.Case, statement.Case.Search.Expression != nil, scopes)
			case statement.Rethrow:
			case statement.Return:
				message, code := "", ""
				if statement.Expression != nil {
					message, code = returnPolicy.Message, returnPolicy.Code
				} else if !procedure && !definition.Void() && len(definition.OutputFormals) == 0 {
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
				instruction.Target, err = p.ResolveTarget(statement.Target, scopes)
				if err == nil && p.Variables[instruction.Target.Slot].Constant {
					err = parser.PLPositionError("22005", fmt.Sprintf("variable %q is declared CONSTANT", p.Variables[instruction.Target.Slot].Name), statement.Pos)
				}
			}
			if err != nil {
				return nil, err
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
				if len(target.Steps) != 0 {
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
			if err := diagnostics.expression(p, definition.Source, decl.DefaultStart, decl.DefaultEnd, scopes); err != nil {
				return nil, err
			}
			block.Bindings[decl.Name.Text] = slot
			block.Declarations = append(block.Declarations, PLInitializer{Slot: slot, Expression: decl.Default, Start: decl.DefaultStart, End: decl.DefaultEnd, Delimiter: decl.DefaultDelimiter, Query: plExpressionQuery(decl.Default), Scope: initializerScope})
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
	root, steps, ok := plTargetPath(raw)
	if !ok {
		return PLTarget{}, parser.PLPositionError("42601", "invalid assignment target", raw.SourcePosition())
	}
	var names []string
	switch root := root.(type) {
	case parser.ParameterExpr:
		names = []string{"$" + strconv.Itoa(root.Number)}
	case parser.ColumnExpr:
		names = plNames(root.Name)
	}
	// A leading field can instead qualify a lexical variable by block label.
	// Resolve that longest visible root before treating any suffix as fields.
	leading := 0
	for leading < len(steps) && steps[leading].Field != nil {
		names = append(names, steps[leading].Field.Text)
		leading++
	}
	for n := len(names); n > 0; n-- {
		if slot, found := p.Lookup(names[:n], scopes); found {
			if consumed := n - (len(names) - leading); consumed >= 0 {
				return PLTarget{Slot: slot, Steps: steps[consumed:]}, nil
			}
		}
	}
	return PLTarget{}, parser.PLPositionError("42704", fmt.Sprintf("variable %q does not exist", names[0]), raw.SourcePosition())
}

func plTargetPath(raw parser.Expr) (parser.Expr, []parser.TargetStep, bool) {
	switch raw := raw.(type) {
	case parser.ColumnExpr:
		if len(raw.Name.Parts) == 0 {
			return nil, nil, false
		}
		root := raw
		root.Name.Parts = raw.Name.Parts[:1]
		steps := make([]parser.TargetStep, len(raw.Name.Parts)-1)
		for i := range steps {
			steps[i].Field = &raw.Name.Parts[i+1]
		}
		return root, steps, true
	case parser.ParameterExpr:
		return raw, nil, true
	case parser.FieldExpr:
		root, steps, ok := plTargetPath(raw.Base)
		return root, append(steps, parser.TargetStep{Field: &raw.Field}), ok && !raw.Expansion
	case parser.SubscriptExpr:
		root, steps, ok := plTargetPath(raw.Base)
		for i := range raw.Subscripts {
			steps = append(steps, parser.TargetStep{Subscript: &raw.Subscripts[i]})
		}
		return root, steps, ok && len(raw.Subscripts) > 0
	default:
		return nil, nil, false
	}
}

// PLFrame owns all invocation values; definitions and caller parameters remain
// immutable. Exiting a block erases only its declarations, not aliased slots.
type PLFrame struct {
	Program        *PLProgram
	Values         []sqltype.Value
	Scopes         []*PLBlock
	InputCollation sqltype.CollationRef
	handled        *sqlstate.Error
	Searches       []PLSearch
}
type PLSearch struct {
	Value sqltype.Value
	Usage sqltype.Usage
}

func NewPLFrame(p *PLProgram, d *Definition, input *Frame, collation sqltype.CollationRef) *PLFrame {
	f := &PLFrame{Program: p, Values: make([]sqltype.Value, len(p.Variables)), InputCollation: collation}
	if p.SearchCount != 0 {
		f.Searches = make([]PLSearch, p.SearchCount)
	}
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
	clear(f.Searches)
	f.Searches = nil
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
