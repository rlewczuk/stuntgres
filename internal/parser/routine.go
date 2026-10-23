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

// DROP notices retain PostgreSQL's alias-normalized TypeName syntax, unlike
// canonical catalog signature diagnostics. Reuse the regtype syntax authority.
func dropRoutineTargets(targets []GrantRoutineTarget) []GrantRoutineTarget {
	for i := range targets {
		for j := range targets[i].Arguments {
			targets[i].Arguments[j].Type = standaloneType(targets[i].Arguments[j].Type)
		}
	}
	return targets
}
