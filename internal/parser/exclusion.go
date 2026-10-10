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
	if deferrability {
		if c.DeferrabilitySet && c.Deferrable != value {
			l.failAt("42601", "conflicting constraint properties", token.pos.Offset)
			return false
		}
		c.DeferrabilitySet, c.Deferrable = true, value
	} else {
		if c.InitiallySet && c.InitiallyDeferred != value {
			l.failAt("42601", "conflicting constraint properties", token.pos.Offset)
			return false
		}
		c.InitiallySet, c.InitiallyDeferred = true, value
	}
	if c.InitiallyDeferred {
		if c.DeferrabilitySet && !c.Deferrable {
			l.failAt("42601", "constraint declared INITIALLY DEFERRED must be DEFERRABLE", token.pos.Offset)
			return false
		}
		c.Deferrable = true
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
