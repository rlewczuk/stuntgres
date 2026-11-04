package core_test

import "testing"

func TestPGStatUserTablesSchemaReader(t *testing.T) {
	tableStatisticsSchemaReader(t, true)
}
