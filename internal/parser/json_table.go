package parser

import "unicode/utf8"

// JSONTable is a lateral relational SQL/JSON source, not a routine call.
type JSONTable struct {
	Document, Path Expr
	Input          *JSONFormat
	PathName       *Identifier
	Passing        []SQLJSONPassing
	Columns        []JSONTableColumn
	Error          SQLJSONBehavior
	Lateral        bool
	Pos            Position
	PathPos        Position
}
type JSONTableColumn struct {
	Name               Identifier
	Type               TypeName
	Ordinality, Exists bool
	Format             *JSONFormat
	Path               Expr
	PathName           *Identifier
	Wrapper, Quotes    string
	Empty, Error       SQLJSONBehavior
	Columns            []JSONTableColumn
	Pos                Position
}

func (*JSONTable) fromItem() {}

// Expressions preserves written order, including defaults in inactive branches.
func (t *JSONTable) Expressions() []Expr {
	out := []Expr{t.Document}
	for _, p := range t.Passing {
		out = append(out, p.Value)
	}
	var visit func([]JSONTableColumn)
	visit = func(columns []JSONTableColumn) {
		for _, c := range columns {
			if c.Empty.Default != nil {
				out = append(out, c.Empty.Default)
			}
			if c.Error.Default != nil {
				out = append(out, c.Error.Default)
			}
			visit(c.Columns)
		}
	}
	visit(t.Columns)
	return out
}
func (t *JSONTable) WithExpressions(children []Expr) *JSONTable {
	out := *t
	out.Document = children[0]
	out.Passing = append([]SQLJSONPassing(nil), t.Passing...)
	i := 1
	for j := range out.Passing {
		out.Passing[j].Value = children[i]
		i++
	}
	var replace func([]JSONTableColumn) []JSONTableColumn
	replace = func(columns []JSONTableColumn) []JSONTableColumn {
		result := append([]JSONTableColumn(nil), columns...)
		for j := range result {
			c := &result[j]
			if c.Empty.Default != nil {
				c.Empty.Default = children[i]
				i++
			}
			if c.Error.Default != nil {
				c.Error.Default = children[i]
				i++
			}
			c.Columns = replace(c.Columns)
		}
		return result
	}
	out.Columns = replace(t.Columns)
	return &out
}

// Grouping is normally erased from scalar ASTs. JSON_TABLE's root-path
// diagnostic instead points at the first written token, including '('.
func (l *lexer) jsonTablePathPosition(comma Position) Position {
	scratch := lexer{input: l.input, offset: comma.Offset + 1}
	scratch.skip(true)
	return Position{Offset: scratch.offset, Character: comma.Character + utf8.RuneCountInString(l.input[comma.Offset:scratch.offset])}
}
