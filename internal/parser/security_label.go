package parser

// SecurityLabel carries syntax only. Provider authority and object resolution
// belong to execution, never protocol preparation.
type SecurityLabel struct {
	Kind string
	Name ObjectName
	Type TypeName
	Routine GrantRoutineTarget
	Number Identifier
	Provider Identifier
	ProviderSpecified bool
	Text string
	Null bool
	LabelPos Position
	Pos Position
}

func (*SecurityLabel) statement() {}

// Generic modifiers use the ordinary expression grammar. Retain syntax until
// aggregate identity validation, never for binding, evaluation or parameter
// discovery. Parse discards these trees before exposing the utility statement.
func securityLabelTypeModifiers(expressions []Expr) TypeName {
	modifiers := make([]TypeModifier, len(expressions))
	for i, expression := range expressions {
		modifiers[i] = TypeModifier{NonSimple: true, Pos: expression.SourcePosition(), syntax: expression}
	}
	return TypeName{Modifiers: modifiers}
}

func securityLabelLiteralType(l sqlLexer, tok lexeme) TypeName {
	raw := columnType(tok)
	if l.(*lexer).securityLabelSyntax { raw.Name = tok.text }
	return raw
}

// Empty brackets use native zero. Preserve an explicit zero distinctly until
// raw identity validation; no binder sees this feature-private sentinel.
func securityLabelArrayBound(l sqlLexer, bound int32) int32 {
	if l.(*lexer).securityLabelSyntax && bound == 0 { return -1 }
	return bound
}

func discardSecurityLabelTypeSyntax(raw *TypeName) {
	for i := range raw.Modifiers { raw.Modifiers[i].syntax = nil }
	for i, bound := range raw.ArrayBounds { if bound == -1 { raw.ArrayBounds[i] = 0 } }
}

func (s *SecurityLabel) discardModifierSyntax() {
	discardSecurityLabelTypeSyntax(&s.Type)
	for i := range s.Routine.Arguments {
		discardSecurityLabelTypeSyntax(&s.Routine.Arguments[i].Type)
	}
}
