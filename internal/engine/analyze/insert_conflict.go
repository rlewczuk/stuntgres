package analyze

import (
	"stuntgres/internal/engine/exec"
	"stuntgres/internal/engine/expr"
	"stuntgres/internal/engine/ids"
	"stuntgres/internal/sqltype"
)

// ConflictKey is one ON CONFLICT inference element: a column attribute or an
// expression, with its optional collation and operator class.
type ConflictKey struct {
	Attribute  int32
	Expression expr.Expr
	Collation  uint32
	Class      sqltype.IndexClass
}

// InsertConflict is the bound ON CONFLICT clause: inference keys and
// predicate or named constraint, read manifests, and the DO UPDATE action.
// Arbiter selection and write state belong to the engine's per-execution
// state, which refers to this clause.
type InsertConflict struct {
	Keys          []ConflictKey
	Predicate     expr.Expr
	Constraint    ids.ObjectAddress
	NamedIndex    ids.ObjectAddress
	Reads         []int
	Targets       []int
	Update        *UpdateBinding
	UpdateTargets []int
	ActionReads   []int
	Sources       []exec.Leaf
	Returning     *Returning
	Width         int
}

// Clone returns a copy of the clause header for one command, so execution can
// replace its normalized predicate and preflighted action without changing
// the original. The analyzed expressions and manifests are immutable and stay
// shared. A nil clause stays nil.
func (c *InsertConflict) Clone() *InsertConflict {
	if c == nil {
		return nil
	}
	clone := *c
	return &clone
}
