package proc

import (
	"fmt"
	"stuntgres/internal/parser"
)

// PostgreSQL's diagnostic datum namespace includes FOUND, completed CASE
// temporaries and syntactically referenced record fields. These are not native
// executable slots. This ledger is compilation/validation scratch only.
type plDiagnosticDatums struct {
	extra  int
	fields map[plDiagnosticField]struct{}
	names  [3]parser.Identifier
}

type plDiagnosticField struct {
	parent int
	name   string
}

func (d *plDiagnosticDatums) expression(program *PLProgram, source string, start, end parser.Position, scopes []*PLBlock) error {
	return parser.VisitPLCompoundNames(source, start, end, &d.names, func(parts []parser.Identifier) error {
		return d.name(program, parts, scopes)
	})
}

// The original target span includes its assignment operator, but excludes the
// RHS. Lexical accounting therefore sees recursive field/subscript targets and
// index expressions without depending on their SQL AST classification.
func (d *plDiagnosticDatums) target(program *PLProgram, source string, statement *parser.PLStatement, scopes []*PLBlock) error {
	if statement.Target == nil {
		return nil
	}
	return d.expression(program, source, statement.Pos, statement.ExpressionStart, scopes)
}

func (d *plDiagnosticDatums) name(program *PLProgram, parts []parser.Identifier, scopes []*PLBlock) error {
	if len(parts) < 2 {
		return nil
	}
	parent, used, found, err := plDiagnosticParent(program, parts, scopes)
	if err != nil {
		return err
	}
	if !found || used == len(parts) {
		return nil
	}
	field := parts[used].Text
	key := plDiagnosticField{parent: parent, name: field}
	if _, exists := d.fields[key]; exists {
		return nil
	}
	if d.fields == nil {
		d.fields = make(map[plDiagnosticField]struct{})
	}
	d.fields[key] = struct{}{}
	d.extra++
	return nil
}

// plDiagnosticParent mirrors plpgsql_ns_lookup, not ordinary runtime Lookup.
// Eligibility is component-sensitive and applied within each lexical level:
// unqualified before label-qualified, then the next outer level.
func plDiagnosticParent(program *PLProgram, parts []parser.Identifier, scopes []*PLBlock) (slot, used int, found bool, err error) {
	if len(parts) == 0 {
		return 0, 0, false, nil
	}
	for i := len(scopes) - 1; i >= 0; i-- {
		scope := scopes[i]
		if slot, exists := program.Lookup([]string{parts[0].Text}, scopes[i:i+1]); exists {
			eligible, err := plDiagnosticEligible(program, slot, len(parts) > 1)
			if err != nil {
				return 0, 0, false, err
			}
			if eligible {
				return slot, 1, true, nil
			}
		}
		if len(parts) > 1 && scope.Label == parts[0].Text {
			if slot, exists := program.Lookup([]string{parts[0].Text, parts[1].Text}, scopes[i:i+1]); exists {
				eligible, err := plDiagnosticEligible(program, slot, len(parts) > 2)
				if err != nil {
					return 0, 0, false, err
				}
				if eligible {
					return slot, 2, true, nil
				}
			}
		}
	}
	return 0, 0, false, nil
}

func plDiagnosticEligible(program *PLProgram, slot int, hasField bool) (bool, error) {
	if slot < 0 || slot >= len(program.Variables) {
		return false, fmt.Errorf("stuntgres: invalid PL diagnostic parent")
	}
	// A qualified two-part scalar/whole-record match stops namespace lookup,
	// even though it has no remaining component and allocates no RECFIELD.
	if !hasField {
		return true, nil
	}
	variable := program.Variables[slot]
	return variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite(), nil
}

func (d *plDiagnosticDatums) finishCase(realVariables int) string {
	name := fmt.Sprintf("__Case__Variable_%d__", realVariables+d.extra+1)
	d.extra++
	return name
}

// Cursor ROWs are parser-private PostgreSQL datums, not executable Variables.
// Bound query lexing precedes publication of the cursor in its enclosing scope.
func (d *plDiagnosticDatums) cursorDeclaration(program *PLProgram, source string, raw *parser.PLCursorDeclaration, scopes []*PLBlock) error {
	if len(raw.Arguments) != 0 {
		d.extra++
	}
	return d.expression(program, source, raw.QueryStart, raw.QueryEnd, scopes)
}

func (d *plDiagnosticDatums) cursor(program *PLProgram, source string, raw *parser.PLCursorStatement, checked *PLCursorInstruction, scopes []*PLBlock) error {
	// Actuals are lexed in WRITTEN order in the caller's namespace, before a
	// FOR's implicit loop RECORD exists. Runtime projection order is separate.
	for _, actual := range raw.Arguments {
		if err := d.expression(program, source, actual.Start, actual.End, scopes); err != nil {
			return err
		}
	}
	if err := d.expression(program, source, raw.QueryStart, raw.QueryEnd, scopes); err != nil {
		return err
	}
	if err := d.expression(program, source, raw.CommandStart, raw.CommandEnd, scopes); err != nil {
		return err
	}
	for _, value := range raw.Using {
		if err := d.expression(program, source, value.Start, value.End, scopes); err != nil {
			return err
		}
	}
	if err := d.expression(program, source, raw.Motion.CountStart, raw.Motion.CountEnd, scopes); err != nil {
		return err
	}
	if err := d.expression(program, source, raw.TargetsStart, raw.TargetsEnd, scopes); err != nil {
		return err
	}
	if raw.Action == parser.PLFetchCursor {
		wholeRecord := false
		if len(checked.Targets) == 1 {
			target := checked.Targets[0]
			variable := program.Variables[target.Slot]
			wholeRecord = len(target.Steps) == 0 && (variable.Record || variable.Usage.Type != nil && variable.Usage.Type.BaseType().IsComposite())
		}
		if !wholeRecord {
			d.extra++
		} // One ROW per scalar target list, even singleton.
	}
	return nil
}
