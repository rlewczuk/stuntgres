package core_test

import "testing"

func TestPGStatUserTablesCatalogIdentity(t *testing.T) {
	tableStatisticsCatalogIdentity(t, true)
}

func TestPGStatUserTablesSourceSelection(t *testing.T) {
	tableStatisticsSourceSelection(t, true)
}
