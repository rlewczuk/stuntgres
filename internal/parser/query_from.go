package parser

// FromItem preserves join operand and whole-result alias boundaries.
type FromItem interface { fromItem() }
type TableReference struct { Name ObjectName }
func (*TableReference) fromItem() {}
type JoinReference struct {
	Left, Right FromItem
	On Expr
	Using []Identifier
	Natural bool
	UsingAlias *Identifier
	Pos Position
}
func (*JoinReference) fromItem() {}
type AliasReference struct {
	Input FromItem
	Name Identifier
	Columns []Identifier
}
func (*AliasReference) fromItem() {}

// StarExpr is a target item, not a second whole-projection representation.
type StarExpr struct { Qualifier ObjectName; Pos, QualificationPos Position }
func (StarExpr) expression() {}
func (s StarExpr) SourcePosition() Position { return s.Pos }

func FromTables(item FromItem) []*TableReference {
	var result []*TableReference
	var visit func(FromItem)
	visit = func(item FromItem) {
		switch n := item.(type) {
		case *TableReference: result = append(result, n)
		case *JoinReference: visit(n.Left); visit(n.Right)
		case *AliasReference: visit(n.Input)
		}
	}
	visit(item)
	return result
}
