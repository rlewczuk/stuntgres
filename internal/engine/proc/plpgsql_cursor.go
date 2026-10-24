package proc

import (
	"fmt"
	"maps"
	"stuntgres/internal/parser"
	"stuntgres/internal/sqlstate"
	"stuntgres/internal/sqltype"
)

type PLSlotRole uint8

const (
	PLOrdinarySlot PLSlotRole = iota
	PLFoundSlot
	PLCursorArgumentSlot
	PLLoopRecordSlot
	PLIntegerForSlot
)

// Recipes contain immutable syntax and lexical slot references only. All typed
// metadata and all values remain in Variables and PLFrame respectively.
type PLCursorRecipe struct {
	Syntax    *parser.PLCursorDeclaration
	Slot      int
	Arguments []int
	Scopes    []*PLBlock
}
type PLCursorInstruction struct {
	Slot          int
	Arguments     []PLInitializer // Declaration-ordered destinations and original actual spans.
	ArgumentQuery *parser.Select  // One ordinary, uncoerced projection; never per-argument execution.
	Targets       []PLTarget
	Loop          *PLBlock
	LoopSlot      int
}

func snapshotPLScopes(scopes []*PLBlock) []*PLBlock {
	out := make([]*PLBlock, len(scopes))
	for i, s := range scopes {
		out[i] = &PLBlock{Label: s.Label, FunctionScope: s.FunctionScope, Bindings: maps.Clone(s.Bindings)}
	}
	return out
}

func (p *PLProgram) compileCursorDeclaration(decl parser.PLDeclaration, block *PLBlock, scopes []*PLBlock, collate PLCollationResolver, resolve PLTypeResolver, diagnostics *plDiagnosticDatums) error {
	usage, record, err := resolve(parser.PLType{Type: parser.TypeName{Name: "refcursor", Schema: &parser.Identifier{Text: "pg_catalog"}, Pos: decl.Name.Pos}})
	if err != nil {
		return err
	}
	if record || usage.Type.ID() != sqltype.Refcursor {
		return fmt.Errorf("stuntgres: invalid cursor type resolver")
	}
	slot := len(p.Variables)
	recipe := &PLCursorRecipe{Syntax: decl.Cursor, Slot: slot, Scopes: snapshotPLScopes(scopes)}
	p.Variables = append(p.Variables, PLVariable{Name: decl.Name.Text, Usage: usage, Cursor: recipe})
	argumentScope := &PLBlock{Label: decl.Name.Text, Bindings: make(map[string]int)}
	argumentScopes := append(scopes, argumentScope)
	for _, argument := range decl.Cursor.Arguments {
		if _, exists := argumentScope.Bindings[argument.Name.Text]; exists {
			return parser.PLPositionError("42601", fmt.Sprintf("duplicate declaration at or near %q", argument.Name.Text), argument.Name.Pos)
		}
		u, r, inherit, e := p.resolvePLVariableType(argument, argumentScopes, collate, resolve)
		if e != nil {
			return e
		}
		if r {
			return parser.PLPositionError("0A000", "cursor arguments must have a concrete type", argument.Name.Pos)
		}
		index := len(p.Variables)
		p.Variables = append(p.Variables, PLVariable{Name: argument.Name.Text, Usage: u, Role: PLCursorArgumentSlot, InheritInputCollation: inherit})
		recipe.Arguments = append(recipe.Arguments, index)
		argumentScope.Bindings[argument.Name.Text] = index
	}
	recipe.Scopes = append(recipe.Scopes, argumentScope)
	if err := diagnostics.cursorDeclaration(p, p.Source, decl.Cursor, recipe.Scopes); err != nil {
		return err
	}
	block.Bindings[decl.Name.Text] = slot
	block.Declarations = append(block.Declarations, PLInitializer{Slot: slot})
	return nil
}

