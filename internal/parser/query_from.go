package parser

// FromItem preserves join operand and whole-result alias boundaries.
type FromItem interface{ fromItem() }
type TableReference struct {
	Name   ObjectName
	Only   bool // Otherwise scan the named relation and its descendants.
	Sample *TableSample
}

func (*TableReference) fromItem() {}

// TableSample retains the method and expressions at their original positions.
type TableSample struct {
	Method     ObjectName
	Arguments  []Expr
	Repeatable Expr
}

func attachTableSample(item FromItem, sample *TableSample) bool {
	switch n := item.(type) {
	case *TableReference:
		if n.Sample != nil {
			return false
		}
		n.Sample = sample
		return true
	case *AliasReference:
		return attachTableSample(n.Input, sample)
	}
	return false
}

// FunctionFrom is the selected regexp table-function source. Calls in a ROWS
// FROM group run in lockstep; distinct FROM items retain ordinary join behavior.
// RowsFrom preserves raw grammar provenance independently of analyzed execution.
type FunctionFrom struct {
	Calls                         []FunctionExpr
	Ordinality, Lateral, RowsFrom bool
	Pos                           Position
}

func (*FunctionFrom) fromItem() {}

// ValuesQuery is a finite query source, not destination-context INSERT input.
type ValuesQuery struct {
	Rows    [][]Expr
	Order   *Order
	Bounds  *QueryBounds
	Pos     Position
	Lateral bool
	rowEnd  int // Closing row token, independent of parser lookahead.
}

func (*ValuesQuery) fromItem() {}

// queryFromSource retains the parenthesized query boundary and written
// LATERAL state, including VALUES queries with their own ordering and bounds.
func queryFromSource(query *Select, pos Position, lateral bool) FromItem {
	if query.Values != nil && len(query.Locking) == 0 && len(query.With) == 0 {
		query.Values.Lateral = lateral
		return query.Values
	}
	return &DerivedQuery{Query: query, Pos: pos, Lateral: lateral}
}

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
