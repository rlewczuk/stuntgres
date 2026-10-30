package parser

// XMLTable is a relational source. Options retain written expression order.
type XMLTable struct {
 Namespaces []XMLNamespace
 Row, Document Expr
 Columns []XMLTableColumn
 Lateral bool
 Pos Position
}
type XMLNamespace struct { URI Expr; Name Identifier; Default bool; Pos Position }
type XMLTableOption struct { Kind string; Value Expr; Pos Position }
type XMLTableColumn struct {
 Name Identifier
 Type TypeName
 Ordinality bool
 Options []XMLTableOption
 Pos Position
}
func (*XMLTable) fromItem() {}
func (t *XMLTable) Expressions() []Expr {
 out := make([]Expr, 0, len(t.Namespaces)+2)
 for _, ns := range t.Namespaces { out = append(out, ns.URI) }
 out = append(out, t.Row, t.Document)
 for _, column := range t.Columns { for _, option := range column.Options { if option.Value != nil { out = append(out, option.Value) } } }
 return out
}
func (t *XMLTable) WithExpressions(children []Expr) *XMLTable {
 out := *t
 out.Namespaces = append([]XMLNamespace(nil), t.Namespaces...)
 at := 0
 for i := range out.Namespaces { out.Namespaces[i].URI = children[at]; at++ }
 out.Row, out.Document = children[at], children[at+1]; at += 2
 out.Columns = append([]XMLTableColumn(nil), t.Columns...)
 for i := range out.Columns {
  out.Columns[i].Options = append([]XMLTableOption(nil), t.Columns[i].Options...)
  for j := range out.Columns[i].Options { if out.Columns[i].Options[j].Value != nil { out.Columns[i].Options[j].Value = children[at]; at++ } }
 }
 return &out
}
