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
	Trigger     bool
	FoundSlot   int
	Source      string
}
type PLVariable struct {
	Name                      string
	Usage                     sqltype.Usage
	Constant, NotNull, Record bool
	InheritInputCollation     bool
	Role                      PLSlotRole
	Cursor                    *PLCursorRecipe
}
type PLBlock struct {
	Syntax        *parser.PLBlock
	Control       *PLControl
	Label         string
	FunctionScope bool // Implicit FOUND overlay; Bindings remains the canonical formal namespace.
	Bindings      map[string]int
	Declarations  []PLInitializer
	Statements    []PLInstruction
	HandlerScope  *PLBlock
	Handlers      []PLHandler
}

// Control is an immutable, acyclic owner token. Targets retain identity without
// retaining an enclosing instruction/block graph or any invocation state.
type PLControl struct {
	ID   int
	Loop bool
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
	Syntax    *parser.PLStatement
	Query     *parser.Select
	Block     *PLBlock
	Target    PLTarget
	If        *PLConditional
	Case      *PLConditional
	Cursor    *PLCursorInstruction
	Loop      *PLBlock
	Transfer  *PLControl
	QueryLoop *PLQueryLoop
	Foreach   *PLForeach
	Raise     *PLRaise
	Assert    *PLAssert
}
type PLQueryLoop struct {
	Target PLTarget
	Query  parser.Statement
	Body   *PLBlock
}
type PLForeach struct {
	Targets        []PLTarget
	Query          *parser.Select
	Slice          int
	SliceSpecified bool
	SlicePos       parser.Position
	Body           *PLBlock
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
type PLCollationResolver func(sqltype.Usage, *parser.ObjectName) (sqltype.Usage, error)

func CompilePL(definition *Definition, syntax *parser.PLBlock, procedure bool, collate PLCollationResolver, resolve PLTypeResolver) (*PLProgram, error) {
	if resolve == nil || collate == nil {
		return nil, fmt.Errorf("stuntgres: missing PL declaration resolver")
	}
	p := &PLProgram{Syntax: syntax, FormalCount: len(definition.Formals), Function: definition.Name, Procedure: procedure, Source: definition.Source}
	returnPolicy := PLReturnPolicy(definition.Result, len(definition.OutputFormals), procedure)
	var diagnostics plDiagnosticDatums
	outer := &PLBlock{Label: definition.Name, Bindings: make(map[string]int), FunctionScope: true, Control: &PLControl{ID: 1}}
	p.Root = outer
	if definition.Trigger {
		if procedure || len(definition.Formals) != 0 || definition.Result.Type == nil || definition.Result.Type.ID() != 2279 {
			return nil, fmt.Errorf("stuntgres: invalid PL trigger declaration")
		}
		variables, err := TriggerVariables(resolve)
		if err != nil {
			return nil, err
		}
		p.Trigger = true
		p.Variables = variables
		for i, v := range variables {
			outer.Bindings[v.Name] = i
		}
	}
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
	found, record, err := resolve(parser.PLType{Type: parser.TypeName{Name: "bool", Schema: &parser.Identifier{Text: "pg_catalog"}}})
	if err != nil {
		return nil, err
	}
	if record || found.Type == nil || found.Type.ID() != sqltype.Bool {
		return nil, fmt.Errorf("stuntgres: missing PL FOUND boolean type")
	}
	p.FoundSlot = len(p.Variables)
	p.Variables = append(p.Variables, PLVariable{Name: "found", Usage: found, Role: PLFoundSlot})
	nextControl := 2
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
			checked.SearchName = diagnostics.finishCase(len(p.Variables) - 1) // FOUND already has a real slot.
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
			case statement.Raise != nil:
				instruction.Raise, err = p.compileRaise(statement.Raise, statement.Pos)
				if err == nil {
					for _, operand := range statement.Raise.Arguments {
						if err = diagnostics.expression(p, definition.Source, operand.Start, operand.End, scopes); err != nil {
							break
						}
					}
				}
				if err == nil {
					for _, option := range statement.Raise.Options {
						if err = diagnostics.expression(p, definition.Source, option.Value.Start, option.Value.End, scopes); err != nil {
							break
						}
					}
				}
			case statement.Assert != nil:
				rawAssert := statement.Assert
				instruction.Assert = &PLAssert{Condition: plExpressionQuery(rawAssert.Condition.Expression), Message: plExpressionQuery(rawAssert.Message.Expression)}
				err = diagnostics.expression(p, definition.Source, rawAssert.Condition.Start, rawAssert.Condition.End, scopes)
				if err == nil {
					err = diagnostics.expression(p, definition.Source, rawAssert.Message.Start, rawAssert.Message.End, scopes)
				}
			case statement.Foreach != nil:
				rawLoop := statement.Foreach
				targets, targetErr := p.foreachTargets(rawLoop, scopes)
				if targetErr != nil {
					return nil, targetErr
				}
				if err := diagnostics.foreach(p, rawLoop, scopes); err != nil {
					return nil, err
				}
				body, bodyErr := compile(rawLoop.Body, scopes)
				if bodyErr != nil {
					return nil, bodyErr
				}
				instruction.Foreach = &PLForeach{Targets: targets, Query: plExpressionQuery(rawLoop.Source.Expression), Slice: rawLoop.Slice, SliceSpecified: rawLoop.SliceSpecified, SlicePos: rawLoop.SlicePos, Body: body}
			case statement.QueryLoop != nil:
				rawLoop := statement.QueryLoop
				target, targetErr := p.queryLoopTarget(rawLoop.Target, scopes)
				if targetErr != nil {
					return nil, targetErr
				}
				if err := diagnostics.expression(p, definition.Source, rawLoop.Target.SourcePosition(), rawLoop.Start, scopes); err != nil {
					return nil, err
				}
				if err := diagnostics.expression(p, definition.Source, rawLoop.Start, rawLoop.End, scopes); err != nil {
					return nil, err
				}
				body, bodyErr := compile(rawLoop.Body, scopes)
				if bodyErr != nil {
					return nil, bodyErr
				}
				instruction.QueryLoop = &PLQueryLoop{Target: target, Query: rawLoop.Query, Body: body}
			case statement.Cursor != nil:
				cursor, err := p.compileCursor(statement.Cursor, scopes, statement.Pos)
				if err != nil {
					return nil, err
				}
				instruction.Cursor = cursor
				if err := diagnostics.cursor(p, definition.Source, statement.Cursor, cursor, scopes); err != nil {
					return nil, err
				}
				if statement.Cursor.Loop != nil {
					loopSlot := len(p.Variables)
					p.Variables = append(p.Variables, PLVariable{Name: statement.Cursor.LoopName.Text, Record: true, Role: PLLoopRecordSlot})
					loopScope := &PLBlock{Bindings: map[string]int{statement.Cursor.LoopName.Text: loopSlot}}
					loop, err := compile(statement.Cursor.Loop, append(scopes, loopScope))
					if err != nil {
						return nil, err
					}
					loopScope.Statements = []PLInstruction{{Block: loop}}
					cursor.Loop, cursor.LoopSlot = loopScope, loopSlot
				}
			case statement.Loop != nil:
				instruction.Loop, err = compile(statement.Loop, scopes)
			case statement.Transfer != nil:
				instruction.Transfer, err = resolvePLControl(statement.Transfer, statement.Pos, scopes)
				if statement.Transfer.Alias.Text != "" && instruction.Query != nil {
					instruction.Query.Labels = []parser.Identifier{statement.Transfer.Alias}
				}
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
				if err != nil {
					return nil, plUnknownTargetError(statement.Target, err)
				} else if p.Variables[instruction.Target.Slot].Constant {
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
		block := &PLBlock{Syntax: raw, Label: raw.Label.Text, Bindings: make(map[string]int), Control: &PLControl{ID: nextControl, Loop: raw.IsLoop}}
		nextControl++
		scopes = append(scopes, block)
		for _, decl := range raw.Declarations {
			if _, exists := block.Bindings[decl.Name.Text]; exists {
				return nil, parser.PLPositionError("42601", fmt.Sprintf("duplicate declaration at or near %q", decl.Name.Text), decl.Name.Pos)
			}
			if decl.Cursor != nil {
				if err := p.compileCursorDeclaration(decl, block, scopes, collate, resolve, &diagnostics); err != nil {
					return nil, err
				}
				continue
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
			usage, record, inherit, err := p.resolvePLVariableType(decl, scopes, collate, resolve)
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
			if names[0] == "found" && scopes[i].FunctionScope {
				return p.FoundSlot, true
			}
			if slot, ok := scopes[i].Bindings[names[0]]; ok {
				return slot, true
			}
		}
	}
	if len(names) == 2 {
		for i := len(scopes) - 1; i >= 0; i-- {
			if scopes[i].Label == names[0] {
				if names[1] == "found" && scopes[i].FunctionScope {
					return p.FoundSlot, true
				}
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

// Field targets now retain ordered steps instead of a flattened ColumnExpr.
// Keep the same complete unknown-variable diagnostic for either representation.
func plUnknownTargetError(raw parser.Expr, fallback error) error {
	root, steps, ok := plTargetPath(raw)
	column, named := root.(parser.ColumnExpr)
	if !ok || !named {
		return fallback
	}
	names := plNames(column.Name)
	for _, step := range steps {
		if step.Field == nil {
			return fallback
		}
		names = append(names, step.Field.Text)
	}
	return parser.PLPositionError("42601", fmt.Sprintf("%q is not a known variable", strings.Join(names, ".")), raw.SourcePosition())
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
	f.SetFound(false)
	return f
}
func (f *PLFrame) Enter(block *PLBlock) { f.Scopes = append(f.Scopes, block) }
func (f *PLFrame) Leave(block *PLBlock) {
	for _, decl := range block.Declarations {
		if recipe := f.Program.Variables[decl.Slot].Cursor; recipe != nil {
			for _, slot := range recipe.Arguments {
				f.Values[slot] = sqltype.Value{}
			}
		}
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

func (p *PLProgram) resolvePLVariableType(decl parser.PLDeclaration, scopes []*PLBlock, collate PLCollationResolver, resolve PLTypeResolver) (sqltype.Usage, bool, bool, error) {
	var usage sqltype.Usage
	var record, inherit, local bool
	if len(decl.Type.Copy.Parts) > 0 && !decl.Type.Row {
		names := plNames(decl.Type.Copy)
		if slot, found := p.Lookup(names, scopes); found {
			v := p.Variables[slot]
			usage, record, inherit, local = v.Usage, v.Record, v.InheritInputCollation, true
		} else if len(names) == 1 {
			return sqltype.Usage{}, false, false, &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "42704", Message: fmt.Sprintf("variable %q does not exist", names[0]), Context: fmt.Sprintf("compilation of PL/pgSQL function %q near line %d", p.Function, strings.Count(p.Source[:decl.Name.Pos.Offset], "\n")+1)}
		}
	}
	if !local {
		var err error
		usage, record, err = resolve(decl.Type)
		if err != nil {
			return usage, record, false, err
		}
		inherit = !record && usage.Collation.ID != 0 && len(decl.Type.Copy.Parts) == 0
	}
	if decl.Collation != nil {
		var err error
		usage, err = collate(usage, decl.Collation)
		if err != nil {
			return usage, record, false, err
		}
		inherit = false
	}
	return usage, record, inherit, nil
}
