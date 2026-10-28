package parser

func (l *lexer) enforcementProperty(set, negative *bool, pos *Position, token lexeme, value bool) bool {
	if *set && *negative != value {
		l.failAt("42601", "conflicting constraint properties", token.pos.Offset)
		return false
	}
	if !*set {
		*pos = token.pos
	}
	*set, *negative = true, value
	return true
}

func (l *lexer) elementEnforcement(e *TableElement, token lexeme, negative bool) bool {
	if e.Check != nil {
		c := e.Check
		if !l.enforcementProperty(&c.EnforcementSet, &c.NotEnforced, &c.EnforcementPos, token, negative) {
			return false
		}
		c.NotValid = c.NotValid || negative
		return true
	}
	if e.ForeignKey != nil {
		c := e.ForeignKey
		if !l.enforcementProperty(&c.EnforcementSet, &c.NotEnforced, &c.EnforcementPos, token, negative) {
			return false
		}
		c.NotValid = c.NotValid || negative
		return true
	}
	kind := "UNIQUE"
	switch {
	case e.Unique != nil && e.Unique.Primary:
		kind = "PRIMARY KEY"
	case e.NotNull != nil:
		kind = "NOT NULL"
	case e.Exclusion != nil:
		kind = "EXCLUDE"
	default:
		if e.Column != nil {
			e.Column.Constraints = columnEnforcement(e.Column.Constraints, token, negative)
			return true
		}
	}
	property := "ENFORCED"
	if negative {
		property = "NOT ENFORCED"
	}
	l.deferUnsupported(token, kind+" constraints cannot be marked "+property)
	return true
}

func (l *lexer) alterEnforcement(a *AlterTableAction, token lexeme, negative bool) bool {
	if a.Action == AlterConstraintEnforcement {
		return l.enforcementProperty(&a.EnforcementSet, &a.NotEnforced, &a.EnforcementPos, token, negative)
	}
	e := TableElement{Check: a.Check, ForeignKey: a.ForeignKey, Unique: a.Unique, Exclusion: a.Exclusion}
	if a.Action == AddNotNull {
		e.NotNull = &NotNullConstraint{}
	}
	if a.Action == AddUsingIndex {
		e.Unique = &UniqueConstraint{Primary: a.UsingIndex.Primary}
	}
	return l.elementEnforcement(&e, token, negative)
}

func columnEnforcement(columns []ColumnConstraint, token lexeme, negative bool) []ColumnConstraint {
	if len(columns) == 0 || columns[len(columns)-1].Pos.Offset == 0 {
		// An unattached property is syntax, not a durable primary constraint.
		// Keep each orphan in input order so the utility transform diagnoses
		// the first misplaced clause, even if a real constraint follows it.
		return append(columns, ColumnConstraint{EnforcementSet: true, NotEnforced: negative, EnforcementPos: token.pos})
	}
	c := &columns[len(columns)-1]
	if c.EnforcementSet && c.EnforcementRepeat.Offset == 0 {
		c.EnforcementRepeat = token.pos
	}
	c.EnforcementSet, c.NotEnforced, c.EnforcementPos = true, negative, token.pos
	if c.Check != nil {
		c.Check.NotEnforced, c.Check.NotValid = negative, negative
	}
	if c.ForeignKey != nil {
		c.ForeignKey.NotEnforced, c.ForeignKey.NotValid = negative, negative
	}
	return columns
}

func appendColumnConstraint(columns []ColumnConstraint, constraint ColumnConstraint) []ColumnConstraint {
	if constraint.EnforcementSet && constraint.Pos.Offset == 0 {
		return columnEnforcement(columns, lexeme{pos: constraint.EnforcementPos}, constraint.NotEnforced)
	}
	return append(columns, constraint)
}

// All declaration attachment paths share the existing nondeferred FK policy.
// Enforcement does not terminate the repeated property list.
func (l *lexer) foreignProperty(f *ForeignKeyConstraint, token, value lexeme) bool {
	if f == nil {
		l.syntax(token)
		return false
	}
	switch token.text {
	case "not":
		return true // NOT_DEFERRABLE_LA DEFERRABLE
	case "deferrable":
		l.deferUnsupported(token, "foreign key deferrability is unsupported")
	case "initially":
		if value.keyword == nil || value.text != "immediate" && value.text != "deferred" {
			l.syntax(value)
			return false
		}
		if value.text == "deferred" {
			l.deferUnsupported(value, "foreign key deferrability is unsupported")
		}
	}
	return true
}

func (l *lexer) columnForeignProperty(columns []ColumnConstraint, token, value lexeme) bool {
	if len(columns) == 0 {
		l.syntax(token)
		return false
	}
	return l.foreignProperty(columns[len(columns)-1].ForeignKey, token, value)
}

func (l *lexer) elementForeignProperty(element *TableElement, token, value lexeme) bool {
	if element.Column != nil {
		return l.columnForeignProperty(element.Column.Constraints, token, value)
	}
	return l.foreignProperty(element.ForeignKey, token, value)
}
