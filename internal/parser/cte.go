package parser

// StatementWith exposes the existing WITH namespace for its five legal owners.
func StatementWith(statement Statement) []CommonTableExpression {
	switch s := statement.(type) {
	case *Select:
		return s.With
	case *Insert:
		return s.With
	case *Update:
		return s.With
	case *Delete:
		return s.With
	case *Merge:
		return s.With
	}
	return nil
}

func StatementWithRecursive(statement Statement) bool {
	switch s := statement.(type) {
	case *Select:
		return s.WithRecursive
	case *Insert:
		return s.WithRecursive
	case *Update:
		return s.WithRecursive
	case *Delete:
		return s.WithRecursive
	case *Merge:
		return s.WithRecursive
	}
	return false
}

func ModifyingStatement(statement Statement) bool {
	switch statement.(type) {
	case *Insert, *Update, *Delete, *Merge:
		return true
	}
	return false
}

func HasModifyingCTE(statement Statement) bool {
	for _, definition := range StatementWith(statement) {
		if ModifyingStatement(definition.Query) {
			return true
		}
	}
	return false
}

// VisitMutationTargets visits AST command occurrences, not FROM names. A target
// remains physical even when a visible CTE has the same spelling.
func VisitMutationTargets(statement Statement, visit func(Statement) error) error {
	if explanation, ok := statement.(*Explain); ok {
		statement = explanation.Statement
	}
	switch s := statement.(type) {
	case *CreateQueryTable:
		statement = s.Query
	case *CreateView:
		statement = s.Query
	case *DeclareCursor:
		statement = s.Query
	}
	for _, definition := range StatementWith(statement) {
		if err := VisitMutationTargets(definition.Query, visit); err != nil {
			return err
		}
	}
	if ModifyingStatement(statement) {
		return visit(statement)
	}
	return nil
}

// recursiveViewQuery lowers syntax sugar before source admission. Generated
// names have no source position; the user's body and destination aliases retain
// theirs. In particular, short lists project rather than truncate CTE tuples.
func (l *lexer) recursiveViewQuery(name ObjectName, columns []Identifier, body Statement, suffix []ViewOption, pos Position) (*Select, bool) {
	if len(suffix) != 0 {
		l.failAt("0A000", "WITH CHECK OPTION not supported on recursive views", suffix[0].Name.Pos.Offset)
		return nil, false
	}
	query, ok := body.(*Select)
	if !ok {
		l.failAt("0A000", "view body requires an ordinary SELECT", pos.Offset)
		return nil, false
	}
	return recursiveViewSelect(name, columns, query), true
}

func recursiveViewSelect(name ObjectName, columns []Identifier, body *Select) *Select {
	self := Identifier{Text: name.Parts[len(name.Parts)-1].Text}
	aliases := make([]Identifier, len(columns))
	targets := make([]Expr, len(columns))
	for i, column := range columns {
		aliases[i] = Identifier{Text: column.Text}
		targets[i] = ColumnExpr{Name: ObjectName{Parts: []Identifier{aliases[i]}}}
	}
	return &Select{
		WithRecursive: true,
		With:          []CommonTableExpression{{Name: self, Columns: aliases, Query: body}},
		Targets:       targets,
		From:          &TableReference{Name: ObjectName{Parts: []Identifier{self}}},
	}
}
