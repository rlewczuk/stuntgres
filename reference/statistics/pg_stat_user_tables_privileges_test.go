package reference_test

import "testing"

func TestReferencePGStatUserTablesPrivileges(t *testing.T) {
	tableStatPrivileges(t, tableStatFamily{user: true})
}
func TestReferencePGStatUserTablesReadOnly(t *testing.T) {
	tableStatReadOnly(t, tableStatFamily{user: true})
}
