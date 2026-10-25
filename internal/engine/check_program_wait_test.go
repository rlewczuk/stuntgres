package engine

func checkProgramExpression(folded bool) string {
	if folded {
		return "pg_catalog.json_array_length('[1,2]'::json)>0"
	}
	return "pg_catalog.json_array_length(j)>0"
}
