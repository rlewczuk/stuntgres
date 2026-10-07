package parser

// CaseExpr retains the saved simple test rather than duplicating it in WHENs.
type CaseExpr struct {
	Test Expr
	Arms []CaseArm
	Else Expr
	Pos  Position
}
type CaseArm struct {
	Condition, Result Expr
	Pos               Position
}

func (CaseExpr) expression()                {}
func (e CaseExpr) SourcePosition() Position { return e.Pos }

type ConditionalKind uint8

const (
	Coalesce ConditionalKind = iota
	NullIf
	Greatest
	Least
)

func (k ConditionalKind) String() string {
	switch k {
	case Coalesce:
		return "COALESCE"
	case NullIf:
		return "NULLIF"
	case Greatest:
		return "GREATEST"
	case Least:
		return "LEAST"
	}
	return ""
}

type ConditionalExpr struct {
	Kind ConditionalKind
	Args []Expr
	Pos  Position
}

func (ConditionalExpr) expression()                {}
func (e ConditionalExpr) SourcePosition() Position { return e.Pos }
