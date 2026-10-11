package fixture

func PartialIndexIDs(q string, ids ...int64) Step {
	var rows [][]any
	for _, id := range ids {
		rows = append(rows, []any{id})
	}
	return Select(q, []string{"id"}, []string{"INT4"}, rows)
}
