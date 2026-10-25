package parser

// FromItem preserves join operand and whole-result alias boundaries.
type FromItem interface{ fromItem() }
type TableReference struct {
	Name ObjectName
	Only bool // Otherwise scan the named relation and its descendants.
}

func (*TableReference) fromItem() {}

// FunctionFrom is the selected regexp table-function source. Calls in a ROWS
// FROM group run in lockstep; distinct FROM items retain ordinary join behavior.
type FunctionFrom struct {
	Calls               []FunctionExpr
	Ordinality, Lateral bool
	Pos                 Position
}

func (*FunctionFrom) fromItem() {}

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
	Comma       bool // FROM-list combination, not an explicit JOIN namespace.
	Pos         Position
}

func (*JoinReference) fromItem() {}

// FunctionColumnDefinition retains every declared member and source position.
// It is distinct from a range alias's optional name-only column renaming.
type FunctionColumnDefinition struct {
	Name      Identifier
	Type      TypeName
	Collation *ObjectName
}

type AliasReference struct {
	Input             FromItem
	Name              Identifier
	Columns           []Identifier
	ColumnDefinitions []FunctionColumnDefinition
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
