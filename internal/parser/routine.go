package parser

import (
	"strings"
	"unicode/utf8"
)

// CreateFunction is the shared SQL function/procedure declaration. Body inputs
// are private routine parameters, never parameters of the enclosing command.
type CreateFunction struct {
	Name            ObjectName
	Arguments       []RoutineParameter
	Result          TypeName
	ResultSpecified bool
	Language        Identifier
	Body            string
	BodyPos         Position
	Symbol          string
	SymbolSpecified bool
	SymbolPos       Position
	Procedure       bool
	Standard        []Statement // Non-nil for a definition-analyzed SQL-standard body.
	Pos             Position
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

// CreateAggregate declares an ordinary fixed-arity transition aggregate.
// Definition elements retain their lexical node kind for SSPACE validation.
type CreateAggregate struct {
	Name      ObjectName
	Arguments []GrantRoutineArgument
	Options   []OperatorOption
	Pos       Position
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