func (p *PLProgram) compileCursor(raw *parser.PLCursorStatement, scopes []*PLBlock, pos parser.Position) (*PLCursorInstruction, error) {
	target, err := p.ResolveTarget(raw.Variable, scopes)
	if err != nil {
		return nil, plUnknownTargetError(raw.Variable, err)
	}
	if len(target.Steps) != 0 {
		return nil, parser.PLPositionError("42804", "cursor variable must be a simple variable", raw.Variable.SourcePosition())
	}
	variable := p.Variables[target.Slot]
	if variable.Record || !variable.Usage.Valid() || variable.Usage.Type.ID() != sqltype.Refcursor {
		return nil, parser.PLPositionError("42804", fmt.Sprintf("variable %q must be of type cursor or refcursor", variable.Name), raw.Variable.SourcePosition())
	}
	out := &PLCursorInstruction{Slot: target.Slot, LoopSlot: -1}
	if raw.Action == parser.PLFetchCursor && raw.Motion.Counted {
		return nil, parser.PLPositionError("0A000", "FETCH statement cannot return multiple rows", pos)
	}
	if raw.Action == parser.PLForCursor && variable.Cursor == nil {
		return nil, parser.PLPositionError("42601", "cursor FOR loop must use a bound cursor variable", raw.Variable.SourcePosition())
	}
	if raw.Action == parser.PLOpenCursor || raw.Action == parser.PLForCursor {
		recipe := variable.Cursor
		if recipe == nil {
			if raw.HasArguments {
				return nil, parser.PLPositionError("42601", fmt.Sprintf("cursor %q has no arguments", variable.Name), raw.ArgumentsStart)
			}
			if raw.Query == nil && raw.Command == nil {
				return nil, parser.PLPositionError("42601", "expected FOR", raw.Variable.SourcePosition())
			}
		} else {
			if raw.Query != nil || raw.Command != nil {
				return nil, parser.PLPositionError("42601", "bound cursor must not have FOR", raw.Variable.SourcePosition())
			}
			if len(recipe.Arguments) > 0 && !raw.HasArguments {
				return nil, parser.PLPositionError("42601", fmt.Sprintf("cursor %q has arguments", variable.Name), raw.ArgumentsStart)
			}
			if len(recipe.Arguments) == 0 && raw.HasArguments {
				return nil, parser.PLPositionError("42601", fmt.Sprintf("cursor %q has no arguments", variable.Name), raw.ArgumentsStart)
			}
			out.Arguments = make([]PLInitializer, len(recipe.Arguments))
			assigned := make([]bool, len(recipe.Arguments))
			for i, actual := range raw.Arguments {
				index := i
				if actual.Name.Text != "" {
					index = -1
					for j, slot := range recipe.Arguments {
						if p.Variables[slot].Name == actual.Name.Text {
							index = j
							break
						}
					}
					if index < 0 {
						return nil, parser.PLPositionError("42601", fmt.Sprintf("cursor %q has no argument named %q", variable.Name, actual.Name.Text), actual.Name.Pos)
					}
				}
				if index >= len(assigned) {
					return nil, parser.PLPositionError("42601", fmt.Sprintf("too many arguments for cursor %q", variable.Name), actual.Separator)
				}
				if assigned[index] {
					pos := actual.Expression.SourcePosition()
					if actual.Name.Text != "" {
						pos = actual.Name.Pos
					}
					return nil, parser.PLPositionError("42601", fmt.Sprintf("value for parameter %q of cursor %q specified more than once", p.Variables[recipe.Arguments[index]].Name, variable.Name), pos)
				}
				assigned[index] = true
				out.Arguments[index] = PLInitializer{Slot: recipe.Arguments[index], Expression: actual.Expression, Start: actual.Start, End: actual.End, Delimiter: actual.Delimiter}
			}
			for _, present := range assigned {
				if !present {
					return nil, parser.PLPositionError("42601", fmt.Sprintf("not enough arguments for cursor %q", variable.Name), raw.ArgumentsEnd)
				}
			}
			out.ArgumentQuery = p.cursorArgumentQuery(out.Arguments, raw.Arguments)
		}
	}
	for _, rawTarget := range raw.Targets {
		target, err := p.ResolveTarget(rawTarget, scopes)
		if err != nil {
			return nil, err
		}
		if p.Variables[target.Slot].Constant {
			return nil, parser.PLPositionError("22005", fmt.Sprintf("variable %q is declared CONSTANT", p.Variables[target.Slot].Name), rawTarget.SourcePosition())
		}
		out.Targets = append(out.Targets, target)
	}
	return out, nil
}

func (f *PLFrame) SetFound(found bool) {
	f.Values[f.Program.FoundSlot] = sqltype.Boolean(f.Program.Variables[f.Program.FoundSlot].Usage.Type, found)
}

func (p *PLProgram) cursorArgumentQuery(arguments []PLInitializer, actuals []parser.PLCursorActual) *parser.Select {
	if len(arguments) == 0 {
		return nil
	}
	query := &parser.Select{Targets: make([]parser.Expr, len(arguments)), Pos: arguments[0].Expression.SourcePosition()}
	for i, argument := range arguments {
		query.Targets[i] = argument.Expression
	}
	for _, actual := range actuals {
		if actual.Name.Text != "" {
			query.Labels = make([]parser.Identifier, len(arguments))
			for i, argument := range arguments {
				query.Labels[i].Text = p.Variables[argument.Slot].Name
			}
			break
		}
	}
	return query
}

// CursorName reads a cursor variable's portal name; a NULL variable has none.
func (f *PLFrame) CursorName(slot int) (string, error) {
	v := f.Values[slot]
	if v.Type() == nil || v.IsNull() {
		return "", &sqlstate.Error{Severity: "ERROR", SeverityUnlocalized: "ERROR", Code: "22004", Message: fmt.Sprintf("cursor variable %q is null", f.Program.Variables[slot].Name)}
	}
	return v.Text()
}
