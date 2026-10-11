package fixture

func RegclassRows(query, column string, values ...any) Step {
	rows := make([][]any, len(values))
	for i, value := range values {
		rows[i] = []any{value}
	}
	return Select(query, []string{column}, []string{"REGCLASS"}, rows)
}
