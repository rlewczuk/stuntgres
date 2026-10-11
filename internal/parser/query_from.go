package parser

// FromItem preserves join operand and whole-result alias boundaries.
type FromItem interface{ fromItem() }
type TableReference struct {
	Name ObjectName
	Only bool // Otherwise scan the named relation and its descendants.
}

func (*TableReference) fromItem() {}

// ValuesQuery is a finite query source, not destination-context INSERT input.
type ValuesQuery struct {
	Rows   [][]Expr
	Order  *Order
	Bounds *QueryBounds
	Pos    Position
	rowEnd int // Closing row token, independent of parser lookahead.
}

func (*ValuesQuery) fromItem() {}

// JoinKind preserves SQL operand order; zero is the ordinary inner join.
type JoinKind uint8

const (
	JoinInner JoinKind = iota
	JoinLeft
	JoinRight
	JoinFull
)

type JoinReference struct {
	Left, Right FromItem
	Kind        JoinKind
	On          Expr
	Using       []Identifier
	Natural     bool
	UsingAlias  *Identifier
	Pos         Position
}

func (*JoinReference) fromItem() {}

type AliasReference struct {
	Input   FromItem
	Name    Identifier
	Columns []Identifier
}

func (*AliasReference) fromItem() {}

// StarExpr is a target item, not a second whole-projection representation.
type StarExpr struct {
	Qualifier             ObjectName
	Pos, QualificationPos Position
}

func (StarExpr) expression()                {}
func (s StarExpr) SourcePosition() Position { return s.Pos }

func FromTables(item FromItem) []*TableReference {
	var result []*TableReference
	var visit func(FromItem)
	visit = func(item FromItem) {
		switch n := item.(type) {
		case *TableReference:
			result = append(result, n)
		case *JoinReference:
			visit(n.Left)
			visit(n.Right)
		case *AliasReference:
			visit(n.Input)
		}
	}
	visit(item)
	return result
}
