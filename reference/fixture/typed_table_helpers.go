package fixture

func TypedTableAttachment(table, source string, attached bool) Step {
	q := "SELECT reloftype=" + "'" + source + "'::regtype::oid AS attached FROM pg_class WHERE oid='" + table + "'::regclass::oid"
	if !attached {
		q = "SELECT reloftype=0 AS attached FROM pg_class WHERE oid='" + table + "'::regclass::oid"
	}
	return Select(q, []string{"attached"}, []string{"BOOL"}, [][]any{{true}})
}
