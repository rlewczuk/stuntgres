package fixture

func SQLJSONQueryOne(q, typ string, want any, args ...any) Step {
	return Select(q, []string{"v"}, []string{typ}, [][]any{{want}}, args...)
}
