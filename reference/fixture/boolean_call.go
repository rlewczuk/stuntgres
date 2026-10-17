package fixture

// BooleanCallRelationList is the original psql relation-list query, unchanged.
const BooleanCallRelationList = `SELECT n.nspname as "Schema",
  c.relname as "Name",
  CASE c.relkind WHEN 'r' THEN 'table' WHEN 'v' THEN 'view' WHEN 'm' THEN 'materialized view' WHEN 'i' THEN 'index' WHEN 'S' THEN 'sequence' WHEN 't' THEN 'TOAST table' WHEN 'f' THEN 'foreign table' WHEN 'p' THEN 'partitioned table' WHEN 'I' THEN 'partitioned index' END as "Type",
  pg_catalog.pg_get_userbyid(c.relowner) as "Owner"
FROM pg_catalog.pg_class c
     LEFT JOIN pg_catalog.pg_namespace n ON n.oid = c.relnamespace
     LEFT JOIN pg_catalog.pg_am am ON am.oid = c.relam
WHERE c.relkind IN ('r','p','')
      AND n.nspname <> 'pg_catalog'
      AND n.nspname !~ '^pg_toast'
      AND n.nspname <> 'information_schema'
  AND pg_catalog.pg_table_is_visible(c.oid)
ORDER BY 1,2;`

// BooleanCallRelationSetup creates only supported objects, with independently
// configured owner and namespace identities. Call after the empty-list phase.
func BooleanCallRelationSetup() []string {
	queries := []string{"CREATE ROLE bcp_list_owner"}
	for _, schema := range []string{"bcp_list_a", "bcp_list_b", "bcp_list_off"} {
		queries = append(queries, "CREATE SCHEMA "+schema)
	}
	for _, name := range []string{"bcp_list_a.alpha", "bcp_list_a.shared", "bcp_list_b.beta", "bcp_list_b.shared", "bcp_list_off.hidden"} {
		queries = append(queries, "CREATE TABLE "+name+"(id int)", "ALTER TABLE "+name+" OWNER TO bcp_list_owner")
	}
	return queries
}

func BooleanCallRelationRows(reverse bool) [][]any {
	if reverse {
		return [][]any{{"bcp_list_a", "alpha", "table", "bcp_list_owner"}, {"bcp_list_b", "beta", "table", "bcp_list_owner"}, {"bcp_list_b", "shared", "table", "bcp_list_owner"}}
	}
	return [][]any{{"bcp_list_a", "alpha", "table", "bcp_list_owner"}, {"bcp_list_a", "shared", "table", "bcp_list_owner"}, {"bcp_list_b", "beta", "table", "bcp_list_owner"}}
}
