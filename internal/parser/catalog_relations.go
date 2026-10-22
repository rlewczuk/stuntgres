package parser

import "strings"

// CatalogRelationFunctionName selects the ordinary typed catalog information
// calls in existing scalar, stored-expression and record-valued FROM lanes.
func CatalogRelationFunctionName(name string) bool {
	return strings.EqualFold(name, "pg_get_userbyid") || strings.EqualFold(name, "pg_get_catalog_foreign_keys") || strings.EqualFold(name, "pg_extension_update_paths") || strings.EqualFold(name, "pg_tablespace_location") || strings.EqualFold(name, "pg_tablespace_databases")
}
