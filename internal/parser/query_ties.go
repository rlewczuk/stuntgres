package parser

func (l *lexer) validateTies(bounds *QueryBounds, order *Order, locking []RowLockClause) bool {
	if bounds == nil || !bounds.WithTies {
		return true
	}
	if order == nil {
		l.failAt("42601", "WITH TIES cannot be specified without ORDER BY clause", bounds.WithPosition.Offset)
		return false
	}
	for _, mark := range locking {
		if mark.Wait == RowLockSkip {
			l.failAt("42601", "SKIP LOCKED and WITH TIES options cannot be used together", bounds.WithPosition.Offset)
			return false
		}
	}
	return true
}
