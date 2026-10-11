package parser

// CREATE retains its own modifier boundary; only selected ALTER attachments
// reach this helper. Deferring capability errors preserves complete-input syntax.
func (l *lexer) alterConstraintValidity(a *AlterTableAction, not, modifier lexeme) bool {
	if modifier.text != "valid" {
		message := "constraint modifiers are unsupported"
		if a.Action == AddNotNull {
			message = "NOT NULL modifiers are unsupported"
		}
		l.deferUnsupported(not, message)
		return true
	}
	// Released constraint attributes accumulate bits; repeated NOT VALID is
	// harmless. Keep the first positioned modifier for ineligible-kind errors.
	if a.ValidityPos.Offset == 0 {
		a.ValidityPos = not.pos
	}
	switch a.Action {
	case AddNotNull:
		a.NotValid = true
	case AddCheck:
		a.Check.NotValid = true
	case AddForeignKey:
		a.ForeignKey.NotValid = true
	case AddUnique, AddUsingIndex:
		message := "UNIQUE constraints cannot be marked NOT VALID"
		if a.Action == AddUnique && a.Unique.Primary || a.Action == AddUsingIndex && a.UsingIndex.Primary {
			message = "PRIMARY KEY constraints cannot be marked NOT VALID"
		}
		l.deferUnsupported(not, message)
	case AddExclusion:
		l.deferUnsupported(not, "EXCLUDE constraints cannot be marked NOT VALID")
	}
	return true
}

func (l *lexer) alterConstraintNoInherit(a *AlterTableAction, no lexeme) bool {
	if a.Action == AddCheck {
		a.Check.NoInherit = true
		return true
	}
	if a.Action == AddNotNull {
		l.deferUnsupported(no, "NOT NULL modifiers are unsupported")
		return true
	}
	l.syntax(no)
	return false
}
