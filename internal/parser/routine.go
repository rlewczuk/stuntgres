package parser

import (
	"strings"
	"unicode/utf8"
)

// Call is a standalone routine invocation, not an expression statement.
type Call struct {
	Function FunctionExpr
	Pos      Position
}

func (*Call) statement() {}

// CreateFunction is the shared SQL function/procedure declaration. Body inputs
// are private routine parameters, never parameters of the enclosing command.
type CreateFunction struct {
	Name            ObjectName
	Arguments       []RoutineParameter
	Result          TypeName
	ResultSpecified bool
	TableResult     []RoutineParameter
	Language        Identifier
	Body            string
	BodyPos         Position
	Symbol          string
	SymbolSpecified bool
	SymbolPos       Position
	Procedure       bool
	Attributes      []RoutineAttribute
	Replace         bool
	Standard        []Statement // Non-nil for a definition-analyzed SQL-standard body.
	Pos             Position
}

// RoutineAttribute retains utility-validation input in source order.
// Security values are normalized unquoted "definer" or "invoker" keywords.
type RoutineAttribute struct {
	Kind    string
	Value   string
	Setting *Set
	Pos     Position
}

func (d *CreateFunction) setProcedureAttributes(groups ...[]RoutineAttribute) {
	for _, group := range groups {
		d.Attributes = append(d.Attributes, group...)
	}
}

func (l *lexer) functionAttributes(declaration *CreateFunction, groups ...[]RoutineAttribute) bool {
	if declaration.TableResult != nil {
		for _, argument := range declaration.Arguments {
			if argument.Mode == "out" || argument.Mode == "inout" {
				l.failAt("42601", "OUT and INOUT arguments aren't allowed in TABLE functions", argument.Pos.Offset)
				return false
			}
		}
	}
	for _, clauses := range groups {
		declaration.Attributes = append(declaration.Attributes, clauses...)
	}
	return true
}

// RoutineParameter is declaration syntax; identity-only DROP/GRANT arguments
// deliberately have no default expression.
type RoutineParameter struct {
	GrantRoutineArgument
	Default Expr
}

type routineResult struct {
	Type      TypeName
	Specified bool
	Table     []RoutineParameter
}

type routineAS struct {
	Body      lexeme
	Symbol    lexeme
	HasSymbol bool
}

// CreateLanguage admits explicit handler syntax, not interpreter provisioning.
// Names retain their source positions even though native execution rejects it.
type CreateLanguage struct {
	Name       Identifier
	Handler    ObjectName
	Inline     ObjectName
	Validator  ObjectName
	Replace    bool
	Trusted    bool
	Procedural bool
	Pos        Position
}

func (*CreateLanguage) statement() {}

// BodyPos is the position immediately preceding the decoded body. Keep the
// actual quote delimiter width, rather than assuming every body uses $$.
func routineBodyPosition(token lexeme) Position {
	width := 0
	if strings.HasPrefix(token.raw, "$") {
		if end := strings.IndexByte(token.raw[1:], '$'); end >= 0 {
			width = end + 2
		}
	} else if quote := strings.IndexByte(token.raw, '\''); quote >= 0 {
		width = quote + 1
	}
	position := token.pos
	if width > 0 {
		position.Offset += width - 1
		position.Character += utf8.RuneCountInString(token.raw[:width]) - 1
	}
	return position
}

func (*CreateFunction) statement() {}

// CreateAggregate retains the declaration split independently of its canonical
// flattened identity, including ordered aggregates with no direct arguments.
type CreateAggregate struct {
	Name       ObjectName
	Arguments  []GrantRoutineArgument
	Ordered    bool
	DirectArgs int
	Options    []OperatorOption
	Pos        Position
}

func (*CreateAggregate) statement() {}

// DropRoutine identifies catalog signatures, never call-site overload coercions.
type DropRoutine struct {
	Kind     string
	Targets  []GrantRoutineTarget
	IfExists bool
	Mode     DropMode
	Pos      Position
}

func (*DropRoutine) statement() {}

// AlterRoutine retains the requested command kind and exact utility identity.
// OwnerIdentity distinguishes role keywords from identically spelled names.
type AlterRoutine struct {
	Kind          string
	Routine       GrantRoutineTarget
	Action        string
	Target        Identifier
	OwnerIdentity bool
	Attributes    []RoutineAttribute
	Pos           Position
}

func (*AlterRoutine) statement() {}

// Routine and cast utility notices retain PostgreSQL's alias-normalized
// TypeName syntax, unlike canonical catalog signature diagnostics.
func routineUtilityTarget(target GrantRoutineTarget) GrantRoutineTarget {
	for i := range target.Arguments {
		target.Arguments[i].Type = standaloneType(target.Arguments[i].Type)
	}
	return target
}

func dropRoutineTargets(targets []GrantRoutineTarget) []GrantRoutineTarget {
	for i := range targets {
		targets[i] = routineUtilityTarget(targets[i])
	}
	return targets
}
