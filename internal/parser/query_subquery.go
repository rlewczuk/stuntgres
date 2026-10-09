package parser

// RowExpr is structural comparison syntax, not a public composite datum.
type RowExpr struct {
	Fields   []Expr
	Explicit bool
	Pos      Position
}

func (RowExpr) expression()                {}
func (r RowExpr) SourcePosition() Position { return r.Pos }

type SubqueryKind uint8

const (
	SubqueryScalar SubqueryKind = iota
	SubqueryExists
	SubqueryAny
	SubqueryAll
	SubqueryRow
)

type SubqueryExpr struct {
	Kind     SubqueryKind
	Query    *Select
	Left     Expr
	Operator ObjectName
	Negate   bool
	Pos      Position
}

func (SubqueryExpr) expression()                {}
func (s SubqueryExpr) SourcePosition() Position { return s.Pos }

type DerivedQuery struct {
	Query *Select
	Pos   Position
}

func (*DerivedQuery) fromItem() {}

func quantifiedQuery(left Expr, operator ObjectName, kind SubqueryKind, query *Select, negate bool, pos Position) Expr {
	return SubqueryExpr{Kind: kind, Query: query, Left: left, Operator: operator, Negate: negate, Pos: pos}
}
