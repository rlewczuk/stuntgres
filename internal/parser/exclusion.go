package parser

// ExclusionConstraint keeps original index syntax and operator source positions.
// Deferrability is definition state, not the transaction's READ DEFERRABLE mode.
type ExclusionConstraint struct {
	Name              Identifier
	Index             CreateIndex
	Operators         []ObjectName
	Deferrable        bool
	InitiallyDeferred bool
	DeferrabilitySet  bool
	InitiallySet      bool
	Pos               Position
}

type ExclusionElement struct {
	Key      IndexKey
	Operator ObjectName
}

type SetConstraints struct {
	Names    []ObjectName
	All      bool
	Deferred bool
	Pos      Position
}

func (*SetConstraints) statement() {}

func exclusionProperty(l *lexer, c *ExclusionConstraint, token lexeme, deferrability, value bool) bool {
	return constraintProperty(l, token, deferrability, value, &c.Deferrable, &c.InitiallyDeferred, &c.DeferrabilitySet, &c.InitiallySet)
}

func constraintProperty(l *lexer, token lexeme, deferrability, value bool, deferrable, initiallyDeferred, deferrabilitySet, initiallySet *bool) bool {
	if deferrability {
		if *deferrabilitySet && *deferrable != value {
			l.failAt("42601", "conflicting constraint properties", token.pos.Offset)
			return false
		}
		*deferrabilitySet, *deferrable = true, value
	} else {
		if *initiallySet && *initiallyDeferred != value {
			l.failAt("42601", "conflicting constraint properties", token.pos.Offset)
			return false
		}
		*initiallySet, *initiallyDeferred = true, value
	}
	if *initiallyDeferred {
		if *deferrabilitySet && !*deferrable {
			l.failAt("42601", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", token.pos.Offset)
			return false
		}
		*deferrable = true
	}
	return true
}

func usingIndexProperty(l *lexer, c *UsingIndexConstraint, token lexeme, deferrability, value bool) bool {
	if !constraintProperty(l, token, deferrability, value, &c.deferrable, &c.initiallyDeferred, &c.deferrabilitySet, &c.initiallySet) {
		return false
	}
	if value {
		message := "UNIQUE deferrability is unsupported"
		if c.Primary {
			message = "PRIMARY KEY deferrability is unsupported"
		}
		l.deferUnsupported(token, message)
	}
	return true
}

type AlterTableRenameConstraint struct {
	Name                ObjectName
	Constraint, NewName Identifier
	Only, IfExists      bool
	Pos                 Position
	ConstraintPos       Position
}

func (*AlterTableRenameConstraint) statement() {}
