package reference_test

import "testing"

func TestReferencePGStatUserTablesSequentialAccess(t *testing.T) {
	tableStatSequentialAccess(t, tableStatFamily{user: true})
}
func TestReferencePGStatUserTablesIndexAggregation(t *testing.T) {
	tableStatIndexAggregation(t, tableStatFamily{user: true})
}
