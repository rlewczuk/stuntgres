package parser

type constraintTiming struct {
	deferrable, initiallyDeferred, deferrabilitySet, initiallySet bool
}

func columnTimingAttribute(token lexeme, deferrability, value bool) *ColumnConstraint {
	c := &ColumnConstraint{TimingPos: token.pos}
	if deferrability {
		c.DeferrabilitySet, c.Deferrable = true, value
	} else {
		c.InitiallySet, c.InitiallyDeferred = true, value
	}
	c.TimingError = misplacedTiming(deferrability, value)
	return c
}

func misplacedTiming(deferrability, value bool) string {
	if deferrability {
		if value {
			return "misplaced DEFERRABLE clause"
		}
		return "misplaced NOT DEFERRABLE clause"
	}
	if value {
		return "misplaced INITIALLY DEFERRED clause"
	}
	return "misplaced INITIALLY IMMEDIATE clause"
}

// Timing attributes on columns are transformed at utility execution, unlike
// table properties, whose conflicts are grammar errors. Keep the first failing
// attribute with its original position without rejecting a valid parse prefix.
func columnTiming(columns []ColumnConstraint, token lexeme, deferrability, value bool) []ColumnConstraint {
	if len(columns) == 0 || columns[len(columns)-1].Pos.Character == 0 {
		columns = append(columns, ColumnConstraint{})
	}
	c := &columns[len(columns)-1]
	message := ""
	switch {
	case c.Unique == nil && c.ForeignKey == nil:
		message = misplacedTiming(deferrability, value)
	case deferrability && c.DeferrabilitySet:
		message = "multiple DEFERRABLE/NOT DEFERRABLE clauses not allowed"
	case !deferrability && c.InitiallySet:
		message = "multiple INITIALLY IMMEDIATE/DEFERRED clauses not allowed"
	}
	if deferrability {
		c.DeferrabilitySet, c.Deferrable = true, value
	} else {
		c.InitiallySet, c.InitiallyDeferred = true, value
	}
	if message == "" && c.InitiallyDeferred && c.DeferrabilitySet && !c.Deferrable {
		message = "constraint declared INITIALLY DEFERRED must be DEFERRABLE"
	}
	if message != "" && c.TimingError == "" {
		c.TimingError, c.TimingPos = message, token.pos
	}
	if c.InitiallyDeferred && !c.DeferrabilitySet {
		c.Deferrable = true
	}
	if c.Unique != nil {
		c.Unique.Deferrable, c.Unique.InitiallyDeferred = c.Deferrable, c.InitiallyDeferred
	}
	if c.ForeignKey != nil {
		c.ForeignKey.Deferrable, c.ForeignKey.InitiallyDeferred = c.Deferrable, c.InitiallyDeferred
	}
	return columns
}

func (l *lexer) elementTiming(e *TableElement, token lexeme, deferrability, value bool) bool {
	if e.Column != nil {
		e.Column.Constraints = columnTiming(e.Column.Constraints, token, deferrability, value)
		return true
	}
	switch {
	case e.Unique != nil:
		c := e.Unique
		return constraintProperty(l, token, deferrability, value, &c.Deferrable, &c.InitiallyDeferred, &c.deferrabilitySet, &c.initiallySet)
	case e.ForeignKey != nil:
		c := e.ForeignKey
		return constraintProperty(l, token, deferrability, value, &c.Deferrable, &c.InitiallyDeferred, &c.deferrabilitySet, &c.initiallySet)
	case e.Exclusion != nil:
		return exclusionProperty(l, e.Exclusion, token, deferrability, value)
	}
	kind := "CHECK"
	var timing *constraintTiming
	if e.NotNull != nil {
		kind, timing = "NOT NULL", &e.NotNull.timing
	} else if e.Check != nil {
		timing = &e.Check.timing
	}
	if timing == nil {
		l.syntax(token)
		return false
	}
	if !constraintProperty(l, token, deferrability, value, &timing.deferrable, &timing.initiallyDeferred, &timing.deferrabilitySet, &timing.initiallySet) {
		return false
	}
	if timing.deferrable {
		l.deferUnsupported(token, kind+" constraints cannot be marked DEFERRABLE")
	}
	return true
}

func (l *lexer) foreignProperty(c *ForeignKeyConstraint, token lexeme, deferrability, value bool) bool {
	return constraintProperty(l, token, deferrability, value, &c.Deferrable, &c.InitiallyDeferred, &c.deferrabilitySet, &c.initiallySet)
}

func (l *lexer) uniqueProperty(c *UniqueConstraint, token lexeme, deferrability, value bool) bool {
	return constraintProperty(l, token, deferrability, value, &c.Deferrable, &c.InitiallyDeferred, &c.deferrabilitySet, &c.initiallySet)
}

func (l *lexer) alterTiming(a *AlterTableAction, token lexeme, deferrability, value bool) bool {
	if a.Action == AlterConstraintTiming || a.Action == AlterConstraintEnforcement {
		return constraintProperty(l, token, deferrability, value, &a.Deferrable, &a.InitiallyDeferred, &a.DeferrabilitySet, &a.InitiallySet)
	}
	if a.UsingIndex != nil {
		return usingIndexProperty(l, a.UsingIndex, token, deferrability, value)
	}
	if a.Action == AddNotNull {
		if !constraintProperty(l, token, deferrability, value, &a.Deferrable, &a.InitiallyDeferred, &a.DeferrabilitySet, &a.InitiallySet) {
			return false
		}
		if a.Deferrable {
			l.deferUnsupported(token, "NOT NULL constraints cannot be marked DEFERRABLE")
		}
		return true
	}
	e := TableElement{Unique: a.Unique, ForeignKey: a.ForeignKey, Check: a.Check, Exclusion: a.Exclusion}
	return l.elementTiming(&e, token, deferrability, value)
}
