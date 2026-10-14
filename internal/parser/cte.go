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
